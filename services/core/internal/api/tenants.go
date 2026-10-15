package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// tenantService is tenancy as staff see it: which tenants they work in, who
// else does, and instruments shared between them.
//
// Nothing here reads two tenants in one transaction. A request that touches
// several runs one transaction per tenant, each scoped by row-level security
// as usual, and each only after am_staff_tenants has shown the caller holds
// the right role there.
type tenantService struct{ *Server }

// Bounds on one request. Sharing copies images, so it is the expensive one.
const (
	maxShareTargets  = 50
	maxStaffInTenant = 2000
)

var (
	regionShape = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)?$`)
	emailShape  = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

// membership is the caller's place in one tenant.
type membership struct {
	tenant  db.TenantID
	staffID string
	name    string
	region  string
	role    string
}

// in returns ctx as the caller would be if signed in to m's tenant, so the
// audit log there names their staff row there.
func (m *membership) in(ctx context.Context) context.Context {
	return auth.With(ctx, &auth.Principal{Tenant: m.tenant, StaffID: m.staffID, Role: m.role})
}

// memberships is every tenant the signed-in staff member belongs to, found by
// their own address, never one from the request.
func (s *Server) memberships(ctx context.Context) (string, []membership, error) {
	p := auth.From(ctx)
	var email string
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email FROM staff WHERE id = $1`, p.StaffID).Scan(&email)
	}); err != nil {
		return "", nil, err
	}
	var out []membership
	err := s.DB.Unscoped(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, staff_id, tenant_name, region, role FROM am_staff_tenants($1)`, email)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m membership
			if err := rows.Scan(&m.tenant, &m.staffID, &m.name, &m.region, &m.role); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return email, out, err
}

// find returns the caller's membership of a tenant. A tenant they do not
// belong to is not found, whether or not it exists.
func find(ms []membership, id string) (*membership, error) {
	for i := range ms {
		if string(ms[i].tenant) == id {
			return &ms[i], nil
		}
	}
	return nil, notFound("tenant")
}

// adminIn is find, refusing a membership that is not an admin's.
func adminIn(ms []membership, id string) (*membership, error) {
	m, err := find(ms, id)
	if err != nil {
		return nil, err
	}
	if m.role != auth.RoleAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("you are not an administrator of %s", m.name))
	}
	return m, nil
}

func roleIn(r v1.Role) string {
	switch r {
	case v1.Role_ROLE_ADMIN:
		return auth.RoleAdmin
	case v1.Role_ROLE_MEMBER:
		return auth.RoleMember
	}
	return ""
}

func (m *membership) proto(current db.TenantID) *v1.Tenant {
	return &v1.Tenant{Id: string(m.tenant), Name: m.name, Region: m.region, Role: roleProto(m.role), Current: m.tenant == current}
}

func (s *tenantService) ListMyTenants(ctx context.Context, _ *connect.Request[v1.ListMyTenantsRequest]) (*connect.Response[v1.ListMyTenantsResponse], error) {
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ListMyTenants", err)
	}
	out := &v1.ListMyTenantsResponse{}
	for i := range ms {
		out.Tenants = append(out.Tenants, ms[i].proto(auth.From(ctx).Tenant))
	}
	return connect.NewResponse(out), nil
}

// SwitchTenant mints a session in the other tenant, then ends this one. A
// session never spans tenants, so every query after the switch is scoped to
// the new tenant by the same machinery as before it.
func (s *tenantService) SwitchTenant(ctx context.Context, req *connect.Request[v1.SwitchTenantRequest]) (*connect.Response[v1.SwitchTenantResponse], error) {
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "SwitchTenant", err)
	}
	m, err := find(ms, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	p := auth.From(ctx)
	out := &v1.SwitchTenantResponse{}
	if m.tenant == p.Tenant {
		err := s.tx(ctx, func(tx pgx.Tx) (err error) {
			out.Staff, err = staffProto(ctx, tx, p.StaffID)
			return err
		})
		if err != nil {
			return nil, s.internal(ctx, "SwitchTenant", err)
		}
		return connect.NewResponse(out), nil
	}
	session := secure.Token()
	err = s.DB.InTenant(ctx, m.tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO staff_sessions (tenant_id, staff_id, token_hash, expires_at)
			VALUES (am_tenant(), $1, $2, $3)`, m.staffID, s.Keys.SessionHash(session), s.now().Add(auth.StaffSessionTTL)); err != nil {
			return err
		}
		out.Staff, err = staffProto(ctx, tx, m.staffID)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "SwitchTenant", err)
	}
	if token := auth.CookieValue(req.Header(), auth.StaffCookie); secure.ValidToken(token) {
		if err := s.tx(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE staff_sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`,
				s.Keys.SessionHash(token))
			return err
		}); err != nil {
			return nil, s.internal(ctx, "SwitchTenant", err)
		}
	}
	res := connect.NewResponse(out)
	s.Auth.SetCookie(res.Header(), auth.StaffCookie, session, auth.StaffSessionTTL)
	return res, nil
}

func (s *tenantService) CreateTenant(ctx context.Context, req *connect.Request[v1.CreateTenantRequest]) (*connect.Response[v1.CreateTenantResponse], error) {
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" || len(name) > 200 {
		return nil, invalidArg(errors.New("a tenant needs a name of up to 200 characters"))
	}
	region := strings.TrimSpace(req.Msg.GetRegion())
	if region != "" && !regionShape.MatchString(region) {
		return nil, invalidArg(errors.New("region must be a two-letter code such as id or sg"))
	}
	p := auth.From(ctx)
	var email, staffName, here string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT s.email, s.name, t.region FROM staff s JOIN tenants t ON t.id = s.tenant_id
			WHERE s.id = $1`, p.StaffID).Scan(&email, &staffName, &here)
	})
	if err != nil {
		return nil, s.internal(ctx, "CreateTenant", err)
	}
	if region == "" {
		region = here
	}
	m := membership{tenant: db.TenantID(uuid.NewString()), name: name, region: region, role: auth.RoleAdmin}
	// Row-level security lets a transaction insert only its own tenant's
	// rows, the new tenant included: it is scoped to the new id first.
	err = s.DB.InTenant(ctx, m.tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name, region) VALUES (am_tenant(), $1, $2)`, name, region); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO staff (tenant_id, email, name, role) VALUES (am_tenant(), $1, $2, 'admin')
			RETURNING id`, email, staffName).Scan(&m.staffID); err != nil {
			return err
		}
		return audit(m.in(ctx), tx, "tenant.created", string(m.tenant), map[string]any{"name": name, "region": region, "from": string(p.Tenant)})
	})
	if err == nil {
		err = s.tx(ctx, func(tx pgx.Tx) error {
			return audit(ctx, tx, "tenant.created", string(m.tenant), map[string]any{"name": name, "region": region})
		})
	}
	if err != nil {
		return nil, s.internal(ctx, "CreateTenant", err)
	}
	return connect.NewResponse(&v1.CreateTenantResponse{Tenant: m.proto(p.Tenant)}), nil
}

