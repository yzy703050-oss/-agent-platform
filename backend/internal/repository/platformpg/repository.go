// Package platformpg owns only platform schema queries.
package platformpg

import (
	"agent-platform/backend/internal/platformrun"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool    *pgxpool.Pool
	runtime *platformrun.Config
}

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func NewRuntime(pool *pgxpool.Pool, cfg platformrun.Config) (*Repository, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return nil, err
	}
	return &Repository{pool: pool, runtime: &cfg}, nil
}
