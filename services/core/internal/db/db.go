// Package db owns the Postgres connection, the tenant-scoped transaction every
// query runs in, and the schema migrations.
//
// Queries go through storm: the typed packages under internal/store, and the
// declared statements in internal/store/queries for what those cannot say.
// Only the migration runner below speaks SQL directly, because a migration is
// SQL.
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

	"github.com/gsoultan/storm"
	"github.com/gsoultan/storm/runtime"
	"github.com/gsoultan/storm/runtime/pgxdrv"

	// The generated scanners and the registry of declared statements.
	_ "github.com/gsoultan/assessmatrix/services/core/internal/store"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
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

// Tx is one transaction. Every storm query and write takes it as its
// Executor; River, which enqueues jobs in the same transaction, takes Pgx.
type Tx struct {
	ex     pgxdrv.Tx
	tenant storm.UUID
}

func (t *Tx) Query(ctx context.Context, sql string, args []any) (runtime.Rows, error) {
	return t.ex.Query(ctx, sql, args)
}

func (t *Tx) Exec(ctx context.Context, sql string, args []any) (int64, error) {
	return t.ex.Exec(ctx, sql, args)
}

func (t *Tx) CopyFrom(ctx context.Context, table string, cols []string, src runtime.CopySource) (int64, error) {
	return t.ex.CopyFrom(ctx, table, cols, src)
}

func (t *Tx) Batch(ctx context.Context, ops []runtime.BatchOp, each func(int, runtime.Rows, int64, error) error) error {
	return t.ex.Batch(ctx, ops, each)
}

// Pgx is the transaction underneath, for River's InsertTx.
func (t *Tx) Pgx() pgx.Tx { return t.ex.T }

// Tenant is the tenant the transaction is scoped to: the value every row it
// inserts carries, and the only one row-level security lets it write. Zero in
// an unscoped transaction.
func (t *Tx) Tenant() storm.UUID { return t.tenant }

// Conn adapts a single connection, such as the owner's in `core tenant`, to
// the same Executor.
func Conn(c *pgx.Conn) runtime.Executor { return pgxdrv.Conn{C: c} }

// OnConn runs fn in a transaction on a single connection, such as the
// owner's in `core migrate`. It is unscoped: the owner is not held to RLS.
func OnConn(ctx context.Context, c *pgx.Conn, fn func(*Tx) error) error {
	return pgx.BeginFunc(ctx, c, func(ptx pgx.Tx) error {
		return fn(&Tx{ex: pgxdrv.Tx{T: ptx}})
	})
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

// Executor runs a statement outside any transaction, for reads that need
// none (the schema version, whether setup is open).
func (d *DB) Executor() runtime.Executor { return pgxdrv.Pool{P: d.Pool} }

// ErrPrivilegedRole means the core is connected as a role that RLS would not
// stop. Starting anyway would make tenant isolation a matter of luck.
var ErrPrivilegedRole = errors.New("database role can bypass row-level security")

// CheckRole refuses a superuser, a BYPASSRLS role, or the owner of any table.
func (d *DB) CheckRole(ctx context.Context) error {
	r, _, err := queries.CurrentRole.One(ctx, d.Executor())
	if err != nil {
		return fmt.Errorf("inspect database role: %w", err)
	}
	if r.Super || r.Bypass || r.OwnsTables > 0 {
		return fmt.Errorf("%w (superuser=%t bypassrls=%t owns_tables=%d)", ErrPrivilegedRole, r.Super, r.Bypass, r.OwnsTables)
	}
	return nil
}

// InTenant runs fn in a transaction scoped to one tenant. The setting is
// transaction-local, so it cannot leak to the next user of the connection.
func (d *DB) InTenant(ctx context.Context, tenant TenantID, fn func(*Tx) error) error {
	if tenant == "" {
		return errors.New("db: InTenant called without a tenant")
	}
	id, err := storm.ParseUUID(string(tenant))
	if err != nil {
		return fmt.Errorf("db: InTenant called with tenant %q: %w", tenant, err)
	}
	return pgx.BeginFunc(ctx, d.Pool, func(ptx pgx.Tx) error {
		tx := &Tx{ex: pgxdrv.Tx{T: ptx}}
		if err := tx.scope(ctx, id); err != nil {
			return err
		}
		return fn(tx)
	})
}

// Scope moves an unscoped transaction into a tenant, for the one case that
// learns its tenant mid-transaction: first-run setup, which creates it.
func (t *Tx) Scope(ctx context.Context, tenant [16]byte) error {
	if t.tenant != (storm.UUID{}) {
		return errors.New("db: transaction is already scoped to a tenant")
	}
	return t.scope(ctx, tenant)
}

func (t *Tx) scope(ctx context.Context, tenant [16]byte) error {
	if _, _, err := queries.ScopeToTenant.One(ctx, t, storm.UUID(tenant).String()); err != nil {
		return fmt.Errorf("scope transaction to tenant: %w", err)
	}
	t.tenant = tenant
	return nil
}

// Unscoped runs fn with no tenant. Under RLS it sees no tenant rows at all;
// it exists for the SECURITY DEFINER lookups and River.
func (d *DB) Unscoped(ctx context.Context, fn func(*Tx) error) error {
	return pgx.BeginFunc(ctx, d.Pool, func(ptx pgx.Tx) error {
		return fn(&Tx{ex: pgxdrv.Tx{T: ptx}})
	})
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
		// Who runs the installation is read and changed only through its functions.
		`REVOKE ALL ON super_admins FROM ` + role,
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
