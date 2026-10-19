package api

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// procedures lists every RPC in the schema, from the generated descriptors,
// so a new RPC shows up here without anyone remembering to add it.
func procedures(t *testing.T) []string {
	var out []string
	protoregistry.GlobalFiles.RangeFilesByPackage("assessmatrix.v1", func(fd protoreflect.FileDescriptor) bool {
		for i := 0; i < fd.Services().Len(); i++ {
			svc := fd.Services().Get(i)
			for j := 0; j < svc.Methods().Len(); j++ {
				out = append(out, "/"+string(svc.FullName())+"/"+string(svc.Methods().Get(j).Name()))
			}
		}
		return true
	})
	if len(out) == 0 {
		t.Fatal("no procedures found in the registry")
	}
	slices.Sort(out)
	return out
}

// Every RPC has an access policy; the interceptor refuses anything without one.
func TestEveryProcedureHasAPolicy(t *testing.T) {
	policy := Policy()
	for _, p := range procedures(t) {
		if _, ok := policy[p]; !ok {
			t.Errorf("%s has no access policy", p)
		}
	}
	for p := range policy {
		if !slices.Contains(procedures(t), p) {
			t.Errorf("policy names %s, which is not in the schema", p)
		}
	}
}

// The MVP gate from docs/04: tenant A cannot read or write tenant B's rows
// through any endpoint. Each procedure is called from tenant B with tenant A's
// ids. The test fails if a procedure in the schema has no case here, so a new
// endpoint cannot ship untested.
//
// It runs twice: as the administrator of another tenant, and as someone who
// works in both (an administrator of B, a member of A) while signed in to B.
// A session belongs to one tenant, so the second must reach no more of A
// than the first, whatever else their address is allowed.
func TestTenantIsolationAcrossEveryEndpoint(t *testing.T) {
	t.Run("another tenant's administrator", func(t *testing.T) { checkIsolation(t, false) })
	t.Run("member of both, signed in to the other", func(t *testing.T) { checkIsolation(t, true) })
}

