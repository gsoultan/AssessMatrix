package queries

import (
	"time"

	"github.com/gsoultan/storm"
	"github.com/gsoultan/storm/runtime"
)

// Calls into the schema's functions. Most are SECURITY DEFINER lookups that
// find which tenant a credential belongs to before any tenant is chosen, so
// they run unscoped and return only what the caller needs to scope the next
// transaction.

// StaffTenant is a staff member found in some tenant.
type StaffTenant struct {
	TenantID [16]byte
	StaffID  [16]byte
}

// StaffByEmail is every tenant an address may sign in to.
var StaffByEmail = storm.SQL[StaffTenant](`SELECT tenant_id, staff_id FROM am_staff_by_email($1)`)

// CreateTenant makes a tenant and its first administrator, as the owner.
var CreateTenant = storm.SQL[StaffTenant](`SELECT tenant_id, staff_id FROM am_create_tenant($1, '', $2, '')`)

// SetupFirstTenant is first-run setup: it refuses once any tenant exists.
var SetupFirstTenant = storm.SQL[StaffTenant](`SELECT tenant_id, staff_id FROM am_setup_first_tenant($1, $2, $3, $4)`)

// StaffSessionRow is a live staff session.
type StaffSessionRow struct {
	TenantID   [16]byte
	StaffID    [16]byte
	Role       string
	SuperAdmin bool
}

// StaffSession resolves a session cookie's hash.
var StaffSession = storm.SQL[StaffSessionRow](`SELECT tenant_id, staff_id, role, super_admin FROM am_staff_session($1)`)

// Membership is one tenant a staff member works in.
type Membership struct {
	TenantID   [16]byte
	StaffID    [16]byte
	TenantName string
	Region     string
	Role       string
}

// StaffTenants is every tenant an address works in.
var StaffTenants = storm.SQL[Membership](`SELECT tenant_id, staff_id, tenant_name, region, role FROM am_staff_tenants($1)`)

// TenantSummary is a tenant as a super administrator lists it.
type TenantSummary struct {
	TenantID   [16]byte
	TenantName string
	Region     string
}

// AllTenants is every tenant, for a super administrator.
var AllTenants = storm.SQL[TenantSummary](`SELECT tenant_id, tenant_name, region FROM am_all_tenants()`)

// FlagRow is a yes-or-no answer.
type FlagRow struct {
	Yes bool
}

// SuperAdmin says whether an address runs the installation.
var SuperAdmin = storm.SQL[FlagRow](`SELECT am_super_admin($1) AS yes`)

// SetupNeeded says whether first-run setup is still open.
var SetupNeeded = storm.SQL[FlagRow](`SELECT am_setup_needed() AS yes`)

// VersionRow is the newest migration a database has applied.
type VersionRow struct {
	Version string
}

// SchemaVersion is the newest migration applied.
var SchemaVersion = storm.SQL[VersionRow](`SELECT am_schema_version() AS version`)

// SetupState is SetupNeeded and SchemaVersion in one round trip.
type SetupState struct {
	Needed  bool
	Version string
}

// SetupStatus reads both for the setup page.
var SetupStatus = storm.SQL[SetupState](`SELECT am_setup_needed() AS needed, am_schema_version() AS version`)

// TokenAssignment is the assignment a link's hash belongs to.
type TokenAssignment struct {
	TenantID     [16]byte
	AssignmentID [16]byte
	Current      bool
}

// AssignmentByToken resolves an invitation link, current or retired.
var AssignmentByToken = storm.SQL[TokenAssignment](`SELECT tenant_id, assignment_id, current FROM am_assignment_by_token($1)`)

// SessionAssignment is the assignment a candidate session belongs to.
type SessionAssignment struct {
	TenantID     [16]byte
	AssignmentID [16]byte
}

// AssignmentBySession resolves a candidate session cookie's hash.
var AssignmentBySession = storm.SQL[SessionAssignment](`SELECT tenant_id, assignment_id FROM am_assignment_by_session($1)`)

// TenantOf is the tenant a job's row belongs to.
type TenantOf struct {
	TenantID runtime.Null[[16]byte]
}

// OutboxTenant finds the tenant of an outbox row, for the mail job.
var OutboxTenant = storm.SQL[TenantOf](`SELECT am_outbox_tenant($1) AS tenant_id`)

// WebhookDeliveryTenant finds the tenant of a delivery, for the webhook job.
var WebhookDeliveryTenant = storm.SQL[TenantOf](`SELECT am_webhook_delivery_tenant($1) AS tenant_id`)

// PublicCredentialRow is what anyone may learn of a credential.
type PublicCredentialRow struct {
	Jwt        string
	RevokedAt  runtime.Null[time.Time]
	RevokedWhy runtime.Null[string]
}

// PublicCredential reads a credential for the public check, in any tenant.
var PublicCredential = storm.SQL[PublicCredentialRow](`SELECT jwt, revoked_at, revoked_why FROM am_public_credential($1)`)

