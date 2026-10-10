package api

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

// docs/08 §4: "a test that a revoked or replaced link cannot start or submit".
func TestRevokedLinkCannotStartOrSubmit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Revoke")
	f := h.publishedFixture(staff)
	token := h.lastLink(f.email)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	get(staff.assign.RevokeInvite(ctx, connect.NewRequest(&v1.RevokeInviteRequest{AssignmentId: f.assign.GetId()})))

	if _, err := cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents})); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("a revoked session must not start, got %v", err)
	}
	if _, err := cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("a revoked session must not submit, got %v", err)
	}
	open := get(h.browser().candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: token})))
	if open.Msg.GetOk() || open.Msg.GetRefusal() != v1.Refusal_REFUSAL_REVOKED {
		t.Fatalf("a revoked link must say so, got %v", open.Msg.GetRefusal())
	}
}

func TestResendRetiresTheOldLinkAndSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Resend")
	f := h.publishedFixture(staff)
	old := h.lastLink(f.email)
	cand := h.redeem(f)
	h.clock.add(2 * time.Minute)

	get(staff.assign.SendInvites(ctx, connect.NewRequest(&v1.SendInvitesRequest{AssignmentIds: []string{f.assign.GetId()}})))
	if _, err := cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents})); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("resending must end the old session, got %v", err)
	}
	open := get(h.browser().candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: old})))
	if open.Msg.GetRefusal() != v1.Refusal_REFUSAL_SUPERSEDED {
		t.Fatalf("an old link must read as replaced, got %v", open.Msg.GetRefusal())
	}
	if fresh := h.lastLink(f.email); fresh == old {
		t.Fatal("resending must mint a new link")
	} else if r := get(h.browser().candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: fresh}))); !r.Msg.GetOk() {
		t.Fatalf("the new link must open, got %v", r.Msg.GetRefusal())
	}
}

// docs/08 §4: "a lock test (25 wrong guesses)". Five codes of five tries.
func TestTwentyFiveWrongGuessesLockTheLink(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Lock")
	f := h.publishedFixture(staff)
	token := h.lastLink(f.email)
	c := h.browser()
	guesses := 0
	for round := 0; round < OTPMaxSends; round++ {
		sent := get(c.candidate.RequestCode(ctx, connect.NewRequest(&v1.RequestCodeRequest{Token: token})))
		if !sent.Msg.GetOk() {
			t.Fatalf("round %d: code refused: %v", round, sent.Msg.GetRefusal())
		}
		real := h.lastCode(f.email)
		wrong := "000000"
		if real == wrong {
			wrong = "111111"
		}
		for try := 0; try < OTPTries; try++ {
			v := get(c.candidate.VerifyCode(ctx, connect.NewRequest(&v1.VerifyCodeRequest{Token: token, Code: wrong})))
			guesses++
			if v.Msg.GetOk() {
				t.Fatal("a wrong code must never verify")
			}
		}
		h.clock.add(OTPCooldown)
	}
	if guesses != 25 {
		t.Fatalf("made %d guesses", guesses)
	}
	open := get(c.candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: token})))
	if open.Msg.GetRefusal() != v1.Refusal_REFUSAL_LOCKED {
		t.Fatalf("after 25 wrong guesses the link must be locked, got %v", open.Msg.GetRefusal())
	}
	if r := get(c.candidate.RequestCode(ctx, connect.NewRequest(&v1.RequestCodeRequest{Token: token}))); r.Msg.GetOk() {
		t.Fatal("a locked link must not send another code")
	}
}

func TestUnknownLinkSaysNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	c := h.browser()
	for _, tok := range []string{"", "short", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		r := get(c.candidate.OpenLink(ctx, connect.NewRequest(&v1.OpenLinkRequest{Token: tok})))
		if r.Msg.GetOk() || r.Msg.GetRefusal() != v1.Refusal_REFUSAL_UNKNOWN || r.Msg.GetMaskedEmail() != "" {
			t.Fatalf("token %q: an unknown link must reveal nothing, got %+v", tok, r.Msg)
		}
	}
}

func TestDraftHoldsInvitations(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Draft")
	e := get(staff.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_EXAM}))).Msg.GetExam()
	p := get(staff.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Email: "d@example.test"}}))).Msg.GetPerson()
	res := get(staff.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: e.GetId(), PersonIds: []string{p.GetId()}})))
	if res.Msg.GetSent() != 0 || res.Msg.GetAssignments()[0].GetLinkState() != v1.LinkState_LINK_STATE_UNSENT {
		t.Fatal("an invitation to a draft is held, not mailed")
	}
	again := get(staff.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: e.GetId(), PersonIds: []string{p.GetId()}})))
	if len(again.Msg.GetAssignments()) != 0 {
		t.Fatal("re-assigning someone is a no-op")
	}
}
