package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus/testutil"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/metrics"
)

// rpcCount reads am_rpc_requests_total for one procedure and code.
func rpcCount(t *testing.T, procedure, code string) float64 {
	t.Helper()
	fams, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if f.GetName() != "am_rpc_requests_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			l := map[string]string{}
			for _, p := range m.GetLabel() {
				l[p.GetName()] = p.GetValue()
			}
			if l["procedure"] == procedure && l["code"] == code {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// The numbers on call say what the candidates' work is doing: events stored,
// signals dropped for want of consent, refusals by code, and the sync lag.
func TestMetricsCountTheWork(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Metrics")
	f := h.publishedFixture(staff)
	e := get(staff.exams.GetExam(ctx, connect.NewRequest(&v1.GetExamRequest{Id: f.exam.GetId()}))).Msg.GetExam()
	e.Tier = 1
	get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_IN_PLACE, ExpectedUpdatedAt: e.GetUpdatedAt()})))
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()

	stored, dropped := testutil.ToFloat64(metrics.EventsStored), testutil.ToFloat64(metrics.SignalsDropped)
	lags := histCount(t)
	okBefore := rpcCount(t, "CandidateService/AppendEvents", "ok")
	req := &v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "m1", Events: []*v1.AttemptEvent{
		{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}, ClientTime: h.clock.now().Add(-3e9).UnixMilli()},
		{Kind: domain.KindFaceMissing, Values: []string{"40000"}},
	}}
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(req)))
	// A retry of the same batch stores nothing new.
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(req)))

	if d := testutil.ToFloat64(metrics.EventsStored) - stored; d != 1 {
		t.Errorf("events stored went up by %v, want 1 (the answer; the retry adds none)", d)
	}
	if d := testutil.ToFloat64(metrics.SignalsDropped) - dropped; d != 1 {
		t.Errorf("signals dropped went up by %v, want 1 (the unconsented camera event)", d)
	}
	if d := rpcCount(t, "CandidateService/AppendEvents", "ok") - okBefore; d != 2 {
		t.Errorf("AppendEvents ok went up by %v, want 2", d)
	}

	// Only the answer had a client time; the dropped signal is not timed.
	if d := histCount(t) - lags; d != 1 {
		t.Errorf("sync lag samples went up by %v, want 1", d)
	}

	refused := rpcCount(t, "ExamService/ListExams", "unauthenticated")
	if _, err := h.browser().exams.ListExams(ctx, connect.NewRequest(&v1.ListExamsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("want unauthenticated, got %v", err)
	}
	if d := rpcCount(t, "ExamService/ListExams", "unauthenticated") - refused; d != 1 {
		t.Errorf("a refusal by the policy must be counted, went up by %v", d)
	}
}

func histCount(t *testing.T) uint64 {
	t.Helper()
	fams, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if f.GetName() == "am_sync_lag_seconds" {
			return f.GetMetric()[0].GetHistogram().GetSampleCount()
		}
	}
	return 0
}
