package api

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

func (c *client) me(t *testing.T) *v1.Staff {
	t.Helper()
	return get(c.auth.WhoAmI(context.Background(), connect.NewRequest(&v1.WhoAmIRequest{}))).Msg.GetStaff()
}

func (c *client) myTenants(t *testing.T) []*v1.Tenant {
	t.Helper()
	return get(c.tenants.ListMyTenants(context.Background(), connect.NewRequest(&v1.ListMyTenantsRequest{}))).Msg.GetTenants()
}

func (c *client) setAccess(email, tenant string, role v1.Role) error {
	_, err := c.tenants.SetAccess(context.Background(), connect.NewRequest(&v1.SetAccessRequest{Email: email, TenantId: tenant, Role: role}))
	return err
}

func (c *client) newTenant(t *testing.T, name string) string {
	t.Helper()
	return get(c.tenants.CreateTenant(context.Background(), connect.NewRequest(&v1.CreateTenantRequest{Name: name}))).Msg.GetTenant().GetId()
}

func (c *client) switchTo(t *testing.T, tenant string) {
	t.Helper()
	st := get(c.tenants.SwitchTenant(context.Background(), connect.NewRequest(&v1.SwitchTenantRequest{TenantId: tenant}))).Msg.GetStaff()
	if st.GetTenantId() != tenant {
		t.Fatalf("switched to %s, landed in %s", tenant, st.GetTenantId())
	}
}

func newEmail(who string) string {
	return strings.ToLower(who + "-" + secure.Token()[:8] + "@example.test")
}

// An administrator gives a member two tenants; the member works in one at a
// time, cannot administer either, and loses a tenant the moment access to it
// is withdrawn.
func TestAdminLimitsAMemberToChosenTenants(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	admin := h.tenant("Org A")
	tA := h.lastTenant
	tB := admin.newTenant(t, "Org B")
	tC := admin.newTenant(t, "Org C")
	if n := len(admin.myTenants(t)); n != 3 {
		t.Fatalf("the admin should be in the three tenants they made, got %d", n)
	}

	member := newEmail("member")
	for _, tn := range []string{tA, tB} {
		if err := admin.setAccess(member, tn, v1.Role_ROLE_MEMBER); err != nil {
			t.Fatal(err)
		}
	}
	h.mail(member, "staff-access")

	m := h.signIn(member)
	if me := m.me(t); me.GetRole() != v1.Role_ROLE_MEMBER || me.GetTenantId() != tA {
		t.Fatalf("member signed in as %v in %s, want a member in Org A", me.GetRole(), me.GetTenantName())
	}
	mine := m.myTenants(t)
	if len(mine) != 2 {
		t.Fatalf("the member should see exactly Org A and Org B, got %d tenants", len(mine))
	}
	for _, tn := range mine {
		if tn.GetId() == tC {
			t.Error("the member sees Org C, which they were never given")
		}
	}
	_, err := m.tenants.SwitchTenant(ctx, connect.NewRequest(&v1.SwitchTenantRequest{TenantId: tC}))
	if code(err) != connect.CodeNotFound {
		t.Errorf("switching to a tenant you were not given: %v", err)
	}

	// Work done in A stays in A after switching to B.
	inA := get(m.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_QUIZ}))).Msg.GetExam()
	m.switchTo(t, tB)
	for _, e := range get(m.exams.ListExams(ctx, connect.NewRequest(&v1.ListExamsRequest{}))).Msg.GetExams() {
		if e.GetId() == inA.GetId() {
			t.Error("after switching to B the member still sees A's instrument")
		}
	}
	var open int
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM staff_sessions s JOIN staff st ON st.id = s.staff_id
		WHERE lower(st.email) = $1 AND st.tenant_id = $2 AND s.revoked_at IS NULL`, member, tA).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Errorf("switching left %d sessions open in the tenant switched away from", open)
	}

	// A member administers nothing.
	denied := func(name string, err error) {
		t.Helper()
		if code(err) != connect.CodePermissionDenied {
			t.Errorf("%s by a member: %v, want permission denied", name, err)
		}
	}
	_, err = m.tenants.CreateTenant(ctx, connect.NewRequest(&v1.CreateTenantRequest{Name: "Mine"}))
	denied("CreateTenant", err)
	_, err = m.tenants.ListAccess(ctx, connect.NewRequest(&v1.ListAccessRequest{}))
	denied("ListAccess", err)
	denied("SetAccess", m.setAccess(member, tC, v1.Role_ROLE_ADMIN))
	_, err = m.tenants.ListExamCopies(ctx, connect.NewRequest(&v1.ListExamCopiesRequest{ExamId: inA.GetId()}))
	denied("ListExamCopies", err)
	_, err = m.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: inA.GetId(), TenantIds: []string{tC}}))
	denied("ShareExam", err)

	// The admin's table shows the member in A and B only.
	access := get(admin.tenants.ListAccess(ctx, connect.NewRequest(&v1.ListAccessRequest{}))).Msg
	if len(access.GetTenants()) != 3 {
		t.Errorf("the admin administers three tenants, the table has %d", len(access.GetTenants()))
	}
	for _, st := range access.GetStaff() {
		if st.GetEmail() == member && len(st.GetGrants()) != 2 {
			t.Errorf("the member should have two grants, has %d", len(st.GetGrants()))
		}
	}

	// Withdrawing B ends the session there at once, and sign-in lands in A.
	if err := admin.setAccess(member, tB, v1.Role_ROLE_UNSPECIFIED); err != nil {
		t.Fatal(err)
	}
	if _, err := m.auth.WhoAmI(ctx, connect.NewRequest(&v1.WhoAmIRequest{})); code(err) != connect.CodeUnauthenticated {
		t.Errorf("a session in a withdrawn tenant still works: %v", err)
	}
	again := h.signIn(member)
	if me := again.me(t); me.GetTenantId() != tA {
		t.Errorf("after losing B the member signed in to %s, want Org A", me.GetTenantName())
	}
	if n := len(again.myTenants(t)); n != 1 {
		t.Errorf("after losing B the member has %d tenants, want 1", n)
	}
	var logged int
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'staff.access'
		AND detail->>'email' = $2`, tB, member).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if logged != 2 {
		t.Errorf("B's audit log has %d access changes for the member, want 2 (granted, withdrawn)", logged)
	}
}

