// Package migration prepares and migrates runtime-v1 storage explicitly.
// Runtime processes never call these administrator/DDL entry points.
package migration

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Principals struct{ Prefix string }

func (p Principals) Owner(component string) string    { return p.Prefix + "_" + component + "_owner" }
func (p Principals) Migrator(component string) string { return p.Prefix + "_" + component + "_migrate" }
func (p Principals) Runtime(service string) string    { return p.Prefix + "_" + service + "_runtime" }

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
var passwordPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{32,}$`)

func identifier(value string) string { return pgx.Identifier{value}.Sanitize() }

// Provision only owns roles marked for this exact database OID. Replays never
// silently adopt existing principals or rotate their credentials.
func Provision(ctx context.Context, pool *pgxpool.Pool, prefix string, passwords map[string]string) (Principals, error) {
	p := Principals{Prefix: prefix}
	if !prefixPattern.MatchString(prefix) {
		return Principals{}, fmt.Errorf("invalid runtime role prefix")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Principals{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(88153002)"); err != nil {
		return Principals{}, err
	}
	var database, login string
	var oid uint32
	var administrator bool
	if err = tx.QueryRow(ctx, "SELECT current_database(),session_user,oid,(SELECT rolsuper FROM pg_roles WHERE rolname=session_user) FROM pg_database WHERE datname=current_database()").Scan(&database, &login, &oid, &administrator); err != nil {
		return Principals{}, err
	}
	if !administrator {
		return Principals{}, fmt.Errorf("dedicated bootstrap administrator required")
	}
	owners := []string{p.Owner("control"), p.Owner("platform"), p.Owner("travel")}
	logins := []string{p.Migrator("control"), p.Migrator("platform"), p.Migrator("travel"), p.Runtime("platform"), p.Runtime("travel"), p.Runtime("maintenance")}
	for _, role := range append(append([]string{}, owners...), logins...) {
		isLogin := !strings.HasSuffix(role, "_owner")
		marker := fmt.Sprintf("xdb-runtime-v1:%d:%s", oid, role)
		var existingMarker string
		var exists, canLogin, super, createDB, createRole, bypass, inherit bool
		err = tx.QueryRow(ctx, `SELECT true,COALESCE(shobj_description(oid,'pg_authid'),''),rolcanlogin,rolsuper,rolcreatedb,rolcreaterole,rolbypassrls,rolinherit FROM pg_roles WHERE rolname=$1`, role).Scan(&exists, &existingMarker, &canLogin, &super, &createDB, &createRole, &bypass, &inherit)
		if err != nil && err != pgx.ErrNoRows {
			return Principals{}, err
		}
		if exists {
			if existingMarker != marker || canLogin != isLogin || super || createDB || createRole || bypass || inherit {
				return Principals{}, fmt.Errorf("refusing unmanaged or privileged runtime principal")
			}
			continue
		}
		options := " NOLOGIN"
		if isLogin {
			password := passwords[role]
			if !passwordPattern.MatchString(password) {
				return Principals{}, fmt.Errorf("missing or invalid bootstrap password for runtime principal")
			}
			options = " LOGIN PASSWORD '" + password + "'"
		}
		if _, err = tx.Exec(ctx, "CREATE ROLE "+identifier(role)+options+" NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS"); err != nil {
			return Principals{}, fmt.Errorf("create runtime principal: %w", err)
		}
		if _, err = tx.Exec(ctx, "COMMENT ON ROLE "+identifier(role)+" IS '"+marker+"'"); err != nil {
			return Principals{}, err
		}
	}
	if _, err = tx.Exec(ctx, "REVOKE ALL ON DATABASE "+identifier(database)+" FROM PUBLIC; REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
		return Principals{}, err
	}
	for _, role := range logins {
		if _, err = tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+identifier(database)+" TO "+identifier(role)); err != nil {
			return Principals{}, err
		}
	}
	for _, component := range []string{"control", "platform", "travel"} {
		owner, migrator := p.Owner(component), p.Migrator(component)
		if _, err = tx.Exec(ctx, "GRANT "+identifier(owner)+" TO "+identifier(migrator)+"; CREATE SCHEMA IF NOT EXISTS "+identifier(component)+" AUTHORIZATION "+identifier(owner)); err != nil {
			return Principals{}, err
		}
		var actualOwner string
		if err = tx.QueryRow(ctx, "SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname=$1", component).Scan(&actualOwner); err != nil || actualOwner != owner {
			return Principals{}, fmt.Errorf("refusing foreign schema owner")
		}
		// Function EXECUTE starts as a global PUBLIC default in PostgreSQL.
		// A per-schema REVOKE cannot remove that global grant.
		if _, err = tx.Exec(ctx, "REVOKE ALL ON SCHEMA "+identifier(component)+" FROM PUBLIC; ALTER ROLE "+identifier(migrator)+" IN DATABASE "+identifier(database)+" SET search_path=pg_catalog,"+identifier(component)+"; ALTER DEFAULT PRIVILEGES FOR ROLE "+identifier(owner)+" REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC"); err != nil {
			return Principals{}, err
		}
		if component != "control" {
			services := []string{component}
			if component == "platform" {
				services = append(services, "maintenance")
			}
			for _, service := range services {
				role := identifier(p.Runtime(service))
				if _, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA "+identifier(component)+" TO "+role+"; ALTER DEFAULT PRIVILEGES FOR ROLE "+identifier(owner)+" IN SCHEMA "+identifier(component)+" GRANT SELECT,INSERT,UPDATE ON TABLES TO "+role+"; ALTER DEFAULT PRIVILEGES FOR ROLE "+identifier(owner)+" IN SCHEMA "+identifier(component)+" GRANT USAGE,SELECT ON SEQUENCES TO "+role); err != nil {
					return Principals{}, err
				}
			}
		}
	}
	for _, service := range []string{"platform", "travel", "maintenance"} {
		schema := service
		if service == "maintenance" {
			schema = "platform"
		}
		if _, err = tx.Exec(ctx, "ALTER ROLE "+identifier(p.Runtime(service))+" IN DATABASE "+identifier(database)+" SET search_path=pg_catalog,"+identifier(schema)); err != nil {
			return Principals{}, err
		}
	}
	// Only migration metadata, never cross-service business rows.
	if _, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA control TO "+identifier(p.Migrator("platform"))+","+identifier(p.Migrator("travel"))+"; GRANT USAGE ON SCHEMA platform TO "+identifier(p.Migrator("travel"))); err != nil {
		return Principals{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Principals{}, err
	}
	return p, nil
}
