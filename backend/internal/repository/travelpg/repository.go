// Package travelpg owns only travel schema queries; external references are not FKs.
package travelpg

import (
	"agent-platform/backend/internal/repository/runtimepg"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
	*scopedStore
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, scopedStore: runtimepg.NewTravel(pool)}
}
