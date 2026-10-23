package postgres

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"github.com/google/uuid"
)

func (r *Repository) ListRunEvents(ctx context.Context, s model.Scope, id string, after int64) ([]contracts.Event, error) {
	if _, e := r.GetRun(ctx, s, id); e != nil {
		return nil, e
	}
	rows, e := r.pool.Query(ctx, "SELECT run_id,attempt,seq,type,payload FROM run_events WHERE run_id=$1 AND seq>$2 ORDER BY seq LIMIT 1000", id, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contracts.Event{}
	for rows.Next() {
		var v contracts.Event
		if e = rows.Scan(&v.RunID, &v.Attempt, &v.Seq, &v.Type, &v.Payload); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) AppendRunEvent(ctx context.Context, s model.Scope, event contracts.Event) error {
	if event.Type != "tool.started" && event.Type != "tool.completed" && event.Type != "tool.failed" && event.Type != "travel.phase" {
		return model.ErrInvalidInput
	}
	if len(event.Payload) > 16000 || !json.Valid(event.Payload) {
		return model.ErrInvalidInput
	}
	if event.Type == "travel.phase" {
		if err := contracts.ValidateTravelPhase(event.Payload); err != nil {
			return err
		}
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	run, e := lockRun(ctx, tx, s, event.RunID)
	if e != nil {
		return e
	}
	if run.Attempt != event.Attempt || (run.Status != "running" && run.Status != "pausing") {
		return model.ErrConflict
	}
	if event.Type == "travel.phase" {
		var kind string
		if e = tx.QueryRow(ctx, "SELECT agent_type FROM conversations WHERE id=$1", run.ConversationID).Scan(&kind); e != nil {
			return e
		}
		if kind != "travel" {
			return model.ErrInvalidInput
		}
	}
	_, e = tx.Exec(ctx, "INSERT INTO run_events(run_id,seq,attempt,type,payload) SELECT $1,COALESCE(MAX(seq),0)+1,$2,$3,$4 FROM run_events WHERE run_id=$1", run.ID, run.Attempt, event.Type, event.Payload)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *Repository) SavePartialOutput(ctx context.Context, s model.Scope, id string, attempt int64, text string) error {
	if len(text) > 256000 {
		return model.ErrInvalidInput
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	run, e := lockRun(ctx, tx, s, id)
	if e != nil {
		return e
	}
	if run.Attempt != attempt || (run.Status != "running" && run.Status != "pausing" && run.Status != "cancelling") {
		return model.ErrConflict
	}
	if _, e = tx.Exec(ctx, "UPDATE agent_runs SET partial_output=$2 WHERE id=$1", id, text); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO messages(id,conversation_id,run_id,user_id,role,content,attempt,source_key,display_status,ordinal) SELECT $1,$2,$3,$4,'assistant',$5,$6,'partial','partial',COALESCE(MAX(ordinal),0)+1 FROM messages WHERE conversation_id=$2 ON CONFLICT(run_id,source_key) WHERE source_key<>'' DO UPDATE SET content=excluded.content,attempt=excluded.attempt`, uuid.NewString(), run.ConversationID, id, run.UserID, text, attempt)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
