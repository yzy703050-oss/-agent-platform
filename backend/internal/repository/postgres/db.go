package postgres

import (
	"agent-platform/backend/migrations"
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, e := pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(88153001)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version text PRIMARY KEY)"); e != nil {
		return e
	}
	for _, migration := range []struct{ version, file string }{{"001", "001_platform.up.sql"}, {"002", "002_message_delivery.up.sql"}} {
		tag, e := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING", migration.version)
		if e != nil {
			return e
		}
		if tag.RowsAffected() > 0 {
			data, e := migrations.Files.ReadFile(migration.file)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, string(data)); e != nil {
				return e
			}
		}
	}
	return tx.Commit(ctx)
}
