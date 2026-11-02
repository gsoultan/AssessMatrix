package api

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// The server went down twenty minutes into a 30-minute exam and came back
// 25 minutes later. Before AddTime nothing could give that time back: the
// deadline was fixed at the start, so every answer after it was dropped and
// the sitting could only be force-submitted. Staff now add the minutes, the
// candidate's later answers count, and the log says who gave it and why.
func TestAddTimeGivesBackAnOutage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Outage")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	deadline := time.UnixMilli(att.GetDeadlineAt())

	// Back up 15 minutes after the deadline, beyond the sync grace.
	h.clock.add(time.Until(deadline) + 15*time.Minute)
	const reason = "Server outage from 10:20 to 10:45"
	res := get(staff.proctor.AddTime(ctx, connect.NewRequest(&v1.AddTimeRequest{ExamId: f.exam.GetId(), Minutes: 25, Reason: reason})))
	added := res.Msg.GetAdded()
	want := deadline.Add(25 * time.Minute).UnixMilli()
	if len(added) != 1 || added[0].GetAssignmentId() != f.assign.GetId() || added[0].GetDeadlineAt() != want {
		t.Fatalf("added = %+v, want the one sitting with deadline %d", added, want)
	}

	// The runner learns the new deadline on its next save, and an answer made
	// now, inside the added time, counts.
	ack := get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "after",
		Events: []*v1.AttemptEvent{{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}, ClientTime: h.clock.now().UnixMilli()}}})))
	if ack.Msg.GetDeadlineAt() != want {
		t.Fatalf("AppendEvents told the runner deadline %d, want %d", ack.Msg.GetDeadlineAt(), want)
	}
	// Not expired any more, so a forced submit still needs the required answers;
	// q1 is answered, so an ordinary hand-in goes through.
	sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()})))
	if !sub.Msg.GetOk() {
		t.Fatalf("hand-in inside the added time refused: missing %v", sub.Msg.GetMissingItemIds())
	}
	if _, ok := sub.Msg.GetAttempt().GetAnswers()["q1"]; !ok || sub.Msg.GetAttempt().GetScore().GetRaw() != 2 {
		t.Fatalf("the answer made in the added time was lost: %+v", sub.Msg.GetAttempt().GetAnswers())
	}

	// Staff see who gave the time; the record is still intact.
	log := get(staff.results.GetAttemptLog(ctx, connect.NewRequest(&v1.GetAttemptLogRequest{AttemptId: att.GetId()}))).Msg
	if !log.GetChainIntact() {
		t.Fatal("adding time broke the attempt's seal")
	}
	ev := findKind(log.GetEvents(), domain.KindTimeAdded)
	if ev == nil || ev.GetDetail() != reason || ev.GetText() == "" || ev.GetValues()[0] != "25" || ev.GetValues()[1] != strconv.FormatInt(want, 10) {
		t.Fatalf("staff log entry = %+v", ev)
	}
	// The candidate sees how much and why, not which staff member.
	rec := get(cand.candidate.GetRecord(ctx, connect.NewRequest(&v1.GetRecordRequest{}))).Msg
	if ev := findKind(rec.GetAttempt().GetEvents(), domain.KindTimeAdded); ev == nil || ev.GetDetail() != reason || ev.GetText() != "" {
		t.Fatalf("candidate record entry = %+v", ev)
	}

	// A sitting already handed in is left alone.
	again := get(staff.proctor.AddTime(ctx, connect.NewRequest(&v1.AddTimeRequest{ExamId: f.exam.GetId(), Minutes: 5, Reason: reason})))
	if len(again.Msg.GetAdded()) != 0 {
		t.Fatalf("time was added to a handed-in sitting: %+v", again.Msg.GetAdded())
	}
}

