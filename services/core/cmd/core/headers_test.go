package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The camera is the runner's alone, WebAssembly may compile only where the
// face check runs, and no page may be framed or load a foreign script.
func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), true)
	get := func(path string) http.Header {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Header()
	}
	for _, path := range []string{"/", "/exams/x", "/verify/", "/take/", "/take/assets/vision-abc/x.wasm"} {
		hd := get(path)
		csp := hd.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: policy %q lacks %q", path, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe-eval'") && !strings.Contains(csp, "wasm-unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("%s: scripts too loose: %q", path, csp)
		}
		runner := strings.HasPrefix(path, "/take")
		if got := strings.Contains(csp, "wasm-unsafe-eval"); got != runner {
			t.Errorf("%s: wasm allowed = %v, want %v", path, got, runner)
		}
		if got := strings.Contains(hd.Get("Permissions-Policy"), "camera=(self)"); got != runner {
			t.Errorf("%s: camera allowed = %v, want %v", path, got, runner)
		}
		if hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("X-Frame-Options") != "DENY" || hd.Get("Strict-Transport-Security") == "" {
			t.Errorf("%s: missing a base header: %v", path, hd)
		}
	}
	if get("/assessmatrix.v1.ExamService/ListExams").Get("Content-Security-Policy") != "" {
		t.Error("an RPC answer is not a page")
	}
	w := httptest.NewRecorder()
	securityHeaders(http.NotFoundHandler(), false).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("no HSTS over plain http")
	}
}