// A tenant always keeps an administrator.
func TestATenantKeepsAnAdministrator(t *testing.T) {
	h := newHarness(t)
	admin := h.tenant("Solo")
	tn, me := h.lastTenant, admin.me(t).GetEmail()
	if err := admin.setAccess(me, tn, v1.Role_ROLE_MEMBER); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("the last admin demoted themselves: %v", err)
	}
	if err := admin.setAccess(me, tn, v1.Role_ROLE_UNSPECIFIED); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("the last admin removed themselves: %v", err)
	}
	if err := admin.setAccess(newEmail("deputy"), tn, v1.Role_ROLE_ADMIN); err != nil {
		t.Fatal(err)
	}
	if err := admin.setAccess(me, tn, v1.Role_ROLE_MEMBER); err != nil {
		t.Fatalf("with a second admin, stepping down should work: %v", err)
	}
	// The role is read on every request, so the change applies at once.
	if _, err := admin.tenants.ListAccess(context.Background(), connect.NewRequest(&v1.ListAccessRequest{})); code(err) != connect.CodePermissionDenied {
		t.Errorf("an admin who stepped down still administers: %v", err)
	}
}

// Being an admin somewhere does not make you one everywhere.
func TestAdminRightsAreCheckedInEachTenant(t *testing.T) {
	h := newHarness(t)
	x := h.tenant("Xs")
	tX, emailX := h.lastTenant, x.me(t).GetEmail()
	y := h.tenant("Ys")
	tY := h.lastTenant
	h.tenant("Zs")
	tZ := h.lastTenant
	if err := y.setAccess(emailX, tY, v1.Role_ROLE_MEMBER); err != nil {
		t.Fatal(err)
	}
	if err := x.setAccess(newEmail("friend"), tY, v1.Role_ROLE_ADMIN); code(err) != connect.CodePermissionDenied {
		t.Errorf("granting access where you are only a member: %v", err)
	}
	if err := x.setAccess(newEmail("friend"), tZ, v1.Role_ROLE_ADMIN); code(err) != connect.CodeNotFound {
		t.Errorf("granting access to a tenant you are not in: %v", err)
	}
	f := h.publishedFixture(x)
	r := get(x.tenants.ShareExam(context.Background(), connect.NewRequest(&v1.ShareExamRequest{
		ExamId: f.exam.GetId(), TenantIds: []string{tY, tZ, tX},
	}))).Msg.GetResults()
	if len(r) != 3 {
		t.Fatalf("one result per target, got %d", len(r))
	}
	for _, res := range r {
		if res.GetError() == "" || res.GetCopy() != nil {
			t.Errorf("sharing into %s should have been refused", res.GetTenantId())
		}
	}
	var copies int
	if err := h.owner.QueryRow(context.Background(), `SELECT count(*) FROM exams WHERE source_exam_id = $1`, f.exam.GetId()).Scan(&copies); err != nil {
		t.Fatal(err)
	}
	if copies != 0 {
		t.Errorf("%d copies were made where none was allowed", copies)
	}
}

