// Package model is the storm model of the core's tables, imported from the
// migrated schema with `storm import`.
//
// The SQL files in internal/db/migrations remain the schema of record: they
// also hold what a Go declaration cannot say (row-level security policies,
// grants, the SECURITY DEFINER functions and the append-only triggers), so
// this model is a projection of them, and the code generated from it is
// regenerated against a migrated database (`go generate ./internal/store`).
// River's tables are River's and are not modelled.
//
// After changing a migration, re-import the table it touches and regenerate.
package model

// The model is written the way storm reads it: a foreign key is a field of
// the referenced type (Tenant Tenant becomes tenant_id), a has-many is a
// slice of the child, and a one-to-one is a foreign key declared unique
// (an Attempt's Assignment, a Credential's Attempt). Explicit constraint
// names pin the names the migrations gave them, so a diff against a migrated
// database is empty.

// All is every model, for the generator.
func All() []any {
	return []any{
		&Tenant{},
		&Staff{},
		&StaffCode{},
		&StaffSession{},
		&SuperAdmin{},
		&AuditLog{},
		&Exam{},
		&ExamVersion{},
		&Media{},
		&Person{},
		&Assignment{},
		&InviteEvent{},
		&RetiredToken{},
		&Attempt{},
		&AttemptEvent{},
		&AttemptBatch{},
		&ItemScore{},
		&ItemStat{},
		&AnalysisRequest{},
		&Credential{},
		&IssuerKey{},
		&Outbox{},
		&WebhookEndpoint{},
		&WebhookEvent{},
		&WebhookDelivery{},
	}
}