// staffIn lists the active staff of one tenant, by address.
func staffIn(ctx context.Context, tx pgx.Tx) ([][3]string, error) {
	rows, err := tx.Query(ctx, `SELECT lower(email), name, role FROM staff WHERE disabled_at IS NULL
		ORDER BY lower(email) LIMIT $1`, maxStaffInTenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var r [3]string
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// access is ListAccess's table, optionally for one address.
func (s *Server) access(ctx context.Context, ms []membership, only string) (*v1.ListAccessResponse, error) {
	out := &v1.ListAccessResponse{}
	byEmail := map[string]*v1.StaffAccess{}
	for i := range ms {
		m := &ms[i]
		if m.role != auth.RoleAdmin {
			continue
		}
		out.Tenants = append(out.Tenants, m.proto(auth.From(ctx).Tenant))
		var rows [][3]string
		err := s.DB.InTenant(ctx, m.tenant, func(tx pgx.Tx) (err error) {
			rows, err = staffIn(ctx, tx)
			return err
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if only != "" && r[0] != only {
				continue
			}
			sa := byEmail[r[0]]
			if sa == nil {
				sa = &v1.StaffAccess{Email: r[0]}
				byEmail[r[0]] = sa
				out.Staff = append(out.Staff, sa)
			}
			if sa.Name == "" {
				sa.Name = r[1]
			}
			sa.Grants = append(sa.Grants, &v1.AccessGrant{TenantId: string(m.tenant), Role: roleProto(r[2])})
		}
	}
	slices.SortFunc(out.Staff, func(a, b *v1.StaffAccess) int { return strings.Compare(a.GetEmail(), b.GetEmail()) })
	return out, nil
}

func (s *tenantService) ListAccess(ctx context.Context, _ *connect.Request[v1.ListAccessRequest]) (*connect.Response[v1.ListAccessResponse], error) {
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ListAccess", err)
	}
	out, err := s.access(ctx, ms, "")
	if err != nil {
		return nil, s.internal(ctx, "ListAccess", err)
	}
	return connect.NewResponse(out), nil
}

// SetAccess grants, changes or withdraws one person's access to one tenant.
// Withdrawing ends their sessions there at once. A tenant always keeps at
// least one administrator, so nobody can lock everyone out of it.
func (s *tenantService) SetAccess(ctx context.Context, req *connect.Request[v1.SetAccessRequest]) (*connect.Response[v1.SetAccessResponse], error) {
	email := strings.ToLower(strings.TrimSpace(req.Msg.GetEmail()))
	if len(email) > 320 || !emailShape.MatchString(email) {
		return nil, invalidArg(errors.New("enter a valid email address"))
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if len(name) > 200 {
		return nil, invalidArg(errors.New("a name is at most 200 characters"))
	}
	role := roleIn(req.Msg.GetRole())
	if req.Msg.GetRole() != v1.Role_ROLE_UNSPECIFIED && role == "" {
		return nil, invalidArg(errors.New("unknown role"))
	}
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	m, err := adminIn(ms, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	var added bool
	err = s.DB.InTenant(ctx, m.tenant, func(tx pgx.Tx) error {
		// One change to a tenant's administrators at a time, so two admins
		// demoting each other cannot both pass the last-admin check.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tenants WHERE id = am_tenant() FOR UPDATE`); err != nil {
			return err
		}
		var (
			id      string
			was     string
			active  bool
			current = true
		)
		err := tx.QueryRow(ctx, `SELECT id, role, disabled_at IS NULL FROM staff WHERE lower(email) = $1 FOR UPDATE`, email).
			Scan(&id, &was, &active)
		if errors.Is(err, pgx.ErrNoRows) {
			current = false
		} else if err != nil {
			return err
		}
		if current && active && was == auth.RoleAdmin && role != auth.RoleAdmin {
			var others int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM staff WHERE role = 'admin' AND disabled_at IS NULL AND id <> $1`, id).
				Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return failedPrecondition(m.name + " needs at least one administrator; make someone else an administrator first")
			}
		}
		if role == "" {
			if !current || !active {
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE staff SET disabled_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE staff_sessions SET revoked_at = now() WHERE staff_id = $1 AND revoked_at IS NULL`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM staff_codes WHERE staff_id = $1`, id); err != nil {
				return err
			}
			return audit(m.in(ctx), tx, "staff.access", id, map[string]any{"email": email, "role": "none", "was": was})
		}
		if err := tx.QueryRow(ctx, `INSERT INTO staff (tenant_id, email, name, role) VALUES (am_tenant(), $1, $2, $3)
			ON CONFLICT (tenant_id, lower(email)) DO UPDATE SET role = excluded.role, disabled_at = NULL,
				name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE staff.name END
			RETURNING id`, email, name, role).Scan(&id); err != nil {
			return err
		}
		if !current || !active {
			was, added = "none", true
		}
		if err := audit(m.in(ctx), tx, "staff.access", id, map[string]any{"email": email, "role": role, "was": was}); err != nil {
			return err
		}
		if !added {
			return nil
		}
		return s.queueMail(ctx, tx, "staff-access", email, "Anda mendapat akses ke "+m.name+" di AssessMatrix",
			"Anda sekarang dapat bekerja di "+m.name+" di AssessMatrix.\n\nMasuk dengan alamat email ini di "+
				strings.TrimRight(s.PublicURL, "/")+"/ ; kode masuk akan dikirim ke email ini.")
	})
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	out, err := s.access(ctx, ms, email)
	if err != nil {
		return nil, s.internal(ctx, "SetAccess", err)
	}
	res := &v1.SetAccessResponse{Staff: &v1.StaffAccess{Email: email}}
	if len(out.GetStaff()) > 0 {
		res.Staff = out.GetStaff()[0]
	}
	return connect.NewResponse(res), nil
}

/* ── sharing instruments ───────────────────────────────────────────────── */

// copyIn reads the copy of an original held by the tenant tx is scoped to.
func copyIn(ctx context.Context, tx pgx.Tx, m *membership, sourceID string) (*v1.ExamCopy, error) {
	c := &v1.ExamCopy{TenantId: string(m.tenant), TenantName: m.name}
	var status int16
	err := tx.QueryRow(ctx, `SELECT id, source_version, published_version, status FROM exams WHERE source_exam_id = $1`, sourceID).
		Scan(&c.ExamId, &c.SourceVersion, &c.PublishedVersion, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	c.Status = v1.Status(status)
	return c, err
}

func (s *tenantService) ListExamCopies(ctx context.Context, req *connect.Request[v1.ListExamCopiesRequest]) (*connect.Response[v1.ListExamCopiesResponse], error) {
	id := req.Msg.GetExamId()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		_, err := getExam(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "ListExamCopies", err)
	}
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ListExamCopies", err)
	}
	out := &v1.ListExamCopiesResponse{}
	for i := range ms {
		m := &ms[i]
		if m.role != auth.RoleAdmin || m.tenant == auth.From(ctx).Tenant {
			continue
		}
		err := s.DB.InTenant(ctx, m.tenant, func(tx pgx.Tx) error {
			c, err := copyIn(ctx, tx, m, id)
			out.Copies = append(out.Copies, c)
			return err
		})
		if err != nil {
			return nil, s.internal(ctx, "ListExamCopies", err)
		}
	}
	return connect.NewResponse(out), nil
}

// sharedImage is an uploaded image a shared version shows.
type sharedImage struct {
	url, contentType string
	bytes            int32
	sha              []byte
}

// original is the version being shared, read once in its own tenant.
type original struct {
	tenant     db.TenantID
	tenantName string
	exam       *v1.Exam
	version    int32
	images     []sharedImage
}

// uploadedImages walks every image an instrument shows.
func uploadedImages(e *v1.Exam, fn func(m *v1.Media)) {
	for _, sec := range e.GetSections() {
		for _, it := range sec.GetItems() {
			if m := it.GetImage(); m != nil {
				fn(m)
			}
			for _, c := range it.GetChoices() {
				if m := c.GetImage(); m != nil {
					fn(m)
				}
			}
		}
	}
}

func (s *tenantService) ShareExam(ctx context.Context, req *connect.Request[v1.ShareExamRequest]) (*connect.Response[v1.ShareExamResponse], error) {
	targets := slices.Compact(slices.Sorted(slices.Values(req.Msg.GetTenantIds())))
	if len(targets) == 0 {
		return nil, invalidArg(errors.New("choose at least one tenant to share with"))
	}
	if len(targets) > maxShareTargets {
		return nil, invalidArg(fmt.Errorf("share with at most %d tenants at a time", maxShareTargets))
	}
	src := &original{tenant: auth.From(ctx).Tenant}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetExamId(), false)
		if err != nil {
			return err
		}
		src.version = req.Msg.GetVersion()
		if src.version == 0 {
			src.version = e.GetPublishedVersion()
		}
		if src.version == 0 {
			return failedPrecondition("publish this instrument before sharing it")
		}
		if src.exam, err = examAt(ctx, tx, e.GetId(), src.version); err != nil {
			return err
		}
		src.exam.Id = e.GetId()
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = am_tenant()`).Scan(&src.tenantName); err != nil {
			return err
		}
		seen := map[string]bool{}
		var missing error
		uploadedImages(src.exam, func(m *v1.Media) {
			id, ok := strings.CutPrefix(m.GetUrl(), MediaPrefix)
			if !ok || seen[id] || missing != nil || !validUUID(id) {
				return
			}
			seen[id] = true
			img := sharedImage{url: m.GetUrl()}
			err := tx.QueryRow(ctx, `SELECT content_type, bytes, sha256 FROM media WHERE id = $1`, id).Scan(&img.contentType, &img.bytes, &img.sha)
			if errors.Is(err, pgx.ErrNoRows) {
				return // already broken here; the copy shows it broken too
			}
			if err != nil {
				missing = err
				return
			}
			src.images = append(src.images, img)
		})
		return missing
	})
	if err != nil {
		return nil, s.internal(ctx, "ShareExam", err)
	}
	_, ms, err := s.memberships(ctx)
	if err != nil {
		return nil, s.internal(ctx, "ShareExam", err)
	}
	out := &v1.ShareExamResponse{}
	var shared []string
	for _, id := range targets {
		r := &v1.ShareResult{TenantId: id}
		out.Results = append(out.Results, r)
		m, err := adminIn(ms, id)
		if err == nil && m.tenant == src.tenant {
			err = failedPrecondition("That is the tenant this instrument is in.")
		}
		if err == nil {
			r.Copy, err = s.shareInto(ctx, src, m)
		}
		if err != nil {
			// Each target fails on its own, in words an administrator can act on.
			var ce *connect.Error
			if errors.As(err, &ce) {
				r.Error = ce.Message()
			} else {
				s.Log.ErrorContext(ctx, "share failed", "tenant", id, "err", err)
				r.Error = "It could not be shared there. Try again; if it keeps failing, tell your support contact."
			}
			continue
		}
		shared = append(shared, id)
	}
	if len(shared) > 0 {
		err := s.tx(ctx, func(tx pgx.Tx) error {
			return audit(ctx, tx, "exam.shared", src.exam.GetId(), map[string]any{"version": src.version, "tenants": shared})
		})
		if err != nil {
			return nil, s.internal(ctx, "ShareExam", err)
		}
	}
	return connect.NewResponse(out), nil
}

// shareInto brings one tenant's copy up to the original's version and
// publishes it there. Images are copied into the tenant, so its candidates
// never read another tenant's objects; one it already holds is reused.
//
// The copy's earlier versions stay as they were: invitations and attempts
// pinned to them keep the paper they were given.
func (s *tenantService) shareInto(ctx context.Context, src *original, m *membership) (*v1.ExamCopy, error) {
	if len(src.images) > 0 && (s.Media.Store == nil || m.region != s.Media.Region) {
		return nil, failedPrecondition(fmt.Sprintf("Its images cannot be stored in %s's region (%s).", m.name, m.region))
	}
	ctx = m.in(ctx)
	var out *v1.ExamCopy
	err := s.DB.InTenant(ctx, m.tenant, func(tx pgx.Tx) error {
		var (
			copyID string
			at     int32
		)
		err := tx.QueryRow(ctx, `SELECT id, source_version FROM exams WHERE source_exam_id = $1 FOR UPDATE`, src.exam.GetId()).
			Scan(&copyID, &at)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		next := proto.Clone(src.exam).(*v1.Exam)
		next.SharedFrom = nil
		urls := map[string]string{}
		for _, img := range src.images {
			if urls[img.url], err = s.copyImage(ctx, tx, src.tenant, img); err != nil {
				return err
			}
		}
		uploadedImages(next, func(im *v1.Media) {
			if u, ok := urls[im.GetUrl()]; ok {
				im.Url = u
			}
		})

		var cur *v1.Exam
		if copyID == "" {
			if cur, err = insertExam(ctx, tx, next); err != nil {
				return err
			}
		} else if cur, err = getExam(ctx, tx, copyID, true); err != nil {
			return err
		}
		if copyID != "" && at == src.version && cur.GetStatus() == v1.Status_STATUS_PUBLISHED && cur.GetPublishedVersion() == cur.GetVersion() {
			out, err = copyIn(ctx, tx, m, src.exam.GetId())
			return err // already holds this version, live
		}
		// A new version after a live one; over an unpublished draft, which
		// sharing replaces, the draft's own number.
		next.Id, next.Status, next.Version = cur.GetId(), v1.Status_STATUS_PUBLISHED, cur.GetVersion()
		if cur.GetPublishedVersion() == cur.GetVersion() {
			next.Version++
		}
		next.PublishedVersion = next.GetVersion()
		if _, err := writeExam(ctx, tx, next); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE exams SET source_exam_id = $2, source_version = $3, source_tenant_name = $4 WHERE id = $1`,
			cur.GetId(), src.exam.GetId(), src.version, src.tenantName); err != nil {
			return err
		}
		published, err := getExam(ctx, tx, cur.GetId(), false)
		if err != nil {
			return err
		}
		if err := snapshot(ctx, tx, published, fmt.Sprintf("shared from %s, version %d", src.tenantName, src.version)); err != nil {
			return err
		}
		if err := audit(ctx, tx, "exam.shared_in", cur.GetId(), map[string]any{
			"from_tenant": string(src.tenant), "from_exam": src.exam.GetId(), "from_version": src.version, "version": next.GetVersion(),
		}); err != nil {
			return err
		}
		out, err = copyIn(ctx, tx, m, src.exam.GetId())
		return err
	})
	return out, err
}

// copyImage puts one of the original's images in the tenant tx is scoped to
// and returns its URL there.
func (s *tenantService) copyImage(ctx context.Context, tx pgx.Tx, from db.TenantID, img sharedImage) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM media WHERE sha256 = $1 AND content_type = $2 LIMIT 1`, img.sha, img.contentType).Scan(&id)
	if err == nil {
		return MediaPrefix + id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	srcKey, err := media.Key(string(from), strings.TrimPrefix(img.url, MediaPrefix))
	if err != nil {
		return "", err
	}
	r, err := s.Media.Get(ctx, srcKey)
	if err != nil {
		return "", fmt.Errorf("read image %s: %w", srcKey, err)
	}
	body, err := io.ReadAll(io.LimitReader(r, media.MaxBytes+1))
	r.Close()
	if err != nil {
		return "", err
	}
	if len(body) > media.MaxBytes {
		return "", fmt.Errorf("image %s is larger than an upload may be", srcKey)
	}
	p := auth.From(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO media (tenant_id, content_type, bytes, sha256, uploaded_by)
		VALUES (am_tenant(), $1, $2, $3, $4) RETURNING id`, img.contentType, len(body), img.sha, "staff:"+p.StaffID).Scan(&id); err != nil {
		return "", err
	}
	key, err := media.Key(string(p.Tenant), id)
	if err != nil {
		return "", err
	}
	// Stored before the row commits, as an upload is.
	if err := s.Media.Put(ctx, key, img.contentType, body); err != nil {
		return "", err
	}
	return MediaPrefix + id, nil
}