func checkIsolation(t *testing.T, memberOfA bool) {
	h := newHarness(t)
	ctx := context.Background()

	a := h.tenant("Alpha")
	tenantA := h.lastTenant
	fa := h.publishedFixture(a)
	candA := h.redeem(fa)
	attA := get(candA.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(candA.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: attA.GetId(), BatchId: "x",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}}}})))
	get(candA.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: attA.GetId()})))

	var b *client
	if memberOfA {
		email := "both-" + secure.Token()[:8] + "@example.test"
		h.newTenant("Beta", email) // first, so signing in lands here
		get(a.tenants.SetAccess(ctx, connect.NewRequest(&v1.SetAccessRequest{Email: email, TenantId: tenantA, Role: v1.Role_ROLE_MEMBER})))
		b = h.signIn(email)
	} else {
		b = h.tenant("Beta")
	}
	me := get(b.auth.WhoAmI(ctx, connect.NewRequest(&v1.WhoAmIRequest{}))).Msg.GetStaff()
	if me.GetTenantName() != "Beta" {
		t.Fatalf("the intruder is signed in to %q, not Beta", me.GetTenantName())
	}
	tenantB := me.GetTenantId()
	fb := h.publishedFixture(b)
	tokenB := h.lastLink(fb.email)
	candB := h.redeem(fb)

	// A second, unsubmitted exam in A that B will try to change.
	e2 := get(a.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_QUIZ}))).Msg.GetExam()
	p2 := get(a.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Email: "solo@example.test"}}))).Msg.GetPerson()
	as2 := get(a.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: e2.GetId(), PersonIds: []string{p2.GetId()}}))).Msg.GetAssignments()[0]

	// A webhook in A, with one delivery, that B will try to reach.
	hookA := get(a.hooks.CreateWebhook(ctx, connect.NewRequest(&v1.CreateWebhookRequest{
		Url: "https://hooks.alpha.example/am", Events: []string{EventAttemptSubmitted}}))).Msg.GetEndpoint()
	pingA := get(a.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: hookA.GetId()}))).Msg.GetDelivery()

	// Nothing B does may mail A's people.
	quiet := func(name string, f func()) {
		t.Helper()
		before, beforeCand := h.mailCount(a.email), h.mailCount(fa.email)
		f()
		if h.mailCount(a.email) != before || h.mailCount(fa.email) != beforeCand {
			t.Errorf("%s: B's call mailed someone in A", name)
		}
	}
	denied := func(name string, err error) {
		t.Helper()
		if c := connect.CodeOf(err); c != connect.CodeNotFound {
			t.Errorf("%s: tenant B reached tenant A's data (code %v, err %v)", name, c, err)
		}
	}
	// Cases keyed by procedure. Each one asserts B gets nothing of A's.
	cases := map[string]func(){
		"/assessmatrix.v1.ExamService/ListExams": func() {
			r := get(b.exams.ListExams(ctx, connect.NewRequest(&v1.ListExamsRequest{})))
			for _, e := range r.Msg.GetExams() {
				if e.GetId() == fa.exam.GetId() || e.GetId() == e2.GetId() {
					t.Error("ListExams: B sees A's instrument")
				}
			}
		},
		"/assessmatrix.v1.ExamService/GetExam": func() {
			_, err := b.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: fa.exam.GetId()}))
			denied("GetExam", err)
		},
		"/assessmatrix.v1.ExamService/CreateExam": func() {
			// Creates in B's own tenant only; A's list must not grow.
			get(b.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_EXAM})))
			for _, e := range get(a.exams.ListExams(ctx, connect.NewRequest(&v1.ListExamsRequest{}))).Msg.GetExams() {
				if e.GetId() != fa.exam.GetId() && e.GetId() != e2.GetId() {
					t.Error("CreateExam: B's instrument appeared in A")
				}
			}
		},
		"/assessmatrix.v1.ExamService/SaveExam": func() {
			_, err := b.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: sampleExam(&v1.Exam{Id: e2.GetId()}), Mode: v1.SaveMode_SAVE_MODE_DRAFT}))
			denied("SaveExam", err)
		},
		"/assessmatrix.v1.ExamService/DuplicateExam": func() {
			_, err := b.exams.DuplicateExam(ctx, connect.NewRequest(&v1.DuplicateExamRequest{Id: fa.exam.GetId()}))
			denied("DuplicateExam", err)
		},
		"/assessmatrix.v1.ExamService/SetExamStatus": func() {
			_, err := b.exams.SetExamStatus(ctx, connect.NewRequest(&v1.SetExamStatusRequest{Id: fa.exam.GetId(), Status: v1.Status_STATUS_ARCHIVED}))
			denied("SetExamStatus", err)
		},
		"/assessmatrix.v1.ExamService/DeleteExam": func() {
			_, err := b.exams.DeleteExam(ctx, connect.NewRequest(&v1.DeleteExamRequest{Id: e2.GetId()}))
			denied("DeleteExam", err)
		},
		"/assessmatrix.v1.ExamService/ListExamVersions": func() {
			_, err := b.exams.ListExamVersions(ctx, connect.NewRequest(&v1.ListExamVersionsRequest{ExamId: fa.exam.GetId()}))
			denied("ListExamVersions", err)
		},
		"/assessmatrix.v1.PersonService/ListPeople": func() {
			for _, p := range get(b.people.ListPeople(ctx, connect.NewRequest(&v1.ListPeopleRequest{}))).Msg.GetPeople() {
				if p.GetId() == fa.person.GetId() || p.GetId() == p2.GetId() {
					t.Error("ListPeople: B sees A's roster")
				}
			}
		},
		"/assessmatrix.v1.PersonService/AddPerson": func() {
			// The same address in two tenants is two people, not a collision.
			get(b.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Email: fa.email}})))
		},
		"/assessmatrix.v1.PersonService/EnsurePeople": func() {
			r := get(b.people.EnsurePeople(ctx, connect.NewRequest(&v1.EnsurePeopleRequest{People: []*v1.PersonDraft{{Email: "solo@example.test"}}})))
			if r.Msg.GetIds()[0] == p2.GetId() {
				t.Error("EnsurePeople: B resolved an address to A's person")
			}
		},
		"/assessmatrix.v1.PersonService/DeletePerson": func() {
			_, err := b.people.DeletePerson(ctx, connect.NewRequest(&v1.DeletePersonRequest{Id: p2.GetId()}))
			denied("DeletePerson", err)
		},
		"/assessmatrix.v1.PersonService/ErasePerson": func() {
			// A's candidate with a submitted attempt: the worst thing B could wipe.
			_, err := b.people.ErasePerson(ctx, connect.NewRequest(&v1.ErasePersonRequest{Id: fa.person.GetId(), Reason: "test"}))
			denied("ErasePerson", err)
		},
		"/assessmatrix.v1.AssignmentService/ListAssignments": func() {
			r := get(b.assign.ListAssignments(ctx, connect.NewRequest(&v1.ListAssignmentsRequest{ExamId: fa.exam.GetId()})))
			if len(r.Msg.GetAssignments()) != 0 {
				t.Error("ListAssignments: B sees A's assignments")
			}
		},
		"/assessmatrix.v1.AssignmentService/AssignPeople": func() {
			_, err := b.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: fa.exam.GetId(), PersonIds: []string{fb.person.GetId()}}))
			denied("AssignPeople (A's exam)", err)
			_, err = b.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: fb.exam.GetId(), PersonIds: []string{p2.GetId()}}))
			denied("AssignPeople (A's person)", err)
		},
		"/assessmatrix.v1.AssignmentService/Unassign": func() {
			_, err := b.assign.Unassign(ctx, connect.NewRequest(&v1.UnassignRequest{Id: as2.GetId()}))
			denied("Unassign", err)
		},
		"/assessmatrix.v1.AssignmentService/SendInvites": func() {
			_, err := b.assign.SendInvites(ctx, connect.NewRequest(&v1.SendInvitesRequest{AssignmentIds: []string{fa.assign.GetId()}}))
			denied("SendInvites", err)
		},
		"/assessmatrix.v1.AssignmentService/RevokeInvite": func() {
			_, err := b.assign.RevokeInvite(ctx, connect.NewRequest(&v1.RevokeInviteRequest{AssignmentId: fa.assign.GetId()}))
			denied("RevokeInvite", err)
		},
		"/assessmatrix.v1.ResultService/ListResponses": func() {
			_, err := b.results.ListResponses(ctx, connect.NewRequest(&v1.ListResponsesRequest{ExamId: fa.exam.GetId()}))
			denied("ListResponses", err)
		},
		"/assessmatrix.v1.ResultService/GetAttemptLog": func() {
			_, err := b.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: attA.GetId()}))
			denied("GetAttemptLog", err)
		},
		"/assessmatrix.v1.ResultService/ListReviews": func() {
			_, err := b.results.ListReviews(ctx, connect.NewRequest(&v1.ListReviewsRequest{ExamId: fa.exam.GetId(), IncludeDone: true}))
			denied("ListReviews", err)
		},
		"/assessmatrix.v1.ResultService/ScoreAnswer": func() {
			_, err := b.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: attA.GetId(), ItemId: "q4", Points: 1}))
			denied("ScoreAnswer", err)
		},
		"/assessmatrix.v1.ResultService/GetItemAnalysis": func() {
			_, err := b.results.GetItemAnalysis(ctx, connect.NewRequest(&v1.GetItemAnalysisRequest{ExamId: fa.exam.GetId()}))
			denied("GetItemAnalysis", err)
		},
		"/assessmatrix.v1.ProctorService/WatchExam": func() {
			st, err := b.proctor.WatchExam(ctx, connect.NewRequest(&v1.WatchExamRequest{ExamId: fa.exam.GetId()}))
			if err == nil {
				if st.Receive() {
					t.Error("WatchExam: B watched A's exam")
				}
				err = st.Err()
				st.Close()
			}
			denied("WatchExam", err)
		},
		"/assessmatrix.v1.AuthService/WhoAmI": func() {
			r := get(b.auth.WhoAmI(ctx, connect.NewRequest(&v1.WhoAmIRequest{})))
			if r.Msg.GetStaff().GetTenantName() != "Beta" {
				t.Error("WhoAmI: B is told it is someone else")
			}
		},
		// Tenancy: B is an administrator, but of B only.
		"/assessmatrix.v1.TenantService/ListMyTenants": func() {
			for _, tn := range get(b.tenants.ListMyTenants(ctx, connect.NewRequest(&v1.ListMyTenantsRequest{}))).Msg.GetTenants() {
				switch {
				case tn.GetId() != tenantA:
				case !memberOfA:
					t.Error("ListMyTenants: B is told it belongs to A")
				case tn.GetRole() != v1.Role_ROLE_MEMBER || tn.GetCurrent():
					t.Errorf("ListMyTenants: a member of A is told they are %v there (current %t)", tn.GetRole(), tn.GetCurrent())
				}
			}
		},
		"/assessmatrix.v1.TenantService/SwitchTenant": func() {
			r, err := b.tenants.SwitchTenant(ctx, connect.NewRequest(&v1.SwitchTenantRequest{TenantId: tenantA}))
			if !memberOfA {
				denied("SwitchTenant", err)
				return
			}
			// A member may switch, and is a member there, not B's administrator.
			if err != nil || r.Msg.GetStaff().GetRole() != v1.Role_ROLE_MEMBER || r.Msg.GetStaff().GetTenantId() != tenantA {
				t.Fatalf("SwitchTenant: a member of A switched to %v (err %v)", r, err)
			}
			if _, err := b.tenants.CreateTenant(ctx, connect.NewRequest(&v1.CreateTenantRequest{Name: "Through A"})); code(err) != connect.CodePermissionDenied {
				t.Errorf("SwitchTenant: B's administrator rights came along into A (err %v)", err)
			}
			get(b.tenants.SwitchTenant(ctx, connect.NewRequest(&v1.SwitchTenantRequest{TenantId: tenantB})))
		},
		"/assessmatrix.v1.TenantService/CreateTenant": func() {
			// Only a super administrator makes tenants; B administers one.
			_, err := b.tenants.CreateTenant(ctx, connect.NewRequest(&v1.CreateTenantRequest{Name: "Beta Two", AdminEmail: "mole@example.test"}))
			if code(err) != connect.CodePermissionDenied {
				t.Errorf("CreateTenant by a tenant's administrator: %v, want permission denied", err)
			}
		},
		"/assessmatrix.v1.TenantService/ListAccess": func() {
			r := get(b.tenants.ListAccess(ctx, connect.NewRequest(&v1.ListAccessRequest{})))
			for _, tn := range r.Msg.GetTenants() {
				if tn.GetId() == tenantA {
					t.Error("ListAccess: B sees A as a tenant it runs")
				}
			}
			for _, st := range r.Msg.GetStaff() {
				for _, g := range st.GetGrants() {
					if g.GetTenantId() == tenantA {
						t.Error("ListAccess: B sees who works in A")
					}
				}
			}
		},
		"/assessmatrix.v1.TenantService/SetAccess": func() {
			_, err := b.tenants.SetAccess(ctx, connect.NewRequest(&v1.SetAccessRequest{Email: "mole@example.test", TenantId: tenantA, Role: v1.Role_ROLE_MEMBER}))
			if memberOfA {
				if code(err) != connect.CodePermissionDenied {
					t.Errorf("SetAccess: a member of A changed who works there (err %v)", err)
				}
			} else {
				denied("SetAccess", err)
			}
			for _, st := range get(a.tenants.ListAccess(ctx, connect.NewRequest(&v1.ListAccessRequest{}))).Msg.GetStaff() {
				if st.GetEmail() == "mole@example.test" {
					t.Error("SetAccess: B put someone into A")
				}
			}
		},
		"/assessmatrix.v1.TenantService/ListExamCopies": func() {
			_, err := b.tenants.ListExamCopies(ctx, connect.NewRequest(&v1.ListExamCopiesRequest{ExamId: fa.exam.GetId()}))
			denied("ListExamCopies", err)
		},
		"/assessmatrix.v1.TenantService/ShareExam": func() {
			_, err := b.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: fa.exam.GetId(), TenantIds: []string{tenantA}}))
			denied("ShareExam (A's instrument)", err)
			// B's own instrument cannot be pushed into A either.
			r := get(b.tenants.ShareExam(ctx, connect.NewRequest(&v1.ShareExamRequest{ExamId: fb.exam.GetId(), TenantIds: []string{tenantA}})))
			if res := r.Msg.GetResults()[0]; res.GetError() == "" || res.GetCopy() != nil {
				t.Error("ShareExam: B put an instrument into A")
			}
			for _, e := range get(a.exams.ListExams(ctx, connect.NewRequest(&v1.ListExamsRequest{}))).Msg.GetExams() {
				if e.GetSharedFrom() != nil {
					t.Error("ShareExam: A holds a copy B pushed")
				}
			}
		},
		// Candidate procedures: B's candidate session against A's attempt.
		"/assessmatrix.v1.CandidateService/GetSession": func() {
			r := get(candB.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{})))
			if r.Msg.GetAssignmentId() != fb.assign.GetId() {
				t.Error("GetSession: B's candidate was handed another invitation")
			}
		},
		"/assessmatrix.v1.CandidateService/StartAttempt": func() {
			r := get(candB.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents})))
			if r.Msg.GetAttempt().GetId() == attA.GetId() {
				t.Error("StartAttempt: B resumed A's attempt")
			}
		},
		"/assessmatrix.v1.CandidateService/AppendEvents": func() {
			_, err := candB.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: attA.GetId(), BatchId: "evil",
				Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"a"}}}}))
			denied("AppendEvents", err)
		},
		"/assessmatrix.v1.WebhookService/ListWebhooks": func() {
			for _, e := range get(b.hooks.ListWebhooks(ctx, connect.NewRequest(&v1.ListWebhooksRequest{}))).Msg.GetEndpoints() {
				if e.GetId() == hookA.GetId() {
					t.Error("ListWebhooks: B sees A's webhook")
				}
			}
		},
		"/assessmatrix.v1.WebhookService/CreateWebhook": func() {
			get(b.hooks.CreateWebhook(ctx, connect.NewRequest(&v1.CreateWebhookRequest{
				Url: "https://hooks.beta.example/am", Events: []string{EventAttemptSubmitted}})))
			if n := len(get(a.hooks.ListWebhooks(ctx, connect.NewRequest(&v1.ListWebhooksRequest{}))).Msg.GetEndpoints()); n != 1 {
				t.Errorf("CreateWebhook: A has %d webhooks after B created one", n)
			}
		},
		"/assessmatrix.v1.WebhookService/UpdateWebhook": func() {
			_, err := b.hooks.UpdateWebhook(ctx, connect.NewRequest(&v1.UpdateWebhookRequest{Id: hookA.GetId(),
				Url: "https://evil.example/steal", Events: []string{EventAttemptSubmitted}, Active: true}))
			denied("UpdateWebhook", err)
		},
		"/assessmatrix.v1.WebhookService/RotateWebhookSecret": func() {
			_, err := b.hooks.RotateWebhookSecret(ctx, connect.NewRequest(&v1.RotateWebhookSecretRequest{Id: hookA.GetId()}))
			denied("RotateWebhookSecret", err)
		},
		"/assessmatrix.v1.WebhookService/DeleteWebhook": func() {
			_, err := b.hooks.DeleteWebhook(ctx, connect.NewRequest(&v1.DeleteWebhookRequest{Id: hookA.GetId()}))
			denied("DeleteWebhook", err)
		},
		"/assessmatrix.v1.WebhookService/ListDeliveries": func() {
			for _, req := range []*v1.ListDeliveriesRequest{{}, {EndpointId: hookA.GetId()}} {
				if n := len(get(b.hooks.ListDeliveries(ctx, connect.NewRequest(req))).Msg.GetDeliveries()); n != 0 {
					t.Errorf("ListDeliveries: B sees %d of A's deliveries", n)
				}
			}
		},
		"/assessmatrix.v1.WebhookService/Redeliver": func() {
			_, err := b.hooks.Redeliver(ctx, connect.NewRequest(&v1.RedeliverRequest{DeliveryId: pingA.GetId()}))
			denied("Redeliver", err)
		},
		"/assessmatrix.v1.WebhookService/PingWebhook": func() {
			_, err := b.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: hookA.GetId()}))
			denied("PingWebhook", err)
		},
		"/assessmatrix.v1.CandidateService/SubmitAttempt": func() {
			_, err := candB.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: attA.GetId()}))
			denied("SubmitAttempt", err)
		},

		// Public procedures take their own credential: an address and its
		// code, a link and its code, the setup code. Each of B's resolves to B
		// and nothing of A's, and spending it changes nothing in A.
		"/assessmatrix.v1.AuthService/RequestStaffCode": func() {
			quiet("RequestStaffCode", func() {
				get(h.browser().auth.RequestStaffCode(ctx, connect.NewRequest(&v1.RequestStaffCodeRequest{Email: b.email})))
			})
		},
		"/assessmatrix.v1.AuthService/VerifyStaffCode": func() {
			c := h.browser()
			get(c.auth.RequestStaffCode(ctx, connect.NewRequest(&v1.RequestStaffCodeRequest{Email: b.email})))
			mine := h.lastCode(b.email)
			if r := get(c.auth.VerifyStaffCode(ctx, connect.NewRequest(&v1.VerifyStaffCodeRequest{Email: a.email, Code: mine}))); r.Msg.GetOk() {
				t.Error("VerifyStaffCode: B's code signed in as A's administrator")
			}
			r := get(c.auth.VerifyStaffCode(ctx, connect.NewRequest(&v1.VerifyStaffCodeRequest{Email: b.email, Code: mine})))
			if !r.Msg.GetOk() || r.Msg.GetStaff().GetTenantId() == tenantA {
				t.Errorf("VerifyStaffCode: B's own code signed in to %q", r.Msg.GetStaff().GetTenantName())
			}
		},
		"/assessmatrix.v1.AuthService/SignOut": func() {
			c := h.signIn(b.email)
			get(c.auth.SignOut(ctx, connect.NewRequest(&v1.SignOutRequest{})))
			if _, err := a.auth.WhoAmI(ctx, connect.NewRequest(&v1.WhoAmIRequest{})); err != nil {
				t.Errorf("SignOut: B signed A's administrator out (err %v)", err)
			}
		},
		"/assessmatrix.v1.CandidateService/OpenLink": func() {
			for _, token := range []string{tokenB, secure.Token()} {
				r := get(candB.candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: token})))
				if iss := r.Msg.GetInvitation().GetIssuer(); iss != "" && iss != "Beta" {
					t.Errorf("OpenLink: B's link opened %q's invitation", iss)
				}
			}
		},
		"/assessmatrix.v1.CandidateService/RequestCode": func() {
			quiet("RequestCode", func() {
				get(candB.candidate.RequestCode(ctx, connect.NewRequest(&v1.RequestCodeRequest{Token: tokenB})))
			})
		},
		"/assessmatrix.v1.CandidateService/VerifyCode": func() {
			// A's candidate's code, typed against B's link.
			r := get(candB.candidate.VerifyCode(ctx, connect.NewRequest(&v1.VerifyCodeRequest{Token: tokenB, Code: h.lastCode(fa.email)})))
			if r.Msg.GetOk() {
				t.Error("VerifyCode: A's code opened B's link")
			}
			if s := get(candB.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))); s.Msg.GetAssignmentId() != fb.assign.GetId() {
				t.Error("VerifyCode: B's candidate now holds another invitation")
			}
		},
		"/assessmatrix.v1.CandidateService/RequestNewLink": func() {
			quiet("RequestNewLink", func() {
				get(candB.candidate.RequestNewLink(ctx, connect.NewRequest(&v1.RequestNewLinkRequest{Token: tokenB})))
			})
		},
		"/assessmatrix.v1.CandidateService/EndSession": func() {
			get(h.browser().candidate.EndSession(ctx, connect.NewRequest(&v1.EndSessionRequest{})))
			if s := get(candA.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))); s.Msg.GetAssignmentId() != fa.assign.GetId() {
				t.Error("EndSession: A's candidate lost their session")
			}
		},
		"/assessmatrix.v1.SetupService/GetSetupStatus": func() {
			r := get(h.browser().setup.GetSetupStatus(ctx, connect.NewRequest(&v1.GetSetupStatusRequest{})))
			if r.Msg.GetNeeded() || r.Msg.GetSchemaVersion() != "" || r.Msg.GetRegion() != "" {
				t.Errorf("GetSetupStatus: tells anyone %v once organisations exist", r.Msg)
			}
		},
		"/assessmatrix.v1.SetupService/CompleteSetup": func() {
			_, err := h.browser().setup.CompleteSetup(ctx, connect.NewRequest(&v1.CompleteSetupRequest{SetupCode: h.srv.Keys.SetupCode(),
				OrganizationName: "Takeover", AdminEmail: b.email}))
			if code(err) != connect.CodeFailedPrecondition {
				t.Errorf("CompleteSetup: ran again once organisations exist (err %v)", err)
			}
		},
	}

	for _, proc := range procedures(t) {
		run, ok := cases[proc]
		if !ok {
			t.Errorf("%s has no tenant-isolation case; add one before shipping it", proc)
			continue
		}
		t.Run(proc, func(t *testing.T) { run() })
	}

	// After all that, A's data is exactly as it was.
	if r := get(a.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: fa.exam.GetId()}))); r.Msg.GetExam().GetStatus() != v1.Status_STATUS_PUBLISHED {
		t.Error("A's exam was changed by B")
	}
	log := get(a.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: attA.GetId()})))
	if !log.Msg.GetChainIntact() {
		t.Error("A's attempt log no longer verifies")
	}
	if r := get(a.hooks.ListWebhooks(ctx, connect.NewRequest(&v1.ListWebhooksRequest{}))); len(r.Msg.GetEndpoints()) != 1 ||
		r.Msg.GetEndpoints()[0].GetUrl() != hookA.GetUrl() || r.Msg.GetEndpoints()[0].GetSecretHint() != hookA.GetSecretHint() {
		t.Error("A's webhook was changed by B")
	}
}

