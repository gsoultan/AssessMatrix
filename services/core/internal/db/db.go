// Package db owns the Postgres connection, the tenant-scoped transaction every
// query runs in, and the schema migrations.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// TenantID scopes a transaction. It comes from an authenticated principal or a
// SECURITY DEFINER lookup, never from a request field.
type TenantID string

// DB is the application's handle: a pool connected as the unprivileged role.
type DB struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() { d.Pool.Close() }

// ErrPrivilegedRole means the core is connected as a role that RLS would not
// stop. Starting anyway would make tenant isolation a matter of luck.
var ErrPrivilegedRole = errors.New("database role can bypass row-level security")

// CheckRole refuses a superuser, a BYPASSRLS role, or the owner of any table.
func (d *DB) CheckRole(ctx context.Context) error {
	var super, bypass bool
	var owns int
	err := d.Pool.QueryRow(ctx, `
		SELECT r.rolsuper, r.rolbypassrls,
		       (SELECT count(*) FROM pg_tables t WHERE t.schemaname = 'public' AND t.tableowner = current_user)
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&super, &bypass, &owns)
	if err != nil {
		return fmt.Errorf("inspect database role: %w", err)
	}
	if super || bypass || owns > 0 {
		return fmt.Errorf("%w (superuser=%t bypassrls=%t owns_tables=%d)", ErrPrivilegedRole, super, bypass, owns)
	}
	return nil
}

// InTenant runs fn in a transaction scoped to one tenant. The setting is
// transaction-local, so it cannot leak to the next user of the connection.
func (d *DB) InTenant(ctx context.Context, tenant TenantID, fn func(pgx.Tx) error) error {
	if tenant == "" {
		return errors.New("db: InTenant called without a tenant")
	}
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, string(tenant)); err != nil {
			return fmt.Errorf("scope transaction to tenant: %w", err)
		}
		return fn(tx)
	})
}

// Unscoped runs fn with no tenant. Under RLS it sees no tenant rows at all;
// it exists for the SECURITY DEFINER lookups and River.
func (d *DB) Unscoped(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.Pool, fn)
}

// Migrate applies pending migrations as the schema owner, then grants the
// application role exactly what it needs. Forward-only: there are no downs.
func Migrate(ctx context.Context, ownerURL, appRole string) ([]string, error) {
	if !validIdent(appRole) {
		return nil, fmt.Errorf("invalid application role name %q", appRole)
	}
	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return nil, fmt.Errorf("connect as owner: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	// One migrator at a time, across every replica that starts at once.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7240581)`); err != nil {
		return nil, fmt.Errorf("take migration lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(7240581)`)

	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, path := range names {
		name := strings.TrimPrefix(path, "migrations/")
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil {
			return applied, err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile(path)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", name, err)
		}
		applied = append(applied, name)
	}

	// River's own tables. It versions them itself, one transaction per
	// version (some of its migrations cannot share one).
	ownerPool, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		return applied, err
	}
	defer ownerPool.Close()
	migrator, err := rivermigrate.New(riverpgxv5.New(ownerPool), nil)
	if err != nil {
		return applied, err
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return applied, fmt.Errorf("river migrations: %w", err)
	}

	role := pgx.Identifier{appRole}.Sanitize()
	grants := []string{
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ` + role,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ` + role,
		`GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO ` + role,
		// Evidence and the audit trail are append-only for the application,
		// on top of the triggers that refuse it for everyone.
		`REVOKE UPDATE, DELETE, TRUNCATE ON attempt_events, audit_log FROM ` + role,
		`REVOKE ALL ON schema_migrations FROM ` + role,
		// Tenants are made by the owner (`core tenant`), or once by setup.
		`REVOKE EXECUTE ON FUNCTION am_create_tenant(text, text, text, text) FROM ` + role,
	}
	for _, g := range grants {
		if _, err := conn.Exec(ctx, g); err != nil {
			return applied, fmt.Errorf("grant to %s: %w", appRole, err)
		}
	}
	return applied, nil
}

// LatestMigration is the newest migration this build ships. A database whose
// am_schema_version() is older has not been migrated for this build.
func LatestMigration() string {
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[len(names)-1], "migrations/")
}

func validIdent(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