// Sharing publishes a copy in the other tenant, with its own images; sharing
// a later version adds a version to the copy and leaves the one a candidate
// is sitting exactly as it was.
func TestSharingRollsAVersionOutToChosenTenants(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant("Head office")
	email := a.me(t).GetEmail()
	tB := a.newTenant(t, "Branch B")
	tC := a.newTenant(t, "Branch C")

	img := pngBytes(t)
	if status, _ := a.upload(t, img); status != http.StatusCreated {
		t.Fatalf("upload: %d", status)
	}
	_, url := a.upload(t, img)
	e := get(a.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_EXAM}))).Msg.GetExam()
	e = sampleExam(e)
	e.Sections[0].Items[0].Image = &v1.Media{Url: url, Alt: "a red dot"}
	e = get(a.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_DRAFT}))).Msg.GetExam()

	_, err := a.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: e.GetId(), TenantIds: []string{tB}}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Errorf("sharing a draft that was never published: %v", err)
	}
	get(a.exams.SetExamStatus(ctx, connect.NewRequest(&v1.SetExamStatusRequest{Id: e.GetId(), Status: v1.Status_STATUS_PUBLISHED})))

	before := get(a.tenants.ListExamCopies(ctx, connect.NewRequest(&v1.ListExamCopiesRequest{ExamId: e.GetId()}))).Msg.GetCopies()
	if len(before) != 2 || before[0].GetExamId() != "" || before[1].GetExamId() != "" {
		t.Fatalf("before sharing: two tenants, no copies; got %v", before)
	}

	// Only B gets it.
	res := get(a.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: e.GetId(), TenantIds: []string{tB}}))).Msg.GetResults()
	if res[0].GetError() != "" || res[0].GetCopy().GetSourceVersion() != 1 || res[0].GetCopy().GetPublishedVersion() != 1 {
		t.Fatalf("share into B: %v", res[0])
	}
	copyID := res[0].GetCopy().GetExamId()

	b := h.signIn(email)
	b.switchTo(t, tB)
	cp := get(b.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: copyID}))).Msg.GetExam()
	if cp.GetStatus() != v1.Status_STATUS_PUBLISHED || cp.GetSharedFrom().GetVersion() != 1 || cp.GetSharedFrom().GetTenantName() != "Head office" {
		t.Errorf("B's copy: status %v, shared from %v", cp.GetStatus(), cp.GetSharedFrom())
	}
	copyURL := cp.GetSections()[0].GetItems()[0].GetImage().GetUrl()
	if copyURL == url {
		t.Error("B's copy points at A's image")
	}
	if status, _, body := b.fetch(t, copyURL); status != http.StatusOK || !bytes.Equal(body, img) {
		t.Errorf("B cannot read its copy of the image: %d", status)
	}
	if status, _, _ := b.fetch(t, url); status != http.StatusNotFound {
		t.Errorf("B can read A's original image: %d", status)
	}
	c := h.signIn(email)
	c.switchTo(t, tC)
	for _, x := range get(c.exams.ListExams(ctx, connect.NewRequest(&v1.ListExamsRequest{}))).Msg.GetExams() {
		if x.GetSharedFrom() != nil {
			t.Error("C got a copy it was not chosen for")
		}
	}

	// A candidate in B starts on version 1.
	p := get(b.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Name: "Sari", Email: newEmail("cand")}}))).Msg.GetPerson()
	as := get(b.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: copyID, PersonIds: []string{p.GetId()}}))).Msg.GetAssignments()[0]
	cand := h.redeem(&fixture{exam: cp, person: p, assign: as, email: p.GetEmail()})
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	// Version 2 in A, rolled out to B.
	e = get(a.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: e.GetId()}))).Msg.GetExam()
	e.Sections[0].Items[0].Stem = "Pick b, version two"
	get(a.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_NEW_VERSION})))
	get(a.exams.SetExamStatus(ctx, connect.NewRequest(&v1.SetExamStatusRequest{Id: e.GetId(), Status: v1.Status_STATUS_PUBLISHED})))
	res = get(a.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: e.GetId(), TenantIds: []string{tB}}))).Msg.GetResults()
	if res[0].GetError() != "" || res[0].GetCopy().GetSourceVersion() != 2 || res[0].GetCopy().GetPublishedVersion() != 2 {
		t.Fatalf("roll out version 2 to B: %v", res[0])
	}
	versions := get(b.exams.ListExamVersions(ctx, connect.NewRequest(&v1.ListExamVersionsRequest{ExamId: copyID}))).Msg.GetVersions()
	if len(versions) != 2 {
		t.Fatalf("B's copy should have two versions, has %d", len(versions))
	}
	for _, v := range versions {
		stem := v.GetExam().GetSections()[0].GetItems()[0].GetStem()
		if want := map[int32]string{1: "Pick b", 2: "Pick b, version two"}[v.GetVersion()]; stem != want {
			t.Errorf("B's version %d reads %q, want %q", v.GetVersion(), stem, want)
		}
		if v.GetExam().GetSharedFrom() != nil {
			t.Errorf("B's frozen version %d names where it came from; candidates are given it", v.GetVersion())
		}
	}
	var pinned int32
	if err := h.owner.QueryRow(ctx, `SELECT exam_version FROM attempts WHERE id = $1`, att.GetId()).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned != 1 {
		t.Errorf("the candidate's attempt moved to version %d", pinned)
	}

	// Sharing what B already holds changes nothing, and the image is not copied twice.
	res = get(a.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: e.GetId(), TenantIds: []string{tB}}))).Msg.GetResults()
	if res[0].GetCopy().GetPublishedVersion() != 2 {
		t.Errorf("sharing the same version again published %d", res[0].GetCopy().GetPublishedVersion())
	}
	var images int
	if err := h.owner.QueryRow(ctx, `SELECT count(*) FROM media WHERE tenant_id = $1`, tB).Scan(&images); err != nil {
		t.Fatal(err)
	}
	if images != 1 {
		t.Errorf("B holds %d images, want the one the instrument shows", images)
	}
}
