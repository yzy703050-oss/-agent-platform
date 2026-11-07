package runtimeintegration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"agent-platform/backend/internal/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type runtimeFixture struct {
	Admin, Platform, Travel, Maintenance *pgxpool.Pool
	Migrations                           map[string]*pgxpool.Pool
	Principals                           migration.Principals
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	if os.Getenv("RUNTIME_TEST_ALLOW_DB_CREATE") != "1" || os.Getenv("RUNTIME_TEST_ADMIN_DSN") == "" {
		t.Fatal("isolated PG with explicit RUNTIME_TEST_ALLOW_DB_CREATE=1 and RUNTIME_TEST_ADMIN_DSN required; no skip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(os.Getenv("RUNTIME_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatal("invalid test admin DSN")
	}
	cfg.MaxConns = 2
	cluster, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test admin connection unavailable")
	}
	token := make([]byte, 6)
	if _, err = rand.Read(token); err != nil {
		t.Fatal(err)
	}
	name := "xdb_v1_" + hex.EncodeToString(token)
	if _, err = cluster.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		cluster.Close()
		t.Fatalf("create isolated database: %v", err)
	}
	var databaseOID uint32
	if err = cluster.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&databaseOID); err != nil {
		t.Fatal(err)
	}
	isolated := cfg.Copy()
	isolated.ConnConfig.Database = name
	admin, err := pgxpool.NewWithConfig(ctx, isolated)
	if err != nil {
		t.Fatal(err)
	}
	pools := []*pgxpool.Pool{admin}
	roleOIDs := map[string]uint32{}
	t.Cleanup(func() {
		for _, pool := range pools {
			pool.Close()
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		defer cluster.Close()
		var got uint32
		if !strings.HasPrefix(name, "xdb_v1_") || cluster.QueryRow(cleanupCtx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&got) != nil || got != databaseOID {
			t.Error("refused database cleanup: identity mismatch")
			return
		}
		if _, err := cluster.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("isolated database cleanup: %v", err)
			return
		}
		for role, oid := range roleOIDs {
			if !strings.HasPrefix(role, name+"_") || cluster.QueryRow(cleanupCtx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&got) != nil || got != oid {
				t.Error("refused role cleanup: identity mismatch")
				continue
			}
			if _, err := cluster.Exec(cleanupCtx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("isolated role cleanup: %v", err)
			}
		}
	})
	passwords := map[string]string{}
	// Provision consumes passwords by role name, never credentials from a real app.
	for _, suffix := range []string{"control_migrate", "platform_migrate", "travel_migrate", "platform_runtime", "travel_runtime", "maintenance_runtime"} {
		value := make([]byte, 24)
		if _, err = rand.Read(value); err != nil {
			t.Fatal(err)
		}
		passwords[name+"_"+suffix] = hex.EncodeToString(value)
	}
	principals, err := migration.Provision(ctx, admin, name, passwords)
	if err != nil {
		t.Fatalf("provision isolated roles: %v", err)
	}
	rows, err := admin.Query(ctx, "SELECT rolname,oid FROM pg_roles WHERE starts_with(rolname,$1)", name+"_")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var role string
		var oid uint32
		if err = rows.Scan(&role, &oid); err != nil {
			t.Fatal(err)
		}
		roleOIDs[role] = oid
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	connect := func(role string) *pgxpool.Pool {
		t.Helper()
		config := isolated.Copy()
		config.ConnConfig.User = role
		config.ConnConfig.Password = passwords[role]
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal("isolated role connection unavailable")
		}
		pools = append(pools, pool)
		if err = pool.Ping(ctx); err != nil {
			t.Fatalf("isolated role authentication: %v", err)
		}
		return pool
	}
	f := &runtimeFixture{Admin: admin, Principals: principals, Migrations: map[string]*pgxpool.Pool{}}
	for _, component := range []string{"control", "platform", "travel"} {
		f.Migrations[component] = connect(principals.Migrator(component))
	}
	f.Platform = connect(principals.Runtime("platform"))
	f.Travel = connect(principals.Runtime("travel"))
	f.Maintenance = connect(principals.Runtime("maintenance"))
	return f
}

func (f *runtimeFixture) apply(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, component := range []string{"control", "platform", "travel"} {
		if err := migration.Apply(ctx, f.Migrations[component], component); err != nil {
			t.Fatalf("apply %s: %v", component, err)
		}
	}
}