// Naming sittings extends only those; everyone else keeps their deadline.
func TestAddTimeToNamedSittingsOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Accommodation")
	f := h.publishedFixture(staff)
	first := h.redeem(f)
	a1 := get(first.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	email := "second@example.test"
	p := get(staff.people.AddPerson(ctx, connect.NewRequest(&v1.AddPersonRequest{Person: &v1.PersonDraft{Name: "Sari", Email: email}}))).Msg.GetPerson()
	as := get(staff.assign.AssignPeople(ctx, connect.NewRequest(&v1.AssignPeopleRequest{ExamId: f.exam.GetId(), PersonIds: []string{p.GetId()}}))).Msg.GetAssignments()[0]
	second := h.redeem(&fixture{exam: f.exam, person: p, assign: as, email: email})
	a2 := get(second.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	res := get(staff.proctor.AddTime(ctx, connect.NewRequest(&v1.AddTimeRequest{ExamId: f.exam.GetId(),
		AssignmentIds: []string{as.GetId()}, Minutes: 10, Reason: "Agreed extra time"})))
	if len(res.Msg.GetAdded()) != 1 || res.Msg.GetAdded()[0].GetAssignmentId() != as.GetId() {
		t.Fatalf("added = %+v, want only the named sitting", res.Msg.GetAdded())
	}
	s1 := get(first.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg.GetAttempt()
	s2 := get(second.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg.GetAttempt()
	if s1.GetDeadlineAt() != a1.GetDeadlineAt() {
		t.Error("a sitting that was not named was given time")
	}
	if s2.GetDeadlineAt() != a2.GetDeadlineAt()+(10*time.Minute).Milliseconds() {
		t.Errorf("deadline %d, want %d", s2.GetDeadlineAt(), a2.GetDeadlineAt()+(10*time.Minute).Milliseconds())
	}

	// A sitting of another exam cannot be named under this one.
	other := h.publishedFixture(staff)
	_, err := staff.proctor.AddTime(ctx, connect.NewRequest(&v1.AddTimeRequest{ExamId: f.exam.GetId(),
		AssignmentIds: []string{other.assign.GetId()}, Minutes: 10, Reason: "x"}))
	if code(err) != connect.CodeNotFound {
		t.Errorf("another exam's sitting: want not found, got %v", err)
	}
}

func TestAddTimeIsBoundedAndStaffOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Bounds")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	for name, req := range map[string]*v1.AddTimeRequest{
		"no minutes":   {ExamId: f.exam.GetId(), Reason: "x"},
		"too many":     {ExamId: f.exam.GetId(), Minutes: MaxAddMinutes + 1, Reason: "x"},
		"no reason":    {ExamId: f.exam.GetId(), Minutes: 5, Reason: "  "},
		"long reason":  {ExamId: f.exam.GetId(), Minutes: 5, Reason: strings.Repeat("a", MaxDetailLen+1)},
		"negative add": {ExamId: f.exam.GetId(), Minutes: -30, Reason: "x"},
	} {
		if _, err := staff.proctor.AddTime(ctx, connect.NewRequest(req)); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want invalid argument, got %v", name, err)
		}
	}
	// A candidate can neither call it nor write the event themselves.
	if _, err := cand.proctor.AddTime(ctx, connect.NewRequest(&v1.AddTimeRequest{ExamId: f.exam.GetId(), Minutes: 60, Reason: "x"})); code(err) != connect.CodeUnauthenticated {
		t.Errorf("candidate called AddTime: %v", err)
	}
	_, err := cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "forge",
		Events: []*v1.AttemptEvent{{Kind: domain.KindTimeAdded, Values: []string{"60", "0"}}}}))
	if code(err) != connect.CodeInvalidArgument {
		t.Errorf("candidate wrote %s: %v", domain.KindTimeAdded, err)
	}
	if s := get(cand.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg.GetAttempt(); s.GetDeadlineAt() != att.GetDeadlineAt() {
		t.Error("the deadline moved")
	}
}

func findKind(evs []*v1.AttemptEvent, kind string) *v1.AttemptEvent {
	for _, ev := range evs {
		if ev.GetKind() == kind {
			return ev
		}
	}
	return nil
}