// PublicIssuerRow is a tenant's name and public keys.
type PublicIssuerRow struct {
	Name string
	Keys runtime.JSON
}

// PublicIssuer reads an issuer's keys for the public check.
var PublicIssuer = storm.SQL[PublicIssuerRow](`SELECT name, keys FROM am_public_issuer($1)`)

// CountRow is a number of rows.
type CountRow struct {
	N int64
}

// PurgeAttempt deletes an attempt's event log, as the owner of the
// append-only trigger allows.
var PurgeAttempt = storm.SQL[CountRow](`SELECT am_purge_attempt($1) AS n`)

// OutboxSweep clears sent and stale mail bodies across every tenant.
var OutboxSweep = storm.SQL[CountRow](`SELECT am_outbox_sweep($1) AS n`)

// WebhookSweep deletes old deliveries across every tenant.
var WebhookSweep = storm.SQL[CountRow](`SELECT am_webhook_sweep($1) AS n`)

// DueAttempt is an attempt past its tenant's retention period.
type DueAttempt struct {
	TenantID  [16]byte
	AttemptID [16]byte
}

// RetentionDue lists attempts whose record is due for deletion.
var RetentionDue = storm.SQL[DueAttempt](`SELECT tenant_id, attempt_id FROM am_retention_due($1, $2)`)

// EmailRow is one address.
type EmailRow struct {
	Email string
}

// SuperAdmins is which of a list of addresses run the installation: one call
// for a whole staff list, where SuperAdmin would be one per address.
var SuperAdmins = storm.SQL[EmailRow](`SELECT e AS email FROM unnest($1::text[]) AS e WHERE am_super_admin(e)`)

// WebhookEarlierPending counts (0 or 1) pending deliveries to an endpoint of
// an earlier event about the same subject: one that must be sent first.
var WebhookEarlierPending = storm.SQL[CountRow](`
	SELECT count(*) AS n FROM (
	  SELECT 1 FROM webhook_deliveries d JOIN webhook_events e ON e.id = d.event_id
	  WHERE d.endpoint_id = $1 AND d.state = 1 AND e.subject_id = $2 AND e.seq < $3
	  LIMIT 1) earlier`)

// KindRow is one distinct event kind.
type KindRow struct {
	Kind string
}

// ConsentKinds is every consent kind in one attempt's log: what was agreed
// to and what was withdrawn. The literal pattern is what lets Postgres use
// attempt_events_consent_idx, whatever plan it caches.
var ConsentKinds = storm.SQL[KindRow](`SELECT DISTINCT kind FROM attempt_events WHERE attempt_id = $1 AND kind LIKE 'consent.%'`)

// Updates to the installation (migration 0013). The first four are called
// only for a super administrator; UpdateClaim and UpdateReport only by
// `core update`, as the owner.

// SittingsInProgress is how many sittings an update now would interrupt.
var SittingsInProgress = storm.SQL[CountRow](`SELECT am_sittings_in_progress() AS n`)

// UpdateRunRow is one update asked for.
type UpdateRunRow struct {
	ID            [16]byte
	FromVersion   string
	TargetVersion string
	RequestedBy   string
	RequestedAt   time.Time
	WaitForIdle   bool
	State         string
	Message       string
	StartedAt     runtime.Null[time.Time]
	FinishedAt    runtime.Null[time.Time]
}

// Updates is the newest updates, newest first.
var Updates = storm.SQL[UpdateRunRow](`SELECT id, from_version, target_version, requested_by, requested_at,
	wait_for_idle, state, message, started_at, finished_at FROM am_updates($1)`)

// SeenRow is when something was last seen, if ever.
type SeenRow struct {
	SeenAt runtime.Null[time.Time]
}

// UpdaterSeen is when the updater on the host last looked for work.
var UpdaterSeen = storm.SQL[SeenRow](`SELECT am_updater_seen() AS seen_at`)

// IDRow is a new row's id.
type IDRow struct {
	ID [16]byte
}

// RequestUpdate records an update a super administrator asked for.
var RequestUpdate = storm.SQL[IDRow](`SELECT am_request_update($1, $2, $3, $4, $5, $6) AS id`)

// CancelUpdate withdraws an update not yet started.
var CancelUpdate = storm.SQL[FlagRow](`SELECT am_cancel_update($1) AS yes`)

// ClaimedUpdate is the update the updater is to run now.
type ClaimedUpdate struct {
	ID                 [16]byte
	FromVersion        string
	TargetVersion      string
	Image              string
	PsychometricsImage string
}

// UpdateClaim records that the updater is alive and hands it an update to run, if any.
var UpdateClaim = storm.SQL[ClaimedUpdate](`SELECT id, from_version, target_version, image, psychometrics_image FROM am_update_claim($1)`)

// UpdateReport records the updater's progress or outcome.
var UpdateReport = storm.SQL[FlagRow](`SELECT am_update_report($1, $2, $3) AS yes`)
