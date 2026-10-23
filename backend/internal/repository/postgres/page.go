package postgres

import (
	"agent-platform/backend/internal/model"
	"context"
	"time"
)

func (r *Repository) CreatePage(ctx context.Context, s model.Scope, p model.Page) error {
	tag, e := r.pool.Exec(ctx, `INSERT INTO page_leases(id,user_id,session_id,page_token_hash,expires_at) SELECT $1,$2,$3,$4,$5 WHERE EXISTS(SELECT 1 FROM sessions WHERE id=$3 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now())`, p.ID, s.UserID, s.SessionID, p.TokenHash, p.ExpiresAt)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return model.ErrUnauthenticated
	}
	return nil
}
func (r *Repository) VerifyPage(ctx context.Context, s model.Scope, id, hash string, now time.Time) error {
	var ok bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM page_leases p JOIN sessions s ON s.id=p.session_id WHERE p.id=$1 AND p.user_id=$2 AND p.session_id=$3 AND p.page_token_hash=$4 AND p.closed_at IS NULL AND p.expires_at>$5 AND s.revoked_at IS NULL AND s.expires_at>$5)`, id, s.UserID, s.SessionID, hash, now).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return model.ErrNotFound
	}
	return nil
}
func (r *Repository) TouchPage(ctx context.Context, s model.Scope, id, hash string, now, expires time.Time) error {
	tag, e := r.pool.Exec(ctx, `UPDATE page_leases p SET expires_at=$6,last_seen_at=$5 WHERE p.id=$1 AND p.user_id=$2 AND p.session_id=$3 AND p.page_token_hash=$4 AND p.closed_at IS NULL AND p.expires_at>$5 AND EXISTS(SELECT 1 FROM sessions s WHERE s.id=p.session_id AND s.revoked_at IS NULL AND s.expires_at>$5)`, id, s.UserID, s.SessionID, hash, now, expires)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return model.ErrNotFound
	}
	return nil
}
func (r *Repository) ClosePage(ctx context.Context, s model.Scope, id, hash string, now time.Time) error {
	tag, e := r.pool.Exec(ctx, `UPDATE page_leases SET closed_at=COALESCE(closed_at,$5) WHERE id=$1 AND user_id=$2 AND session_id=$3 AND page_token_hash=$4`, id, s.UserID, s.SessionID, hash, now)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return model.ErrNotFound
	}
	return nil
}
func (r *Repository) CloseSessionPages(ctx context.Context, s model.Scope, now time.Time) error {
	_, e := r.pool.Exec(ctx, "UPDATE page_leases SET closed_at=COALESCE(closed_at,$3) WHERE user_id=$1 AND session_id=$2", s.UserID, s.SessionID, now)
	return e
}
func (r *Repository) ListExpiredPageRuns(ctx context.Context, now time.Time) ([]model.ScopedRun, error) {
	return r.scopedRuns(ctx, `SELECT r.id,r.user_id,p.session_id,r.page_id FROM agent_runs r JOIN page_leases p ON p.id=r.page_id JOIN sessions s ON s.id=p.session_id WHERE r.status IN('accepted','running','resuming') AND (p.closed_at IS NOT NULL OR p.expires_at<=$1 OR s.revoked_at IS NOT NULL OR s.expires_at<=$1)`, now)
}
func (r *Repository) ListRecoverableRuns(ctx context.Context) ([]model.ScopedRun, error) {
	return r.scopedRuns(ctx, `SELECT r.id,r.user_id,p.session_id,r.page_id FROM agent_runs r JOIN page_leases p ON p.id=r.page_id WHERE r.status IN('accepted','running','resuming','pausing','paused','cancelling')`)
}
func (r *Repository) scopedRuns(ctx context.Context, query string, args ...any) ([]model.ScopedRun, error) {
	rows, e := r.pool.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	out := []model.ScopedRun{}
	for rows.Next() {
		var x model.ScopedRun
		if e = rows.Scan(&x.Run.ID, &x.Scope.UserID, &x.Scope.SessionID, &x.Scope.PageID); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, x)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for i := range out {
		out[i].Run, e = r.GetRun(ctx, out[i].Scope, out[i].Run.ID)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
