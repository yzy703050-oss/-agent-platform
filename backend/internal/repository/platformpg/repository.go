// Package platformpg owns only platform schema queries.
package platformpg

import (
	"agent-platform/backend/internal/platformrun"
	"agent-platform/backend/internal/repository/runtimepg"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool    *pgxpool.Pool
	runtime *platformrun.Config
	*scopedStore
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, scopedStore: runtimepg.NewPlatform(pool)}
}

func NewRuntime(pool *pgxpool.Pool, cfg platformrun.Config) (*Repository, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return nil, err
	}
	r := New(pool)
	r.runtime = &cfg
	return r, nil
}
