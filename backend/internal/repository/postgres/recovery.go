package postgres

import (
	"agent-platform/backend/internal/model"
	"context"
)

func (r *Repository) BeginResume(ctx context.Context, s model.Scope, id string) (model.Run, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return model.Run{}, e
	}
	defer tx.Rollback(ctx)
	run, e := lockRun(ctx, tx, s, id)
	if e != nil {
		return run, e
	}
	if run.Status != "paused" {
		return model.Run{}, model.ErrConflict
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM checkpoints WHERE run_id=$1 AND attempt=$2 AND valid) AND EXISTS(SELECT 1 FROM page_leases p JOIN sessions s ON s.id=p.session_id WHERE p.id=$3 AND p.user_id=$4 AND p.session_id=$5 AND p.closed_at IS NULL AND p.expires_at>now() AND s.revoked_at IS NULL AND s.expires_at>now())`, run.ID, run.Attempt, s.PageID, s.UserID, s.SessionID).Scan(&valid)
	if e != nil {
		return model.Run{}, e
	}
	if !valid {
		return model.Run{}, model.ErrConflict
	}
	run, e = scanRun(tx.QueryRow(ctx, "UPDATE agent_runs SET status='resuming',attempt=attempt+1,page_id=$2,version=version+1,updated_at=now() WHERE id=$1 RETURNING "+runColumns, id, s.PageID))
	if e != nil {
		return run, e
	}
	if e = appendEvent(ctx, tx, run, "run.resumed"); e != nil {
		return model.Run{}, e
	}
	return run, tx.Commit(ctx)
}

// ReconcileRun is called only while the process-wide advisory lock proves the previous process is gone.
func (r *Repository) ReconcileRun(ctx context.Context, s model.Scope, old model.Run, status, code string) (model.Run, error) {
	if status != "paused" && status != "interrupted" && status != "cancelled" {
		return model.Run{}, model.ErrConflict
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return model.Run{}, e
	}
	defer tx.Rollback(ctx)
	run, e := lockRun(ctx, tx, s, old.ID)
	if e != nil {
		return run, e
	}
	if run.Attempt != old.Attempt || run.Version != old.Version {
		return model.Run{}, model.ErrConflict
	}
	if status == "paused" {
		var valid bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM checkpoints WHERE run_id=$1 AND attempt=$2 AND valid)", run.ID, run.Attempt).Scan(&valid); e != nil {
			return model.Run{}, e
		}
		if !valid {
			return model.Run{}, model.ErrConflict
		}
	}
	run, e = scanRun(tx.QueryRow(ctx, "UPDATE agent_runs SET status=$2,error_code=$3,version=version+1,updated_at=now() WHERE id=$1 RETURNING "+runColumns, run.ID, status, code))
	if e != nil {
		return run, e
	}
	if status != "paused" {
		if _, e = tx.Exec(ctx, "UPDATE checkpoints SET valid=false WHERE run_id=$1", run.ID); e != nil {
			return model.Run{}, e
		}
	}
	if e = appendEvent(ctx, tx, run, "run."+status); e != nil {
		return model.Run{}, e
	}
	return run, tx.Commit(ctx)
}
