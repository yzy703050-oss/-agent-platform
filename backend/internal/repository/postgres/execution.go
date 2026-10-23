package postgres

import (
	"agent-platform/backend/internal/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
)

func (r *Repository) GetRun(ctx context.Context, s model.Scope, id string) (model.Run, error) {
	return scanRun(r.pool.QueryRow(ctx, "SELECT "+runColumns+" FROM agent_runs WHERE id=$1 AND user_id=$2 AND conversation_id IN(SELECT id FROM conversations WHERE deleted_at IS NULL)", id, s.UserID))
}
func (r *Repository) FindIdempotentRun(ctx context.Context, in model.AdmitRunInput) (model.Run, error) {
	run, err := scanRun(r.pool.QueryRow(ctx, "SELECT "+runColumns+" FROM agent_runs WHERE user_id=$1 AND conversation_id=$2 AND idempotency_key=$3", in.Scope.UserID, in.ConversationID, in.IdempotencyKey))
	if err != nil {
		return run, err
	}
	var hash string
	if err = r.pool.QueryRow(ctx, "SELECT request_hash FROM agent_runs WHERE id=$1", run.ID).Scan(&hash); err != nil {
		return model.Run{}, err
	}
	if hash != in.RequestHash {
		return model.Run{}, model.ErrIdempotencyConflict
	}
	return run, nil
}
func (r *Repository) TransitionRun(ctx context.Context, in model.TransitionRunInput) (model.Run, error) {
	allowed := map[string]bool{"accepted:running": true, "accepted:failed": true, "running:failed": true, "running:pausing": true, "resuming:running": true, "resuming:failed": true, "pausing:interrupted": true, "running:interrupted": true, "accepted:interrupted": true, "resuming:interrupted": true, "running:cancelling": true, "pausing:cancelling": true, "paused:cancelling": true, "accepted:cancelling": true, "resuming:cancelling": true, "cancelling:cancelled": true, "cancelling:interrupted": true}
	if !allowed[in.From+":"+in.To] {
		return model.Run{}, model.ErrConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Run{}, err
	}
	defer tx.Rollback(ctx)
	run, err := lockRun(ctx, tx, in.Scope, in.RunID)
	if err != nil {
		return run, err
	}
	if run.Attempt != in.Attempt || run.Status != in.From {
		return model.Run{}, model.ErrConflict
	}
	run, err = scanRun(tx.QueryRow(ctx, "UPDATE agent_runs SET status=$2,error_code=$3,version=version+1,updated_at=now() WHERE id=$1 RETURNING "+runColumns, run.ID, in.To, in.ErrorCode))
	if err != nil {
		return run, err
	}
	if in.To == "failed" || in.To == "cancelled" || in.To == "interrupted" {
		if _, err = tx.Exec(ctx, "UPDATE checkpoints SET valid=false WHERE run_id=$1", run.ID); err != nil {
			return model.Run{}, err
		}
	}
	if err = appendEvent(ctx, tx, run, "run."+in.To); err != nil {
		return model.Run{}, err
	}
	return run, tx.Commit(ctx)
}
func (r *Repository) AppendRunMessage(ctx context.Context, s model.Scope, id string, attempt int64, m model.Message) error {
	if m.Role != "tool" && (m.Role != "assistant" || len(m.ToolCalls) == 0) {
		return model.ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	run, err := lockRun(ctx, tx, s, id)
	if err != nil {
		return err
	}
	if run.Attempt != attempt || (run.Status != "running" && run.Status != "pausing") {
		return model.ErrConflict
	}
	calls := m.ToolCalls
	if calls == nil {
		calls = []model.ToolCall{}
	}
	b, err := json.Marshal(calls)
	if err != nil {
		return err
	}
	identity := m.ToolCallID
	if m.Role == "assistant" {
		for _, call := range calls {
			identity += "/" + call.ID
		}
	}
	if identity == "" {
		return model.ErrInvalidInput
	}
	digest := sha256.Sum256([]byte(m.Role + ":" + identity))
	key := hex.EncodeToString(digest[:])
	_, err = tx.Exec(ctx, `INSERT INTO messages(id,conversation_id,run_id,user_id,role,content,tool_call_id,tool_calls,attempt,source_key,ordinal) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE(MAX(ordinal),0)+1 FROM messages WHERE conversation_id=$2 ON CONFLICT(run_id,source_key) WHERE source_key<>'' DO NOTHING`, uuid.NewString(), run.ConversationID, run.ID, run.UserID, m.Role, m.Content, m.ToolCallID, b, attempt, key)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
