package main

import (
	"net/http"
	"strings"
)

// The content policies, one per app. Everything is served from our own
// origin: no CDN, no third-party script, no inline script. Styles allow
// inline because the component library injects its own and the verify page
// carries its sheet in the page.
const (
	cspBase = "default-src 'self'; base-uri 'none'; object-src 'none'; form-action 'self'; frame-ancestors 'none'; " +
		"img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; connect-src 'self' data: blob:; worker-src 'self'; manifest-src 'self'"
	// The studio and the verify page run no WebAssembly.
	cspStudio = cspBase + "; script-src 'self'"
	// The runner's camera check compiles the face detector (WebAssembly) in a
	// worker; the worker's own response carries this policy too.
	cspRunner = cspBase + "; script-src 'self' 'wasm-unsafe-eval'; media-src 'self' blob:"
)

// securityHeaders sets the browser-side locks on every response. The camera
// is allowed only to the runner, and only for a tier 2 attempt does the runner
// ask for it. hsts is on when the public origin is https.
func securityHeaders(next http.Handler, hsts bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/assessmatrix.v1."), strings.HasPrefix(p, "/credentials/"), strings.HasPrefix(p, "/issuers/"):
			// Data, not pages: nothing to render, so no page policy.
		case p == "/take" || strings.HasPrefix(p, "/take/"):
			h.Set("Content-Security-Policy", cspRunner)
			h.Set("Permissions-Policy", "camera=(self), fullscreen=(self), microphone=(), geolocation=(), payment=(), usb=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
		default:
			h.Set("Content-Security-Policy", cspStudio)
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
		}
		next.ServeHTTP(w, r)
	})
}
