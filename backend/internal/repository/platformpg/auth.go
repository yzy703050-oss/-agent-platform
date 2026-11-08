package platformpg

import (
	"context"
	"errors"

	"agent-platform/backend/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *Repository) CreateUser(ctx context.Context, user model.User, hash string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO platform.users(id,username,password_hash) VALUES($1,$2,$3)", user.ID, user.Username, hash); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return model.ErrConflict
		}
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO platform.user_settings(user_id) VALUES($1)", user.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindAccount(ctx context.Context, name string) (model.Account, error) {
	var a model.Account
	err := r.pool.QueryRow(ctx, "SELECT id,username,password_hash FROM platform.users WHERE username=$1", name).Scan(&a.User.ID, &a.User.Username, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		err = model.ErrNotFound
	}
	return a, err
}

func (r *Repository) CreateSession(ctx context.Context, s model.Session) error {
	// The controlled function inserts both the guard and the Session; any
	// constraint or connection failure rolls the entire statement back.
	_, err := r.pool.Exec(ctx, "SELECT platform.create_session($1,$2,$3,$4,$5)", s.ID, s.UserID, s.TokenHash, s.CSRFHash, s.ExpiresAt)
	return err
}

func (r *Repository) FindSession(ctx context.Context, hash string) (model.Session, error) {
	var s model.Session
	err := r.pool.QueryRow(ctx, "SELECT id,user_id,token_hash,csrf_hash,expires_at FROM platform.sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp()", hash).Scan(&s.ID, &s.UserID, &s.TokenHash, &s.CSRFHash, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = model.ErrNotFound
	}
	return s, err
}

func (r *Repository) RevokeSession(ctx context.Context, hash string) error {
	_, err := r.pool.Exec(ctx, "SELECT platform.revoke_session($1)", hash)
	return err
}
