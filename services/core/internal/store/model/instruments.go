package model

// Instruments, their published versions, and the images they show.

import (
	"time"

	"github.com/gsoultan/storm"
)

// Exam is an instrument: a test, a survey or a form, edited in its working
// copy and published as numbered, frozen versions.
type Exam struct {
	storm.Model

	Tenant           Tenant
	Kind             int16
	Status           int16
	Version          int32
	PublishedVersion int32
	Title            string
	Issuer           string
	Tier             int16
	PassPct          int16
	Shuffle          bool
	Timed            bool
	Anonymous        bool
	Sections         storm.JSON
	// SourceExamID names the instrument this one was shared from, in
	// another tenant, so it can carry no foreign key.
	SourceExamID     *[16]byte
	SourceVersion    *int32
	SourceTenantName *string

	Versions    []ExamVersion
	Assignments []Assignment
}

func (e *Exam) Schema(t *storm.Table) {
	t.Col(&e.Tenant).ConstraintName("exams_tenant_id_fkey")
	t.Col(&e.Version).Default("1")
	t.Col(&e.PublishedVersion).Default("0")
	t.CheckNamed("exams_check", "(published_version >= 0) AND (published_version <= version)")
	t.CheckNamed("exams_issuer_check", "length(issuer) <= 300")
	t.CheckNamed("exams_kind_check", "(kind >= 1) AND (kind <= 3)")
	t.CheckNamed("exams_pass_pct_check", "(pass_pct >= 0) AND (pass_pct <= 100)")
	t.CheckNamed("exams_source_tenant_name_check", "length(source_tenant_name) <= 200")
	t.CheckNamed("exams_source_version_check", "source_version >= 1")
	t.CheckNamed("exams_source_whole", "((source_exam_id IS NULL) = (source_version IS NULL)) AND ((source_exam_id IS NULL) = (source_tenant_name IS NULL))")
	t.CheckNamed("exams_status_check", "(status >= 1) AND (status <= 3)")
	t.CheckNamed("exams_tier_check", "(tier >= 0) AND (tier <= 3)")
	t.CheckNamed("exams_title_check", "length(title) <= 300")
	t.CheckNamed("exams_version_check", "version >= 1")
	t.Index(&e.Tenant, &e.SourceExamID).Unique().Where("source_exam_id IS NOT NULL").Named("exams_source_key")
	t.Index(&e.Tenant, storm.Desc(&e.UpdatedAt)).Named("exams_tenant_idx")
}

// ExamVersion is a published version of an instrument, frozen as a
// snapshot. Candidates sit a version, never the working copy.
type ExamVersion struct {
	Tenant      Tenant
	Exam        Exam
	Version     int32
	PublishedAt time.Time
	Note        string
	Snapshot    storm.JSON
}

func (v *ExamVersion) Schema(t *storm.Table) {
	t.PrimaryKey(&v.Exam, &v.Version)
	t.Col(&v.Tenant).ConstraintName("exam_versions_tenant_id_fkey")
	t.Col(&v.Exam).ConstraintName("exam_versions_exam_id_fkey")
	t.Col(&v.Exam).OnDelete(storm.Cascade)
	t.Col(&v.PublishedAt).Default("now()")
	t.Col(&v.Note).Default("''::text")
	t.Index(&v.Tenant).Named("exam_versions_tenant_idx")
}

// Media is an image uploaded to a tenant. The bytes live in the media store;
// this row decides who may read them.
type Media struct {
	ID          [16]byte
	Tenant      Tenant
	ContentType string
	Bytes       int32
	Sha256      []byte
	UploadedBy  string
	CreatedAt   time.Time
}

func (m *Media) Schema(t *storm.Table) {
	t.PrimaryKey(&m.ID)
	t.Name("media")
	t.Col(&m.ID).Default("gen_random_uuid()")
	t.Col(&m.Tenant).ConstraintName("media_tenant_id_fkey")
	t.Col(&m.Sha256).NotNull()
	t.Col(&m.CreatedAt).Default("now()")
	t.CheckNamed("media_bytes_check", "(bytes > 0) AND (bytes <= 4194304)")
	t.CheckNamed("media_content_type_check", "content_type = ANY (ARRAY['image/webp'::text, 'image/jpeg'::text, 'image/png'::text])")
	t.CheckNamed("media_sha256_check", "length(sha256) = 32")
	t.CheckNamed("media_uploaded_by_check", "length(uploaded_by) <= 100")
	t.Index(&m.Tenant, &m.Sha256).Named("media_sha_idx")
	t.Index(&m.Tenant).Named("media_tenant_idx")
	t.Index(&m.Tenant, &m.UploadedBy).Named("media_uploader_idx")
}
