// Package travelpg owns only travel schema queries; external references are not FKs.
package travelpg

import "github.com/jackc/pgx/v5/pgxpool"

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }
