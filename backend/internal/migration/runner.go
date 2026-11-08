package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"agent-platform/backend/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func schemaPrincipals(ctx context.Context, tx pgx.Tx, component string) (Principals, error) {
	var owner, marker string
	var databaseOID uint32
	err := tx.QueryRow(ctx, `SELECT pg_get_userbyid(n.nspowner),COALESCE(shobj_description(n.nspowner,'pg_authid'),''),(SELECT oid FROM pg_database WHERE datname=current_database()) FROM pg_namespace n WHERE n.nspname=$1`, component).Scan(&owner, &marker, &databaseOID)
	if err != nil {
		return Principals{}, fmt.Errorf("bootstrap schema first: %w", err)
	}
	suffix := "_" + component + "_owner"
	prefix := strings.TrimSuffix(owner, suffix)
	if !strings.HasSuffix(owner, suffix) || !prefixPattern.MatchString(prefix) || marker != fmt.Sprintf("xdb-runtime-v1:%d:%s", databaseOID, owner) {
		return Principals{}, fmt.Errorf("unmanaged schema owner")
	}
	return Principals{Prefix: prefix}, nil
}

func Apply(ctx context.Context, pool *pgxpool.Pool, component string) error {
	if component != "control" && component != "platform" && component != "travel" {
		return fmt.Errorf("unknown runtime component")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(88153003)"); err != nil {
		return err
	}
	p, err := schemaPrincipals(ctx, tx, component)
	if err != nil {
		return err
	}
	var login string
	if err = tx.QueryRow(ctx, "SELECT session_user").Scan(&login); err != nil {
		return err
	}
	if login != p.Migrator(component) {
		return fmt.Errorf("dedicated component migration login required")
	}
	prior := ""
	if component == "platform" {
		prior = "control"
	}
	if component == "travel" {
		prior = "platform"
	}
	if prior != "" {
		version := "001"
		if component == "platform" {
			version = "002" // Atomic Session wrappers call control capabilities.
		}
		var ready bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+prior+".schema_migrations WHERE version=$1)", version).Scan(&ready); err != nil || !ready {
			return fmt.Errorf("apply %s/%s before %s", prior, version, component)
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+identifier(p.Owner(component))); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+component+".schema_migrations(version text PRIMARY KEY,content_hash text NOT NULL,applied_at timestamptz NOT NULL DEFAULT clock_timestamp())"); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations.Files, component)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	known := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		version, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("invalid migration filename")
		}
		known[version] = true
		data, err := migrations.Files.ReadFile(component + "/" + entry.Name())
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		var previous string
		err = tx.QueryRow(ctx, "SELECT content_hash FROM "+component+".schema_migrations WHERE version=$1", version).Scan(&previous)
		if err == nil {
			if previous != hash {
				return fmt.Errorf("applied %s migration %s checksum changed", component, version)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return err
		}
		if _, err = tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("apply %s/%s: %w", component, entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+component+".schema_migrations(version,content_hash) VALUES($1,$2)", version, hash); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, "SELECT version FROM "+component+".schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		if !known[version] {
			rows.Close()
			return fmt.Errorf("unknown applied runtime migration")
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	// Also secure schemas provisioned by older bootstrap versions. Never rely
	// only on default privileges for already-created SECURITY DEFINER helpers.
	if _, err = tx.Exec(ctx, "REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA "+identifier(component)+" FROM PUBLIC"); err != nil {
		return err
	}
	if component == "control" {
		for _, service := range []string{"platform", "travel", "maintenance"} {
			if _, err = tx.Exec(ctx, "INSERT INTO control.service_principals(login_role,service_id) VALUES($1,$2) ON CONFLICT DO NOTHING", p.Runtime(service), service); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "GRANT SELECT ON control.schema_migrations TO "+identifier(p.Migrator("platform"))+","+identifier(p.Migrator("travel"))); err != nil {
			return err
		}
		for _, service := range []string{"platform", "travel", "maintenance"} {
			if _, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA control TO "+identifier(p.Runtime(service))); err != nil {
				return err
			}
			for _, signature := range []string{"control.issue_scope(jsonb)", "control.assert_scope(text,text,text,text,text)", "control.reserve_attempt(jsonb)", "control.settle_attempt(jsonb)", "control.resource_snapshot(text,bigint)"} {
				if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+identifier(p.Runtime(service))); err != nil {
					return err
				}
			}
		}
		if _, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA control TO "+identifier(p.Owner("platform"))); err != nil {
			return err
		}
		for _, signature := range []string{"control.register_session_guard(text,text,timestamptz)", "control.revoke_session_guard(text)", "control.register_run_guard(text,text,text,text,timestamptz,jsonb)", "control.close_run_guard(text)", "control.set_context_guard(text,text,bigint,bigint,boolean)"} {
			if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+identifier(p.Runtime("platform"))+","+identifier(p.Owner("platform"))); err != nil {
				return err
			}
		}
		for _, signature := range []string{"control.admission_authority(text,text,text)", "control.assert_entry_control(text,text,text)"} {
			if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+identifier(p.Runtime("platform"))); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION control.close_invalid_run_guard(text) TO "+identifier(p.Runtime("platform"))+","+identifier(p.Runtime("maintenance"))); err != nil {
			return err
		}
	}
	if component == "platform" {
		for _, signature := range []string{"platform.create_session(text,text,text,text,timestamptz)", "platform.revoke_session(text)"} {
			if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+identifier(p.Runtime("platform"))); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "REVOKE ALL ON platform.schema_migrations FROM "+identifier(p.Runtime("platform"))+","+identifier(p.Runtime("maintenance"))+"; REVOKE INSERT,UPDATE ON platform.sessions FROM "+identifier(p.Runtime("platform"))+","+identifier(p.Runtime("maintenance"))+"; GRANT SELECT ON platform.schema_migrations TO "+identifier(p.Migrator("travel"))); err != nil {
			return err
		}
	}
	if component == "travel" {
		if _, err = tx.Exec(ctx, "REVOKE ALL ON travel.schema_migrations FROM "+identifier(p.Runtime("travel"))); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
