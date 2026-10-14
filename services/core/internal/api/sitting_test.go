package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

func (h *harness) reassign(staff *client, f *fixture, version int32) []*v1.Assignment {
	h.t.Helper()
	return get(staff.assign.AssignPeople(context.Background(), connect.NewRequest(&v1.AssignPeopleRequest{
		ExamId: f.exam.GetId(), PersonIds: []string{f.person.GetId()}, ExamVersion: version,
	}))).Msg.GetAssignments()
}

// A person sits an instrument again only once the last sitting is closed, and
// the new sitting never touches the old one's attempt or score.
func TestSecondSittingOnlyAfterTheFirstIsHandedIn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Sitting")
	f := h.publishedFixture(staff)
	if f.assign.GetSitting() != 1 {
		t.Fatalf("a first assignment is sitting 1, got %d", f.assign.GetSitting())
	}
	if got := h.reassign(staff, f, 0); len(got) != 0 {
		t.Fatal("while a sitting is open, assigning again must be a no-op")
	}

	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	if got := h.reassign(staff, f, 0); len(got) != 0 {
		t.Fatal("while an attempt is in progress, assigning again must be a no-op")
	}
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b1",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}}}})))
	if sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()}))); !sub.Msg.GetOk() {
		t.Fatal("the first sitting did not hand in")
	}

	again := h.reassign(staff, f, 0)
	if len(again) != 1 || again[0].GetSitting() != 2 || again[0].GetId() == f.assign.GetId() {
		t.Fatalf("after handing in, assigning again must add sitting 2, got %+v", again)
	}
	if again[0].GetLinkState() != v1.LinkState_LINK_STATE_LIVE {
		t.Fatalf("the new sitting must be mailed its own link, got %v", again[0].GetLinkState())
	}
	if more := h.reassign(staff, f, 0); len(more) != 0 {
		t.Fatal("sitting 2 is open, so a third must not be added")
	}

	cand2 := h.redeem(f)
	att2 := get(cand2.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	if att2.GetId() == att.GetId() {
		t.Fatal("a new sitting must start a new attempt, not reopen the handed-in one")
	}

	list := get(staff.assign.ListAssignments(ctx, connect.NewRequest(&v1.ListAssignmentsRequest{ExamId: f.exam.GetId()}))).Msg.GetAssignments()
	if len(list) != 2 {
		t.Fatalf("want both sittings listed, got %d", len(list))
	}
	for _, a := range list {
		if a.GetId() == f.assign.GetId() && (a.GetStatus() != v1.AssignStatus_ASSIGN_STATUS_SUBMITTED || a.GetSubmittedAt() == 0) {
			t.Fatalf("the first sitting must stay handed in, got %+v", a)
		}
	}
}

func TestSecondSittingAfterTheLinkRanOut(t *testing.T) {
	h := newHarness(t)
	staff := h.tenant("Lapsed")
	f := h.publishedFixture(staff)
	h.clock.add(LinkTTL)
	again := h.reassign(staff, f, 0)
	if len(again) != 1 || again[0].GetSitting() != 2 {
		t.Fatalf("an unused link that ran out closes the sitting, got %+v", again)
	}
}

func TestSittingPicksAPublishedVersion(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Versions")
	f := h.publishedFixture(staff)
	// Publish a v2 with a different first question.
	e := sampleExam(f.exam)
	e.Sections[0].Items[0].Stem = "Pick b, again"
	e = get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_NEW_VERSION}))).Msg.GetExam()
	e = get(staff.exams.SetExamStatus(ctx, connect.NewRequest(&v1.SetExamStatusRequest{Id: e.GetId(), Status: v1.Status_STATUS_PUBLISHED}))).Msg.GetExam()
	if e.GetPublishedVersion() != 2 {
		t.Fatalf("want v2 live, got v%d", e.GetPublishedVersion())
	}
	f.exam = e

	p := get(staff.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Email: "v1@example.test"}}))).Msg.GetPerson()
	pick := func(version int32) ([]*v1.Assignment, error) {
		r, err := staff.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{
			ExamId: e.GetId(), PersonIds: []string{p.GetId()}, ExamVersion: version,
		}))
		if err != nil {
			return nil, err
		}
		return r.Msg.GetAssignments(), nil
	}
	if _, err := pick(3); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("an unpublished version must be refused, got %v", err)
	}
	got, err := pick(1)
	if err != nil || len(got) != 1 || got[0].GetExamVersion() != 1 {
		t.Fatalf("asking for v1 must pin v1, got %+v, %v", got, err)
	}
	if def := h.reassign(staff, f, 0); len(def) != 0 {
		t.Fatal("the fixture's person still has an open sitting")
	}
}
