package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	mediarow "github.com/gsoultan/assessmatrix/services/core/internal/store/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/tenant"

	"connectrpc.com/connect"

	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
)

// MediaPrefix is where images are uploaded and served. Item and answer media
// reference them as MediaPrefix + id, on the same origin as everything else,
// so the session cookie is what lets a browser read one.
const MediaPrefix = "/media/"

// allowed is what an upload may be, judged from its bytes, not its label.
var allowed = map[string]bool{"image/webp": true, "image/jpeg": true, "image/png": true}

// MediaHandler serves POST /media (upload) and GET /media/{id} (read). Both
// need a staff or candidate session, and both are scoped to its tenant by
// row-level security: an id from another tenant is simply not found.
func (s *Server) MediaHandler() http.Handler {
	s.limits()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /media", s.uploadMedia)
	mux.HandleFunc("GET /media/{id}", s.serveMedia)
	return mux
}

func (s *Server) principal(w http.ResponseWriter, r *http.Request) *auth.Principal {
	p, err := s.Auth.Either(r.Context(), r.Header)
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			http.Error(w, "sign in required", http.StatusUnauthorized)
		} else {
			s.Log.ErrorContext(r.Context(), "media auth", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return nil
	}
	return p
}

func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if s.Media.Store == nil {
		http.Error(w, "image storage is not configured", http.StatusServiceUnavailable)
		return
	}
	p := s.principal(w, r)
	if p == nil {
		return
	}
	if s.ipLimit != nil && !s.ipLimit.Allow("media:"+p.StaffID+p.AssignmentID) {
		http.Error(w, "too many uploads; wait a minute", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, media.MaxBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf("an image may be at most %d MB", media.MaxBytes>>20), http.StatusRequestEntityTooLarge)
		return
	}
	ctype := http.DetectContentType(body)
	if len(body) == 0 || !allowed[ctype] {
		http.Error(w, "only WebP, JPEG or PNG images can be uploaded", http.StatusUnsupportedMediaType)
		return
	}
	by := "staff:" + p.StaffID
	if p.StaffID == "" {
		by = "assignment:" + p.AssignmentID
	}
	sum := sha256.Sum256(body)
	ctx := auth.With(r.Context(), p)
	var id string
	err = s.tx(ctx, func(tx *db.Tx) error {
		ten, ok, err := tenant.New().IDEq(tx.Tenant()).One(ctx, tx)
		if err != nil {
			return err
		}
		if !ok || ten.Region != s.Media.Region {
			return errResidency
		}
		n := mediarow.Create()
		n.SetTenantID(tx.Tenant())
		n.SetContentType(ctype)
		n.SetBytes(int32(len(body)))
		n.SetSha256(sum[:])
		n.SetUploadedBy(by)
		row, err := n.Insert(ctx, tx)
		if err != nil {
			return err
		}
		id = idText(row.ID)
		key, err := media.Key(string(p.Tenant), id)
		if err != nil {
			return err
		}
		// Stored before the row commits: a failed store leaves no row pointing at nothing.
		return s.Media.Put(ctx, key, ctype, body)
	})
	if errors.Is(err, errResidency) {
		http.Error(w, "this organisation's images must be stored in its own region, which this server does not serve", http.StatusConflict)
		return
	}
	if err != nil {
		s.Log.ErrorContext(ctx, "media upload", "err", err)
		http.Error(w, "could not store the image", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": id, "url": MediaPrefix + id, "bytes": len(body)})
}

var errResidency = errors.New("tenant region not served by this media store")

func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validUUID(id) || s.Media.Store == nil {
		http.NotFound(w, r)
		return
	}
	p := s.principal(w, r)
	if p == nil {
		return
	}
	ctx := auth.With(r.Context(), p)
	var ctype string
	var found bool
	err := s.tx(ctx, func(tx *db.Tx) error {
		m, ok, err := mediarow.New().IDEq(key(id)).One(ctx, tx)
		ctype, found = m.ContentType, ok
		return err
	})
	if err == nil && !found {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.ErrorContext(ctx, "media lookup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	key, err := media.Key(string(p.Tenant), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := s.Media.Get(ctx, key)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.ErrorContext(ctx, "media read", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer body.Close()
	h := w.Header()
	h.Set("Content-Type", ctype)
	// An id never changes what it points at. Private: it needs a session.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'")
	io.Copy(w, body)
}
