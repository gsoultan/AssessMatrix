package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A server stream (the live monitor) needs every message flushed as it is
// sent. The request logger wraps the ResponseWriter, and once hid http.Flusher,
// so connect refused every stream with an internal error. The first message
// must reach the client while the handler is still running.
func TestRequestLoggerKeepsStreamsFlowing(t *testing.T) {
	release := make(chan struct{})
	h := logRequests(slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("the logged ResponseWriter must still be an http.Flusher")
		}
		_, _ = w.Write([]byte("first"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through the logger: %v", err)
		}
		<-release
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	defer close(release)

	// Without a flush the headers never leave either, so even the response
	// would not arrive: everything below is bounded by one timeout.
	got := make(chan string, 1)
	go func() {
		res, err := http.Get(srv.URL)
		if err != nil {
			got <- err.Error()
			return
		}
		defer res.Body.Close()
		b := make([]byte, 5)
		_, err = io.ReadFull(res.Body, b)
		if err != nil {
			got <- err.Error()
			return
		}
		got <- string(b)
	}()
	select {
	case s := <-got:
		if s != "first" {
			t.Fatalf("read %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first message did not arrive while the stream was open")
	}
}
