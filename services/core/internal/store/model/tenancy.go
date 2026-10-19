package model

// Tenants, the staff who work in them, and how staff sign in.

import (
	"time"

	"github.com/gsoultan/storm"
)

// Tenant is an organisation. Every other row belongs to exactly one, and
// row-level security keeps each tenant's rows invisible to the others.
type Tenant struct {
	ID            [16]byte
	Name          string
	Region        string
	CreatedAt     time.Time
	RetentionDays int32
}

func (tn *Tenant) Schema(t *storm.Table) {
	t.PrimaryKey(&tn.ID)
	t.Col(&tn.ID).Default("gen_random_uuid()")
	t.Col(&tn.Region).Default("'id'::text")
	t.Col(&tn.CreatedAt).Default("now()")
	t.Col(&tn.RetentionDays).Default("365")
	t.CheckNamed("tenants_name_check", "(length(name) >= 1) AND (length(name) <= 200)")
	t.CheckNamed("tenants_region_check", "region ~ '^[a-z]{2}(-[a-z0-9]+)?$'::text")
	t.CheckNamed("tenants_retention_days_check", "(retention_days >= 30) AND (retention_days <= 365)")
}

// Staff is a person who works in the studio of one tenant: an administrator
// or a member. The same address may hold a Staff row in several tenants.
type Staff struct {
	ID         [16]byte
	Tenant     Tenant
	Email      string
	Name       string
	CreatedAt  time.Time
	Role       string
	DisabledAt *time.Time

	Sessions []StaffSession
}

func (s *Staff) Schema(t *storm.Table) {
	t.PrimaryKey(&s.ID)
	// What a child's (tenant_id, …_id) key points at (migration 0008_tenant_keys).
	t.UniqueNamed("staff_tenant_id_key", &s.Tenant, &s.ID)
	t.Name("staff")
	t.Col(&s.ID).Default("gen_random_uuid()")
	t.Col(&s.Tenant).ConstraintName("staff_tenant_id_fkey")
	t.Col(&s.Name).Default("''::text")
	t.Col(&s.CreatedAt).Default("now()")
	t.Col(&s.Role).Default("'member'::text")
	t.CheckNamed("staff_email_check", "(email ~ '^[^@\\s]+@[^@\\s]+$'::text) AND (length(email) <= 320)")
	t.CheckNamed("staff_name_check", "length(name) <= 200")
	t.CheckNamed("staff_role_check", "role = ANY (ARRAY['admin'::text, 'member'::text])")
	t.Index(storm.Lower(&s.Email)).Named("staff_email_idx")
	t.Index(&s.Tenant, storm.Lower(&s.Email)).Unique().Named("staff_email_key")
	t.Index(&s.Tenant).Named("staff_tenant_idx")
}

// StaffCode is the one-time sign-in code last mailed to a staff member,
// stored only as a hash.
type StaffCode struct {
	Staff     Staff
	Tenant    Tenant
	CodeHash  []byte
	ExpiresAt time.Time
	Tries     int32
	SentAt    time.Time
}

func (c *StaffCode) Schema(t *storm.Table) {
	t.PrimaryKey(&c.Staff)
	t.Col(&c.Staff).ConstraintName("staff_codes_staff_id_fkey")
	t.Col(&c.Staff).OnDelete(storm.Cascade)
	t.Col(&c.Tenant).ConstraintName("staff_codes_tenant_id_fkey")
	t.Col(&c.CodeHash).NotNull()
	t.Col(&c.Tries).Default("0")
	t.CheckNamed("staff_codes_tries_check", "tries >= 0")
	t.Index(&c.Tenant).Named("staff_codes_tenant_idx")
}

// StaffSession is a signed-in staff browser, stored only as a hash of its
// cookie.
type StaffSession struct {
	ID        [16]byte
	Tenant    Tenant
	Staff     Staff
	TokenHash []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func (s *StaffSession) Schema(t *storm.Table) {
	t.PrimaryKey(&s.ID)
	t.Col(&s.ID).Default("gen_random_uuid()")
	t.Col(&s.Tenant).ConstraintName("staff_sessions_tenant_id_fkey")
	t.Col(&s.Staff).ConstraintName("staff_sessions_staff_id_fkey")
	t.Col(&s.Staff).OnDelete(storm.Cascade)
	t.Col(&s.TokenHash).NotNull()
	t.Col(&s.CreatedAt).Default("now()")
	t.UniqueNamed("staff_sessions_token_hash_key", &s.TokenHash)
	t.Index(&s.Staff).Named("staff_sessions_staff_idx")
	t.Index(&s.Tenant).Named("staff_sessions_tenant_idx")
}

// SuperAdmin is an address that runs the whole installation. It belongs to no
// tenant; the application reads it only through SECURITY DEFINER functions.
type SuperAdmin struct {
	Email     string
	CreatedAt time.Time
}

func (a *SuperAdmin) Schema(t *storm.Table) {
	t.PrimaryKey(&a.Email)
	t.Col(&a.CreatedAt).Default("now()")
	t.CheckNamed("super_admins_email_check", "(email = lower(email)) AND (length(email) <= 320)")
}

// AuditLog is one staff write, appended in the same transaction as the
// write. Append-only: a trigger refuses UPDATE and DELETE.
type AuditLog struct {
	ID     int64
	Tenant Tenant
	At     time.Time
	Actor  string
	Action string
	Target string
	Detail storm.JSON
}

func (l *AuditLog) Schema(t *storm.Table) {
	t.PrimaryKey(&l.ID)
	t.Name("audit_log")
	t.Col(&l.ID).Identity()
	t.Col(&l.Tenant).ConstraintName("audit_log_tenant_id_fkey")
	t.Col(&l.At).Default("now()")
	t.Col(&l.Target).Default("''::text")
	t.Col(&l.Detail).Default("'{}'::jsonb")
	t.Index(&l.Tenant, storm.Desc(&l.At)).Named("audit_log_tenant_idx")
}

// Joins declares the reads that cross from a staff member to their tenant.
func (s *Staff) Joins(j *storm.Joins) {
	var tn Tenant
	// WithTenant is a staff member and the tenant they are signed in to.
	j.Named("WithTenant").
		Inner(&tn, &s.Tenant).
		Take(&s.ID, "ID").
		Take(&s.Email, "Email").
		Take(&s.Name, "Name").
		Take(&s.Role, "Role").
		Take(&tn.ID, "TenantID").
		Take(&tn.Name, "TenantName").
		Take(&tn.Region, "Region")
}