// Without a tenant set, the application role sees no rows at all: a query
// that forgets its scope returns nothing rather than everything.
func TestNoTenantSeesNothing(t *testing.T) {
	h := newHarness(t)
	h.publishedFixture(h.tenant("Visible"))
	ctx := context.Background()
	for _, table := range tenantTables(t, h) {
		var n int
		err := h.srv.DB.Unscoped(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{table}.Sanitize()).Scan(&n)
		})
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows visible with no tenant set", table, n)
		}
	}
	var other int
	err := h.srv.DB.InTenant(ctx, db.TenantID("00000000-0000-0000-0000-000000000000"), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM exams`).Scan(&other)
	})
	if err != nil || other != 0 {
		t.Errorf("a tenant with no data sees %d exams (err %v)", other, err)
	}
}

// Tables that hold no tenant's data, and why. Everything else in the schema
// must be scoped by row-level security, so a new table without it fails here.
var unscopedTables = map[string]string{
	"schema_migrations": "migration bookkeeping; the application role has no grant on it",
	// A property of a person across the installation, not of one tenant.
	// RLS is on with no policy and the application role has no grant; it is
	// read only through am_super_admin and am_staff_session.
	"super_admins": "installation-wide; the application role has no grant on it",
	// River's own tables. Job arguments carry ids only (mail.SendArgs,
	// webhook.DeliverArgs, RetentionArgs); each worker looks the row up with
	// a SECURITY DEFINER function and then works inside that row's tenant.
	"river_client":       "job queue",
	"river_client_queue": "job queue",
	"river_job":          "job queue",
	"river_leader":       "job queue",
	"river_migration":    "job queue",
	"river_notification": "job queue",
	"river_queue":        "job queue",
}

// tenantTables is every table in the schema that holds tenant rows.
func tenantTables(t *testing.T, h *harness) []string {
	t.Helper()
	rows, err := h.owner.Query(context.Background(), `SELECT c.relname FROM pg_class c
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range names {
		if _, ok := unscopedTables[n]; !ok {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatal("no tenant tables found")
	}
	return out
}

// Every tenant table is under row-level security with exactly one policy,
// the tenant one, for every command. A second permissive policy would widen
// it; a missing WITH CHECK would let a row be written into another tenant.
func TestEveryTableIsTenantScoped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, table := range tenantTables(t, h) {
		key := "tenant_id"
		if table == "tenants" {
			key = "id"
		}
		want := "(" + key + " = am_tenant())"
		var (
			rls     bool
			notNull *bool
			pols    []string
		)
		err := h.owner.QueryRow(ctx, `SELECT c.relrowsecurity,
				(SELECT a.attnotnull FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = $2 AND NOT a.attisdropped),
				coalesce((SELECT array_agg(concat_ws(' ', p.polcmd, p.polpermissive, p.polroles = '{0}',
					pg_get_expr(p.polqual, p.polrelid), pg_get_expr(p.polwithcheck, p.polrelid))) FROM pg_policy p WHERE p.polrelid = c.oid), '{}')
			FROM pg_class c WHERE c.oid = to_regclass('public.' || quote_ident($1))`, table, key).Scan(&rls, &notNull, &pols)
		if err != nil {
			t.Fatal(err)
		}
		if !rls {
			t.Errorf("%s: row-level security is off; enable it with a tenant policy, or list the table in unscopedTables with a reason", table)
		}
		if notNull == nil || !*notNull {
			t.Errorf("%s: has no NOT NULL %s column to scope it by", table, key)
		}
		if exact := "* t t " + want + " " + want; len(pols) != 1 || pols[0] != exact {
			t.Errorf("%s: policies are %q, want exactly one: %q", table, pols, exact)
		}
	}
}

