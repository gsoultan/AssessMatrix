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
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
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
// through any endpoint. Each staff and candidate procedure is called by
// tenant B with tenant A's ids. The test fails if a procedure in the schema
// has no case here, so a new endpoint cannot ship untested.
func TestTenantIsolationAcrossEveryEndpoint(t *testing.T) {
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

	b := h.tenant("Beta")
	fb := h.publishedFixture(b)
	candB := h.redeem(fb)

	// A second, unsubmitted exam in A that B will try to change.
	e2 := get(a.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_QUIZ}))).Msg.GetExam()
	p2 := get(a.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Email: "solo@example.test"}}))).Msg.GetPerson()
	as2 := get(a.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: e2.GetId(), PersonIds: []string{p2.GetId()}}))).Msg.GetAssignments()[0]

	// A webhook in A, with one delivery, that B will try to reach.
	hookA := get(a.hooks.CreateWebhook(ctx, connect.NewRequest(&v1.CreateWebhookRequest{
		Url: "https://hooks.alpha.example/am", Events: []string{EventAttemptSubmitted}}))).Msg.GetEndpoint()
	pingA := get(a.hooks.PingWebhook(ctx, connect.NewRequest(&v1.PingWebhookRequest{Id: hookA.GetId()}))).Msg.GetDelivery()

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
				if tn.GetId() == tenantA {
					t.Error("ListMyTenants: B is told it belongs to A")
				}
			}
		},
		"/assessmatrix.v1.TenantService/SwitchTenant": func() {
			_, err := b.tenants.SwitchTenant(ctx, connect.NewRequest(&v1.SwitchTenantRequest{TenantId: tenantA}))
			denied("SwitchTenant", err)
		},
		"/assessmatrix.v1.TenantService/CreateTenant": func() {
			made := get(b.tenants.CreateTenant(ctx, connect.NewRequest(&v1.CreateTenantRequest{Name: "Beta Two"}))).Msg.GetTenant()
			for _, tn := range get(a.tenants.ListMyTenants(ctx, connect.NewRequest(&v1.ListMyTenantsRequest{}))).Msg.GetTenants() {
				if tn.GetId() == made.GetId() {
					t.Error("CreateTenant: B's new tenant was handed to A")
				}
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
			_, err := b.tenants.SetAccess(ctx, connect.NewRequest(&v1.SetAccessRequest{Email: "mole@example.test", TenantId: tenantA, Role: v1.Role_ROLE_ADMIN}))
			denied("SetAccess", err)
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
	}

	// Public procedures take their own credential and are covered elsewhere:
	// a link token resolves to exactly one tenant (invite_test.go), a staff
	// email to exactly one account. Sign-out and end-session only clear a cookie.
	policy := Policy()
	for _, proc := range procedures(t) {
		if policy[proc] == auth.Public {
			continue
		}
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
	for _, table := range []string{"tenants", "staff", "exams", "people", "assignments", "attempts", "attempt_events", "outbox", "audit_log",
		"webhook_endpoints", "webhook_events", "webhook_deliveries"} {
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
