package bootstrap

import (
	"context"
	"errors"
	"net/http"

	"agent-platform/backend/internal/config"
	"agent-platform/backend/internal/repository/postgres"
	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	Handler http.Handler
	close   func()
}

func (a *App) Close() { a.close() }
func New(ctx context.Context, cfg config.Config) (*App, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseDSN)
	if err != nil {
		return nil, errors.New("invalid DB_DSN")
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database unavailable")
	}
	lock, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, errors.New("database lock connection unavailable")
	}
	var held bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(88153002)").Scan(&held); err != nil || !held {
		lock.Release()
		pool.Close()
		return nil, errors.New("another platform instance is active")
	}
	closeApp := func() {
		lock.Exec(context.Background(), "SELECT pg_advisory_unlock(88153002)")
		lock.Release()
		pool.Close()
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		closeApp()
		return nil, errors.New("database migration failed")
	}
	repo := postgres.New(pool)
	h := router.New(router.Dependencies{Auth: authsvc.New(repo), Users: usersvc.New(repo), Conversations: convsvc.New(repo), Settings: settingssvc.New(repo), SecureCookies: cfg.CookieSecure, AllowedOrigin: cfg.PublicOrigin, Ready: pool.Ping})
	return &App{Handler: h, close: closeApp}, nil
}
