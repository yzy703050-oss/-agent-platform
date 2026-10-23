package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"agent-platform/backend/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const runColumns = "id,conversation_id,user_id,page_id,status,attempt,version,context_eligible"

func scanRun(row pgx.Row) (model.Run, error) {
	var r model.Run
	err := row.Scan(&r.ID, &r.ConversationID, &r.UserID, &r.PageID, &r.Status, &r.Attempt, &r.Version, &r.ContextEligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, model.ErrNotFound
	}
	return r, err
}
func lockConversation(ctx context.Context, tx pgx.Tx, scope model.Scope, id string) error {
	var got string
	err := tx.QueryRow(ctx, "SELECT id FROM conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE", id, scope.UserID).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}
func lockRun(ctx context.Context, tx pgx.Tx, scope model.Scope, id string) (model.Run, error) {
	var conv string
	err := tx.QueryRow(ctx, "SELECT conversation_id FROM agent_runs WHERE id=$1 AND user_id=$2", id, scope.UserID).Scan(&conv)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Run{}, model.ErrNotFound
	}
	if err != nil {
		return model.Run{}, err
	}
	if err = lockConversation(ctx, tx, scope, conv); err != nil {
		return model.Run{}, err
	}
	return scanRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM agent_runs WHERE id=$1 AND user_id=$2 FOR UPDATE", id, scope.UserID))
}
func appendEvent(ctx context.Context, tx pgx.Tx, run model.Run, kind string) error {
	_, err := tx.Exec(ctx, "INSERT INTO run_events(run_id,seq,attempt,type) SELECT $1,COALESCE(MAX(seq),0)+1,$2,$3 FROM run_events WHERE run_id=$1", run.ID, run.Attempt, kind)
	return err
}
func appendMessage(ctx context.Context, tx pgx.Tx, run model.Run, role, content string) error {
	_, err := tx.Exec(ctx, "INSERT INTO messages(id,conversation_id,run_id,user_id,role,content,ordinal) SELECT $1,$2,$3,$4,$5,$6,COALESCE(MAX(ordinal),0)+1 FROM messages WHERE conversation_id=$2",
		uuid.NewString(), run.ConversationID, run.ID, run.UserID, role, content)
	return err
}
func (r *Repository) AcceptRun(ctx context.Context, in model.AdmitRunInput) (model.Run, error) {
	return r.admit(ctx, in, false)
}
func (r *Repository) SupersedePausedRun(ctx context.Context, in model.AdmitRunInput) (model.Run, error) {
	return r.admit(ctx, in, true)
}
func (r *Repository) admit(ctx context.Context, in model.AdmitRunInput, supersede bool) (model.Run, error) {
	if in.IdempotencyKey == "" || in.Content == "" || in.RequestHash == "" {
		return model.Run{}, model.ErrConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Run{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockConversation(ctx, tx, in.Scope, in.ConversationID); err != nil {
		return model.Run{}, err
	}
	existing, err := scanRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM agent_runs WHERE user_id=$1 AND conversation_id=$2 AND idempotency_key=$3", in.Scope.UserID, in.ConversationID, in.IdempotencyKey))
	if err == nil {
		var hash string
		if err = tx.QueryRow(ctx, "SELECT request_hash FROM agent_runs WHERE id=$1", existing.ID).Scan(&hash); err != nil {
			return model.Run{}, err
		}
		if hash != in.RequestHash {
			return model.Run{}, model.ErrIdempotencyConflict
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, model.ErrNotFound) {
		return model.Run{}, err
	}
	var validPage bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM page_leases p JOIN sessions s ON s.id=p.session_id WHERE p.id=$1 AND p.user_id=$2 AND p.session_id=$3 AND p.closed_at IS NULL AND p.expires_at>now() AND s.revoked_at IS NULL AND s.expires_at>now())", in.Scope.PageID, in.Scope.UserID, in.Scope.SessionID).Scan(&validPage)
	if err != nil {
		return model.Run{}, err
	}
	if !validPage {
		return model.Run{}, model.ErrNotFound
	}
	old, err := scanRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM agent_runs WHERE conversation_id=$1 AND status IN('accepted','running','pausing','paused','resuming','cancelling') FOR UPDATE", in.ConversationID))
	if err == nil {
		if !supersede || old.Status != "paused" {
			return model.Run{}, model.ErrConflict
		}
		if _, err = tx.Exec(ctx, "UPDATE agent_runs SET status='cancelled',error_code='replaced_by_new_query',context_eligible=false,version=version+1,updated_at=now() WHERE id=$1", old.ID); err != nil {
			return model.Run{}, err
		}
		if _, err = tx.Exec(ctx, "UPDATE checkpoints SET valid=false WHERE run_id=$1", old.ID); err != nil {
			return model.Run{}, err
		}
		if err = appendEvent(ctx, tx, old, "run.cancelled"); err != nil {
			return model.Run{}, err
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Run{}, err
	}
	run := model.Run{ID: uuid.NewString(), UserID: in.Scope.UserID, ConversationID: in.ConversationID, PageID: in.Scope.PageID, Status: "accepted", Attempt: 1, Version: 1, ContextEligible: true}
	_, err = tx.Exec(ctx, "INSERT INTO agent_runs(id,user_id,conversation_id,page_id,idempotency_key,request_hash,status) VALUES($1,$2,$3,$4,$5,$6,'accepted')", run.ID, run.UserID, run.ConversationID, run.PageID, in.IdempotencyKey, in.RequestHash)
	if err != nil {
		return model.Run{}, err
	}
	if err = appendMessage(ctx, tx, run, "user", in.Content); err != nil {
		return model.Run{}, err
	}
	if err = appendEvent(ctx, tx, run, "run.accepted"); err != nil {
		return model.Run{}, err
	}
	return run, tx.Commit(ctx)
}
func (r *Repository) CompleteRun(ctx context.Context, in model.FinishRunInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	run, err := lockRun(ctx, tx, in.Scope, in.RunID)
	if err != nil {
		return err
	}
	if run.Attempt != in.Attempt || run.Status != "running" {
		return model.ErrConflict
	}
	if err = appendMessage(ctx, tx, run, "assistant", in.FinalText); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE agent_runs SET status='succeeded',version=version+1,updated_at=now() WHERE id=$1", run.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE checkpoints SET valid=false WHERE run_id=$1", run.ID); err != nil {
		return err
	}
	if err = appendEvent(ctx, tx, run, "run.completed"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) SaveCheckpoint(ctx context.Context, in model.CheckpointWrite) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	run, err := lockRun(ctx, tx, in.Scope, in.Checkpoint.RunID)
	if err != nil {
		return err
	}
	cp := in.Checkpoint
	if run.Attempt != cp.Attempt || (run.Status != "running" && run.Status != "pausing" && run.Status != "resuming") {
		return model.ErrConflict
	}
	if len(cp.Payload) == 0 || cp.Engine == "" || cp.EngineVersion == "" || cp.FormatVersion < 1 || cp.Fingerprint == "" {
		return model.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO checkpoints(run_id,user_id,conversation_id,engine,engine_version,format_version,attempt,fingerprint,payload,valid)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true)
 ON CONFLICT(run_id) DO UPDATE SET engine=excluded.engine,engine_version=excluded.engine_version,format_version=excluded.format_version,attempt=excluded.attempt,fingerprint=excluded.fingerprint,payload=excluded.payload,valid=true,created_at=now()`,
		run.ID, run.UserID, run.ConversationID, cp.Engine, cp.EngineVersion, cp.FormatVersion, run.Attempt, cp.Fingerprint, cp.Payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) GetCheckpoint(ctx context.Context, scope model.Scope, id string) (model.Checkpoint, bool, error) {
	var own bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agent_runs r JOIN conversations c ON c.id=r.conversation_id WHERE r.id=$1 AND r.user_id=$2 AND c.deleted_at IS NULL)", id, scope.UserID).Scan(&own)
	if err != nil {
		return model.Checkpoint{}, false, err
	}
	if !own {
		return model.Checkpoint{}, false, model.ErrNotFound
	}
	var cp model.Checkpoint
	err = r.pool.QueryRow(ctx, "SELECT run_id,user_id,conversation_id,engine,engine_version,format_version,attempt,fingerprint,payload,valid FROM checkpoints WHERE run_id=$1 AND user_id=$2 AND valid", id, scope.UserID).Scan(&cp.RunID, &cp.UserID, &cp.ConversationID, &cp.Engine, &cp.EngineVersion, &cp.FormatVersion, &cp.Attempt, &cp.Fingerprint, &cp.Payload, &cp.Valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Checkpoint{}, false, nil
	}
	return cp, err == nil, err
}
func (r *Repository) InvalidateCheckpoint(ctx context.Context, scope model.Scope, id string, attempt int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	run, err := lockRun(ctx, tx, scope, id)
	if err != nil {
		return err
	}
	if run.Attempt != attempt {
		return model.ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE checkpoints SET valid=false WHERE run_id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) FinalizePause(ctx context.Context, in model.FinalizePauseInput) (model.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Run{}, err
	}
	defer tx.Rollback(ctx)
	run, err := lockRun(ctx, tx, in.Scope, in.RunID)
	if err != nil {
		return model.Run{}, err
	}
	if run.Attempt != in.Attempt || run.Status != "pausing" {
		return model.Run{}, model.ErrConflict
	}
	var valid bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM checkpoints WHERE run_id=$1 AND attempt=$2 AND valid)", run.ID, run.Attempt).Scan(&valid)
	if err != nil {
		return model.Run{}, err
	}
	if !valid {
		return model.Run{}, model.ErrConflict
	}
	run, err = scanRun(tx.QueryRow(ctx, "UPDATE agent_runs SET status='paused',version=version+1,updated_at=now() WHERE id=$1 RETURNING "+runColumns, run.ID))
	if err != nil {
		return model.Run{}, err
	}
	if err = appendEvent(ctx, tx, run, "run.paused"); err != nil {
		return model.Run{}, err
	}
	return run, tx.Commit(ctx)
}
func (r *Repository) ListContext(ctx context.Context, scope model.Scope, conversationID, runID string) ([]model.Message, error) {
	var own bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)", conversationID, scope.UserID).Scan(&own)
	if err != nil {
		return nil, err
	}
	if !own {
		return nil, model.ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT m.id,m.conversation_id,m.run_id,m.role,m.content,m.tool_call_id,m.ordinal,m.tool_calls FROM messages m JOIN agent_runs r ON r.id=m.run_id WHERE m.conversation_id=$1 AND m.user_id=$2 AND r.context_eligible AND (r.status='succeeded' OR r.id=$3) ORDER BY m.ordinal`, conversationID, scope.UserID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Message{}
	for rows.Next() {
		var m model.Message
		var calls []byte
		if err = rows.Scan(&m.ID, &m.ConversationID, &m.RunID, &m.Role, &m.Content, &m.ToolCallID, &m.Ordinal, &calls); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(calls, &m.ToolCalls); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
