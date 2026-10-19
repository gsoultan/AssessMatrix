// Package queries holds the statements the typed store cannot express: calls
// into the schema's SECURITY DEFINER functions, the transaction's tenant
// setting, and set-based or guarded writes whose WHERE clause is the point.
//
// Each is a storm declaration, so `storm generate` PREPAREs it against the
// migrated schema, generates its scanner, and registers its exact text: a
// statement that is not declared here does not run. Everything else goes
// through the generated packages under internal/store.
package queries

import "github.com/gsoultan/storm"

// RoleRow is what the core checks about the role it connects as.
type RoleRow struct {
	Super      bool
	Bypass     bool
	OwnsTables int64
}

// CurrentRole reports whether the connected role could bypass row-level
// security: a superuser, a BYPASSRLS role, or the owner of any table.
var CurrentRole = storm.SQL[RoleRow](`
	SELECT r.rolsuper AS super, r.rolbypassrls AS bypass,
	       (SELECT count(*) FROM pg_tables t WHERE t.schemaname = 'public' AND t.tableowner = current_user) AS owns_tables
	FROM pg_roles r WHERE r.rolname = current_user`)

// TenantRow is the setting a scoped transaction now carries.
type TenantRow struct {
	Tenant string
}

// ScopeToTenant sets app.tenant_id for the rest of the transaction, which is
// what every row-level security policy compares tenant_id with.
var ScopeToTenant = storm.SQL[TenantRow](`SELECT set_config('app.tenant_id', $1, true) AS tenant`)