// A row may point only at a row in its own tenant. Foreign keys are checked
// without row-level security, so a key on the id alone would let tenant B
// write a row naming tenant A's exam, which A's deletes would then cascade
// into or be blocked by. Every key between tenant tables includes tenant_id.
func TestForeignKeysStayInsideATenant(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	scoped := map[string]bool{}
	for _, table := range tenantTables(t, h) {
		scoped[table] = true
	}
	rows, err := h.owner.Query(ctx, `SELECT c.conrelid::regclass::text, c.confrelid::regclass::text, pg_get_constraintdef(c.oid),
			EXISTS (SELECT 1 FROM unnest(c.conkey, c.confkey) AS k(a, b)
				JOIN pg_attribute x ON x.attrelid = c.conrelid AND x.attnum = k.a
				JOIN pg_attribute y ON y.attrelid = c.confrelid AND y.attnum = k.b
				WHERE x.attname = 'tenant_id' AND y.attname = 'tenant_id')
		FROM pg_constraint c WHERE c.contype = 'f' AND c.connamespace = 'public'::regnamespace ORDER BY 1, 3`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var from, to, def string
		var keyed bool
		if err := rows.Scan(&from, &to, &def, &keyed); err != nil {
			t.Fatal(err)
		}
		if !scoped[from] || !scoped[to] || to == "tenants" {
			continue
		}
		if !keyed {
			t.Errorf("%s: %s can point into another tenant; key it on (tenant_id, ...)", from, def)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// And the database says so, whatever a handler forgets to check: in
	// tenant B, an assignment naming tenant A's exam is refused.
	a := h.tenant("KeysA")
	fa := h.publishedFixture(a)
	b := h.tenant("KeysB")
	tenantB := h.lastTenant
	fb := h.publishedFixture(b)
	err = h.srv.DB.InTenant(ctx, db.TenantID(tenantB), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assignments (tenant_id, exam_id, exam_version, person_id, sitting)
			VALUES (am_tenant(), $1, 1, $2, 99)`, fa.exam.GetId(), fb.person.GetId())
		return err
	})
	if !isForeignKeyViolation(err) {
		t.Errorf("tenant B wrote an assignment naming tenant A's exam (err %v)", err)
	}
}

// SECURITY DEFINER functions run without row-level security, so each one is
// a hole in it by design. Each is listed here with what keeps it narrow; a new
// one fails this test until someone has looked at it.
var definerFunctions = map[string]string{
	"am_all_tenants":             "every tenant's id, name and region; called only for a super administrator",
	"am_assignment_by_session":   "candidate cookie hash to its one assignment",
	"am_assignment_by_token":     "invitation link hash to its one assignment",
	"am_outbox_sweep":            "deletes sent mail past its keep; returns a count",
	"am_outbox_tenant":           "mail worker: outbox id (from its own job) to that row's tenant",
	"am_public_credential":       "public verification of one issued credential by id",
	"am_public_issuer":           "public keys and name of a credential's issuer",
	"am_purge_attempt":           "refuses unless the attempt is in the caller's tenant",
	"am_retention_due":           "retention worker: ids due for purging, across tenants",
	"am_schema_version":          "newest migration name",
	"am_super_admin":             "whether one address is a super administrator; says nothing else",
	"am_setup_first_tenant":      "refuses once any tenant exists",
	"am_setup_needed":            "whether any tenant exists",
	"am_staff_by_email":          "sign-in: an address to one of its own memberships",
	"am_staff_session":           "staff cookie hash to its one session",
	"am_staff_tenants":           "called only with the signed-in staff member's own address",
	"am_take_analysis":           "psychometrics worker: next queued exam id",
	"am_webhook_delivery_tenant": "webhook worker: delivery id (from its own job) to that row's tenant",
	"am_webhook_sweep":           "deletes webhook events past their keep; returns a count",
}

func TestSecurityDefinerFunctionsAreReviewed(t *testing.T) {
	h := newHarness(t)
	rows, err := h.owner.Query(context.Background(), `SELECT p.proname FROM pg_proc p
		WHERE p.pronamespace = 'public'::regnamespace AND p.prosecdef ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if _, ok := definerFunctions[n]; !ok {
			t.Errorf("%s is SECURITY DEFINER and bypasses row-level security; review it and list it in definerFunctions", n)
		}
	}
	for n := range definerFunctions {
		if !slices.Contains(names, n) {
			t.Errorf("definerFunctions lists %s, which is not SECURITY DEFINER in the schema", n)
		}
	}
}
