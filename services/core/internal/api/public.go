package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Public, unauthenticated reads that make a credential checkable by anyone:
// the credential itself and its issuer's profile and public keys. Each goes
// through a security-definer function that takes an id and returns only what
// is already public, so no tenant scope is needed or given.

// PublicHandler serves /credentials/{id}, /issuers/{id} and /issuers/{id}/jwks.
func (s *Server) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /credentials/{id}", s.publicCredential)
	mux.HandleFunc("GET /issuers/{id}", s.publicIssuer)
	mux.HandleFunc("GET /issuers/{id}/jwks", s.publicIssuer)
	return mux
}

func publicJSON(w http.ResponseWriter, maxAge time.Duration, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	// Verifiers run anywhere; nothing here depends on who is asking.
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(maxAge.Seconds())))
	h.Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) publicCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validUUID(id) {
		http.NotFound(w, r)
		return
	}
	var jwt string
	var revoked *time.Time
	var why *string
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT jwt, revoked_at, revoked_why FROM am_public_credential($1)`, id).Scan(&jwt, &revoked, &why)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.ErrorContext(r.Context(), "public credential", "err", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	out := map[string]any{"jwt": jwt, "revoked": revoked != nil}
	if revoked != nil {
		out["revokedAt"] = revoked.UTC().Format(time.RFC3339)
		if why != nil {
			out["revokedReason"] = *why
		}
	}
	// Short: a revocation must reach verifiers soon.
	publicJSON(w, time.Minute, out)
}

func (s *Server) publicIssuer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validUUID(id) {
		http.NotFound(w, r)
		return
	}
	var name string
	var keys json.RawMessage
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT name, keys FROM am_public_issuer($1)`, id).Scan(&name, &keys)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.ErrorContext(r.Context(), "public issuer", "err", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/jwks") {
		publicJSON(w, 5*time.Minute, map[string]any{"keys": keys})
		return
	}
	publicJSON(w, 5*time.Minute, map[string]any{
		"@context": []string{credContextVC, credContextOB},
		"id":       s.IssuerURL(id),
		"type":     []string{"Profile"},
		"name":     name,
	})
}
