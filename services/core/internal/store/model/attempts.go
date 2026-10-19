package model

// Attempts, their evidence, their scores, and the credentials they earn.

import (
	"time"

	"github.com/gsoultan/storm"
)

// Attempt is a candidate's work on one assignment. Its event log is a hash
// chain; LastSeq and LastHash are the chain's head.
type Attempt struct {
	ID          [16]byte
	Tenant      Tenant
	Assignment  Assignment
	Exam        Exam
	ExamVersion int32
	// PersonID is copied from the assignment and carries no foreign key:
	// the attempt is reached through Assignment, and erasing a person
	// removes their attempts explicitly first.
	PersonID    [16]byte
	StartedAt   time.Time
	DeadlineAt  *time.Time
	SubmittedAt *time.Time
	Score       *storm.JSON
	LastSeq     int64
	LastHash    []byte
	PurgedAt    *time.Time
	PurgedWhy   *string
	// Seal is keyed over the chain's head and the purge time
	// (secure.AttemptSeal), so a truncated log or a hand-marked purge shows.
	Seal []byte

	Events     []AttemptEvent
	Batches    []AttemptBatch
	ItemScores []ItemScore
}

func (a *Attempt) Schema(t *storm.Table) {
	t.PrimaryKey(&a.ID)
	// What a child's (tenant_id, …_id) key points at (migration 0008_tenant_keys).
	t.UniqueNamed("attempts_tenant_id_key", &a.Tenant, &a.ID)
	t.Col(&a.ID).Default("gen_random_uuid()")
	t.Col(&a.Tenant).ConstraintName("attempts_tenant_id_fkey")
	t.Col(&a.Assignment).ConstraintName("attempts_assignment_id_fkey")
	t.Col(&a.Assignment).OnDelete(storm.Restrict)
	t.Col(&a.Exam).ConstraintName("attempts_exam_id_fkey")
	t.Col(&a.Exam).OnDelete(storm.Restrict)
	t.Col(&a.Exam).NoIndex()
	t.Col(&a.StartedAt).Default("now()")
	t.Col(&a.LastSeq).Default("0")
	t.Col(&a.LastHash).NotNull()
	t.UniqueNamed("attempts_assignment_id_key", &a.Assignment)
	t.CheckNamed("attempts_purged_why_check", "length(purged_why) <= 200")
	t.Index(storm.IndexExpr(&a.SubmittedAt, "COALESCE(%s, deadline_at, started_at)")).Where("purged_at IS NULL").Named("attempts_retention_idx")
	t.Index(&a.Tenant, &a.Exam).Named("attempts_tenant_idx")
	t.Index(&a.PersonID).Named("attempts_person_idx")
}

// AttemptEvent is one entry in an attempt's tamper-evident log.
// Append-only: a trigger refuses UPDATE and DELETE.
type AttemptEvent struct {
	Tenant     Tenant
	Attempt    Attempt
	Seq        int64
	ServerTime time.Time
	Kind       string
	Body       []byte
	Hash       []byte
}

func (e *AttemptEvent) Schema(t *storm.Table) {
	t.PrimaryKey(&e.Attempt, &e.Seq)
	t.Col(&e.Tenant).ConstraintName("attempt_events_tenant_id_fkey")
	t.Col(&e.Attempt).ConstraintName("attempt_events_attempt_id_fkey")
	t.Col(&e.Attempt).OnDelete(storm.Restrict)
	t.Col(&e.Body).NotNull()
	t.Col(&e.Hash).NotNull()
	t.CheckNamed("attempt_events_seq_check", "seq >= 1")
	t.Index(&e.Tenant).Named("attempt_events_tenant_idx")
}

// AttemptBatch records a batch of events already stored, so a retried batch
// is acknowledged instead of stored twice.
type AttemptBatch struct {
	Tenant  Tenant
	Attempt Attempt
	BatchID string
	LastSeq int64
}

func (b *AttemptBatch) Schema(t *storm.Table) {
	t.PrimaryKey(&b.Attempt, &b.BatchID)
	t.Col(&b.Tenant).ConstraintName("attempt_batches_tenant_id_fkey")
	t.Col(&b.Attempt).ConstraintName("attempt_batches_attempt_id_fkey")
	t.Col(&b.Attempt).OnDelete(storm.Restrict)
	t.CheckNamed("attempt_batches_batch_id_check", "(length(batch_id) >= 1) AND (length(batch_id) <= 64)")
	t.Index(&b.Tenant).Named("attempt_batches_tenant_idx")
}

