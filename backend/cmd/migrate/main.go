// migrate is an explicit deployment entry point. It is never started by API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"time"

	"agent-platform/backend/internal/migration"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "reason": "runtime_migration_failed"})
		os.Exit(1)
	}
}

func run() error {
	bootstrap := flag.Bool("bootstrap", false, "prepare dedicated roles using administrator credentials")
	component := flag.String("component", "", "control, platform or travel")
	prefix := flag.String("role-prefix", "", "dedicated bootstrap role prefix")
	flag.Parse()
	var dsn string
	switch {
	case *bootstrap && *component == "":
		dsn = os.Getenv("RUNTIME_BOOTSTRAP_DSN")
	case !*bootstrap:
		switch *component {
		case "control":
			dsn = os.Getenv("RUNTIME_CONTROL_DSN")
		case "platform":
			dsn = os.Getenv("RUNTIME_PLATFORM_DSN")
		case "travel":
			dsn = os.Getenv("RUNTIME_TRAVEL_DSN")
		default:
			return errors.New("component required")
		}
	default:
		return errors.New("choose exactly one migration operation")
	}
	if dsn == "" {
		return errors.New("explicit dedicated migration DSN required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid migration DSN")
	}
	cfg.MaxConns = 2
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return errors.New("migration connection unavailable")
	}
	defer pool.Close()
	if *bootstrap {
		data, err := os.ReadFile(os.Getenv("RUNTIME_PROVISION_PASSWORDS_FILE"))
		if err != nil {
			return errors.New("private principal passwords unavailable")
		}
		var passwords map[string]string
		if json.Unmarshal(data, &passwords) != nil {
			return errors.New("invalid private principal passwords")
		}
		if _, err = migration.Provision(ctx, pool, *prefix, passwords); err != nil {
			return err
		}
	} else {
		if err = migration.Apply(ctx, pool, *component); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "scope": "runtime_database_migration_only", "component": *component, "bootstrap": *bootstrap})
}
