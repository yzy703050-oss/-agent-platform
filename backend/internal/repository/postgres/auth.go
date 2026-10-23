package postgres

import (
	"agent-platform/backend/internal/model"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *Repository) CreateUser(ctx context.Context, user model.User, hash string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO users(id,username,password_hash) VALUES($1,$2,$3)", user.ID, user.Username, hash); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return model.ErrConflict
		}
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO user_settings(user_id) VALUES($1)", user.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindUser(ctx context.Context, scope model.Scope) (model.User, error) {
	var user model.User
	err := r.pool.QueryRow(ctx, "SELECT id,username FROM users WHERE id=$1", scope.UserID).Scan(&user.ID, &user.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return user, model.ErrNotFound
	}
	return user, err
}
func (r *Repository) FindAccount(ctx context.Context, name string) (model.Account, error) {
	var a model.Account
	err := r.pool.QueryRow(ctx, "SELECT id,username,password_hash FROM users WHERE username=$1", name).Scan(&a.User.ID, &a.User.Username, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, model.ErrNotFound
	}
	return a, err
}
func (r *Repository) CreateSession(ctx context.Context, s model.Session) error {
	_, err := r.pool.Exec(ctx, "INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,$4,$5)", s.ID, s.UserID, s.TokenHash, s.CSRFHash, s.ExpiresAt)
	return err
}
func (r *Repository) FindSession(ctx context.Context, hash string) (model.Session, error) {
	var s model.Session
	err := r.pool.QueryRow(ctx, "SELECT id,user_id,token_hash,csrf_hash,expires_at FROM sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now()", hash).Scan(&s.ID, &s.UserID, &s.TokenHash, &s.CSRFHash, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, model.ErrNotFound
	}
	return s, err
}
func (r *Repository) RevokeSession(ctx context.Context, hash string) error {
	_, err := r.pool.Exec(ctx, "UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE token_hash=$1", hash)
	return err
}