// ItemScore is one item's result in a scored attempt: the index item
// analysis reads.
type ItemScore struct {
	Tenant      Tenant
	Attempt     Attempt
	Exam        Exam
	ExamVersion int32
	ItemID      string
	Points      *int32
	MaxPoints   int32
	Choices     []string
}

func (s *ItemScore) Schema(t *storm.Table) {
	t.PrimaryKey(&s.Attempt, &s.ItemID)
	t.Col(&s.Tenant).ConstraintName("item_scores_tenant_id_fkey")
	t.Col(&s.Attempt).ConstraintName("item_scores_attempt_id_fkey")
	t.Col(&s.Attempt).OnDelete(storm.Cascade)
	t.Col(&s.Exam).ConstraintName("item_scores_exam_id_fkey")
	t.Col(&s.Exam).OnDelete(storm.Restrict)
	t.Col(&s.Exam).NoIndex()
	t.Col(&s.Choices).Default("'{}'::text[]")
	t.CheckNamed("item_scores_item_id_check", "length(item_id) <= 64")
	t.CheckNamed("item_scores_max_points_check", "max_points >= 0")
	t.CheckNamed("item_scores_points_check", "points >= 0")
	t.Index(&s.Tenant, &s.Exam, &s.ExamVersion).Named("item_scores_exam_idx")
}

// ItemStat is the item analysis of one published version.
type ItemStat struct {
	Tenant      Tenant
	Exam        Exam
	ExamVersion int32
	ComputedAt  time.Time
	Stats       storm.JSON
}

func (s *ItemStat) Schema(t *storm.Table) {
	t.PrimaryKey(&s.Tenant, &s.Exam, &s.ExamVersion)
	t.Col(&s.Tenant).ConstraintName("item_stats_tenant_id_fkey")
	t.Col(&s.Exam).ConstraintName("item_stats_exam_id_fkey")
	t.Col(&s.Exam).OnDelete(storm.Cascade)
	t.Col(&s.Exam).NoIndex()
	t.Col(&s.ComputedAt).Default("now()")
}

// AnalysisRequest asks the psychometrics worker to recompute the item
// analysis of one published version. A request already queued is refreshed.
type AnalysisRequest struct {
	Tenant      Tenant
	Exam        Exam
	ExamVersion int32
	RequestedAt time.Time
}

func (r *AnalysisRequest) Schema(t *storm.Table) {
	t.PrimaryKey(&r.Tenant, &r.Exam, &r.ExamVersion)
	t.Name("analysis_queue")
	t.Col(&r.Tenant).ConstraintName("analysis_queue_tenant_id_fkey")
	t.Col(&r.Exam).ConstraintName("analysis_queue_exam_id_fkey")
	t.Col(&r.Exam).OnDelete(storm.Cascade)
	t.Col(&r.Exam).NoIndex()
	t.Col(&r.RequestedAt).Default("now()")
}

// Credential is the signed result issued for a passed attempt, verifiable by
// anyone who holds its id.
type Credential struct {
	ID         [16]byte
	Tenant     Tenant
	Attempt    Attempt
	Jwt        string
	IssuedAt   time.Time
	RevokedAt  *time.Time
	RevokedWhy *string
}

func (c *Credential) Schema(t *storm.Table) {
	t.PrimaryKey(&c.ID)
	t.Col(&c.ID).Default("gen_random_uuid()")
	t.Col(&c.Tenant).ConstraintName("credentials_tenant_id_fkey")
	t.Col(&c.Attempt).ConstraintName("credentials_attempt_id_fkey")
	t.Col(&c.Attempt).OnDelete(storm.Restrict)
	t.Col(&c.IssuedAt).Default("now()")
	t.UniqueNamed("credentials_attempt_id_key", &c.Attempt)
	t.Index(&c.Tenant).Named("credentials_tenant_idx")
}

// IssuerKey is a tenant's signing key for credentials. One is current; the
// retired ones still verify what they signed.
type IssuerKey struct {
	Tenant     Tenant
	Kid        string
	PublicJwk  storm.JSON
	PrivateKey []byte
	CreatedAt  time.Time
	RetiredAt  *time.Time
}

func (k *IssuerKey) Schema(t *storm.Table) {
	t.PrimaryKey(&k.Tenant, &k.Kid)
	t.Col(&k.Tenant).ConstraintName("issuer_keys_tenant_id_fkey")
	t.Col(&k.PrivateKey).NotNull()
	t.Col(&k.CreatedAt).Default("now()")
	t.CheckNamed("issuer_keys_kid_check", "(length(kid) >= 8) AND (length(kid) <= 64)")
	t.Index(&k.Tenant).Unique().Where("retired_at IS NULL").Named("issuer_keys_current")
}
