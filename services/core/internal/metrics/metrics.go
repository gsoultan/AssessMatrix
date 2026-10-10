// Package metrics is what the core reports to Prometheus: rate, errors and
// duration per procedure, and the few numbers that say whether candidates'
// work is reaching the server. Labels are procedure names and codes only:
// never a tenant, a person or an attempt, so the series stay few and carry no
// personal data.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Registry holds every series the core exports.
	Registry = prometheus.NewRegistry()

	rpcTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "am_rpc_requests_total", Help: "Procedure calls by outcome (a connect code, or ok).",
	}, []string{"procedure", "code"})
	rpcSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "am_rpc_duration_seconds", Help: "Time to answer a procedure call; for a stream, its whole life.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"procedure"})

	// EventsStored counts candidate events written to attempt logs.
	EventsStored = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "am_events_stored_total", Help: "Candidate events sealed into attempt logs.",
	})
	// SignalsDropped counts integrity signals refused for want of consent.
	SignalsDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "am_signals_dropped_total", Help: "Integrity signals dropped because their consent was missing.",
	})
	// RecordsPurged counts attempt records deleted, by why (retention or erasure).
	RecordsPurged = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "am_records_purged_total", Help: "Attempt records deleted: at the end of retention, or on an erasure request.",
	}, []string{"why"})
	// SyncLag is how long a candidate's event took to reach the server.
	SyncLag = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "am_sync_lag_seconds",
		Help:    "Server receipt time minus the event's client time, for new events. Includes clock skew; offline stretches show as the long tail.",
		Buckets: []float64{.25, .5, 1, 2, 5, 10, 30, 60, 300, 1800, 7200},
	})
)

func init() {
	Registry.MustRegister(rpcTotal, rpcSeconds, EventsStored, SignalsDropped, SyncLag, RecordsPurged,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// Lag records an event's trip from the device, never below zero.
func Lag(clientMs int64, now time.Time) {
	if clientMs <= 0 {
		return
	}
	SyncLag.Observe(max(0, now.Sub(time.UnixMilli(clientMs)).Seconds()))
}

// Handler serves the registry in the Prometheus text format.
func Handler() http.Handler { return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{}) }

// Interceptor counts and times every procedure, unary and streaming.
func Interceptor() connect.Interceptor { return interceptor{} }

type interceptor struct{}

// name turns /assessmatrix.v1.CandidateService/AppendEvents into CandidateService/AppendEvents.
func name(procedure string) string {
	return strings.TrimPrefix(strings.TrimPrefix(procedure, "/"), "assessmatrix.v1.")
}

func observe(procedure string, start time.Time, err error) {
	code := "ok"
	if err != nil {
		code = connect.CodeUnknown.String()
		var ce *connect.Error
		if errors.As(err, &ce) {
			code = ce.Code().String()
		}
	}
	p := name(procedure)
	rpcTotal.WithLabelValues(p, code).Inc()
	rpcSeconds.WithLabelValues(p).Observe(time.Since(start).Seconds())
}

func (interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		res, err := next(ctx, req)
		observe(req.Spec().Procedure, start, err)
		return res, err
	}
}

func (interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		start := time.Now()
		err := next(ctx, conn)
		observe(conn.Spec().Procedure, start, err)
		return err
	}
}
