package model

// The roster: the people a tenant invites, and their invitations.

import (
	"time"

	"github.com/gsoultan/storm"
)

// Person is someone on a tenant's roster: a candidate who may be invited to
// sit an instrument. Not a staff user.
type Person struct {
	ID        [16]byte
	Tenant    Tenant
	Name      string
	Email     string
	GroupName string
	CreatedAt time.Time

	Assignments []Assignment
}

func (p *Person) Schema(t *storm.Table) {
	t.PrimaryKey(&p.ID)
	// What a child's (tenant_id, …_id) key points at (migration 0008_tenant_keys).
	t.UniqueNamed("people_tenant_id_key", &p.Tenant, &p.ID)
	t.Name("people")
	t.Col(&p.ID).Default("gen_random_uuid()")
	t.Col(&p.Tenant).ConstraintName("people_tenant_id_fkey")
	t.Col(&p.GroupName).Default("''::text")
	t.Col(&p.CreatedAt).Default("now()")
	t.CheckNamed("people_email_check", "(email ~ '^[^@\\s]+@[^@\\s]+$'::text) AND (length(email) <= 320)")
	t.CheckNamed("people_group_name_check", "length(group_name) <= 200")
	t.CheckNamed("people_name_check", "length(name) <= 200")
	t.Index(&p.Tenant, storm.Lower(&p.Email)).Unique().Named("people_email_key")
	t.Index(&p.Tenant, storm.Desc(&p.CreatedAt)).Named("people_tenant_created_idx")
}

// Assignment is one sitting of an instrument by a person: the invitation,
// its link and sign-in code (as hashes), and where the sitting stands.
type Assignment struct {
	ID           [16]byte
	Tenant       Tenant
	Exam         Exam
	ExamVersion  int32
	Person       Person
	AssignedAt   time.Time
	DueAt        *time.Time
	Status       int16
	StartedAt    *time.Time
	SubmittedAt  *time.Time
	ScorePct     *int16
	Passed       *bool
	TokenHash    []byte
	SentAt       *time.Time
	ExpiresAt    *time.Time
	RedeemedAt   *time.Time
	RevokedAt    *time.Time
	SessionHash  []byte
	OtpHash      []byte
	OtpExpiresAt *time.Time
	OtpTries     int32
	OtpSentAt    *time.Time
	OtpSends     int32
	SelfServes   int32
	Sitting      int32

	History       []InviteEvent
	RetiredTokens []RetiredToken
}

func (a *Assignment) Schema(t *storm.Table) {
	t.PrimaryKey(&a.ID)
	// What a child's (tenant_id, …_id) key points at (migration 0008_tenant_keys).
	t.UniqueNamed("assignments_tenant_id_key", &a.Tenant, &a.ID)
	t.Col(&a.ID).Default("gen_random_uuid()")
	t.Col(&a.Tenant).ConstraintName("assignments_tenant_id_fkey")
	t.Col(&a.Exam).ConstraintName("assignments_exam_id_fkey")
	t.Col(&a.Exam).OnDelete(storm.Cascade)
	t.Col(&a.Person).ConstraintName("assignments_person_id_fkey")
	t.Col(&a.Person).OnDelete(storm.Cascade)
	t.Col(&a.AssignedAt).Default("now()")
	t.Col(&a.Status).Default("1")
	t.Col(&a.OtpTries).Default("0")
	t.Col(&a.OtpSends).Default("0")
	t.Col(&a.SelfServes).Default("0")
	t.Col(&a.Sitting).Default("1")
	t.UniqueNamed("assignments_session_hash_key", &a.SessionHash)
	t.UniqueNamed("assignments_sitting_key", &a.Exam, &a.Person, &a.Sitting)
	t.UniqueNamed("assignments_token_hash_key", &a.TokenHash)
	t.CheckNamed("assignments_exam_version_check", "exam_version >= 1")
	t.CheckNamed("assignments_otp_sends_check", "otp_sends >= 0")
	t.CheckNamed("assignments_otp_tries_check", "otp_tries >= 0")
	t.CheckNamed("assignments_score_pct_check", "(score_pct >= 0) AND (score_pct <= 100)")
	t.CheckNamed("assignments_self_serves_check", "self_serves >= 0")
	t.CheckNamed("assignments_sitting_check", "sitting >= 1")
	t.CheckNamed("assignments_status_check", "(status >= 1) AND (status <= 4)")
	t.Index(&a.Person).Named("assignments_person_idx")
	t.Index(&a.Tenant, &a.Exam).Named("assignments_tenant_idx")
}

// InviteEvent is one entry in an invitation's history. An exact repeat of
// the last entry is folded into its Count.
type InviteEvent struct {
	Tenant     Tenant
	Assignment Assignment
	Seq        int32
	At         time.Time
	Kind       string
	Detail     string
	Count      int32
}

func (e *InviteEvent) Schema(t *storm.Table) {
	t.PrimaryKey(&e.Assignment, &e.Seq)
	t.Col(&e.Tenant).ConstraintName("invite_events_tenant_id_fkey")
	t.Col(&e.Assignment).ConstraintName("invite_events_assignment_id_fkey")
	t.Col(&e.Assignment).OnDelete(storm.Cascade)
	t.Col(&e.At).Default("now()")
	t.Col(&e.Detail).Default("''::text")
	t.Col(&e.Count).Default("1")
	t.CheckNamed("invite_events_count_check", "count >= 1")
	t.Index(&e.Tenant).Named("invite_events_tenant_idx")
}

// RetiredToken is a link that was replaced by a newer one, kept so opening it
// can say "superseded" rather than "unknown".
type RetiredToken struct {
	TokenHash  []byte
	Tenant     Tenant
	Assignment Assignment
	RetiredAt  time.Time
}

func (r *RetiredToken) Schema(t *storm.Table) {
	t.PrimaryKey(&r.TokenHash)
	t.Col(&r.TokenHash).NotNull()
	t.Col(&r.Tenant).ConstraintName("retired_tokens_tenant_id_fkey")
	t.Col(&r.Assignment).ConstraintName("retired_tokens_assignment_id_fkey")
	t.Col(&r.Assignment).OnDelete(storm.Cascade)
	t.Col(&r.RetiredAt).Default("now()")
	t.Index(&r.Assignment, &r.RetiredAt).Named("retired_tokens_assignment_idx")
	t.Index(&r.Tenant).Named("retired_tokens_tenant_idx")
}
