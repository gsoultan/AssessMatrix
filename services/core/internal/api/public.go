package api

import (
	"encoding/base64"
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
	mux.HandleFunc("GET /credentials/{id}/status", s.publicCredentialStatus)
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

// publicCredentialStatus is the revocation list a credential's
// credentialStatus points at, so a wallet or another verifier holding only
// the signed credential can learn it was revoked. One credential per list:
// nothing about an issuer's other credentials is disclosed.
func (s *Server) publicCredentialStatus(w http.ResponseWriter, r *http.Request) {
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
		s.Log.ErrorContext(r.Context(), "public credential status", "err", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if parts := strings.Split(jwt, "."); len(parts) == 3 {
		if body, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			_ = json.Unmarshal(body, &claims)
		}
	}
	revokedList := []map[string]any{}
	if revoked != nil {
		entry := map[string]any{"id": "urn:uuid:" + id}
		if why != nil {
			entry["revocationReason"] = *why
		}
		revokedList = append(revokedList, entry)
	}
	publicJSON(w, time.Minute, map[string]any{
		"id": s.CredentialStatusURL(id), "issuer": claims.Iss, "revokedCredentials": revokedList,
	})
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
