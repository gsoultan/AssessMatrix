package api

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
)

// The monitor shows each sitting live: progress, last contact and flags with
// their reasons, and updates when the candidate does something.
func TestWatchExamShowsSittingsLive(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	staff := h.tenant("Proctor")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)

	st, err := staff.proctor.WatchExam(ctx, connect.NewRequest(&v1.WatchExamRequest{ExamId: f.exam.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !st.Receive() {
		t.Fatalf("no first snapshot: %v", st.Err())
	}
	first := st.Msg().GetSittings()
	if len(first) != 1 || first[0].GetStatus() != v1.AssignStatus_ASSIGN_STATUS_INVITED || first[0].GetPersonName() != "Budi" || first[0].GetTotal() != 4 {
		t.Fatalf("before starting: %+v", first)
	}

	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "1",
		Events: []*v1.AttemptEvent{
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
			{Kind: domain.KindFacesMultiple, Values: []string{"8000", "2"}},
		}})))
	for st.Receive() {
		s := st.Msg().GetSittings()[0]
		if s.GetAttemptId() != att.GetId() || s.GetAnswered() != 1 || len(s.GetFlags()) == 0 {
			continue
		}
		if s.GetFlags()[0].GetType() != domain.FlagFacesMultiple || s.GetFlags()[0].GetReason() == "" || s.GetLastSeenAt() == 0 {
			t.Fatalf("the flag arrived without its reason: %+v", s.GetFlags())
		}
		return
	}
	t.Fatalf("the monitor never showed the started sitting with its flag: %v", st.Err())
}

// A streaming procedure is guarded like any other. The auth interceptor used
// to wrap unary calls only, so a stream reached its handler unchecked and only
// the tenant lookup inside s.tx refused it. The policy is now enforced at the
// edge for streams too; this keeps both answering "no".
func TestWatchExamNeedsAStaffSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Guard")
	f := h.publishedFixture(staff)
	for name, c := range map[string]*client{"anonymous": h.browser(), "candidate": h.redeem(f)} {
		st, err := c.proctor.WatchExam(ctx, connect.NewRequest(&v1.WatchExamRequest{ExamId: f.exam.GetId()}))
		if err == nil {
			if st.Receive() {
				t.Errorf("%s: watched an exam", name)
			}
			err = st.Err()
			st.Close()
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("%s: want unauthenticated, got %v", name, err)
		}
	}
}
