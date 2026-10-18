package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/storm"
	"github.com/gsoultan/storm/runtime"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/tenant"
)

// The API speaks ids as text, as its protos do; storm speaks them as the 16
// bytes PostgreSQL stores. These are the one crossing between the two.

// key parses an id from a request or a principal. Text that is not a UUID
// becomes the nil UUID, which no row has, so a malformed id reads as absent.
// Handlers that answer NotFound for a bad id check validUUID first.
func key(s string) [16]byte {
	u, err := storm.ParseUUID(s)
	if err != nil {
		return [16]byte{}
	}
	return u
}

// keys parses a list of ids the way key parses one.
func keys(ss []string) [][16]byte {
	out := make([][16]byte, len(ss))
	for i, s := range ss {
		out[i] = key(s)
	}
	return out
}

// idText is a stored id as the API writes it.
func idText(u [16]byte) string { return storm.UUID(u).String() }

// opt is a nullable column as a pointer: nil for NULL.
func opt[T any](n runtime.Null[T]) *T {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

// optTime is opt for a nullable timestamp read as a time.
func optTime(n runtime.Null[time.Time]) *time.Time { return opt(n) }

// noRow reports a read or a keyed write that found no row: absent, or
// invisible under row-level security, which to this tenant is the same.
func noRow(err error) bool { return errors.Is(err, runtime.ErrNoRow) }

// newID is a fresh random id, for a row whose id must be known before it is
// written (it seals a secret, or names an event in its own body).
func newID() [16]byte { return uuid.New() }

// some is a present value for a nullable column.
func some[T any](v T) runtime.Null[T] { return runtime.Null[T]{V: v, Valid: true} }

// lockTenant serialises one kind of change within the tenant tx is scoped
// to, by locking its row until tx ends.
func lockTenant(ctx context.Context, tx *db.Tx) error {
	_, ok, err := tenant.New().IDEq(tx.Tenant()).ForUpdate().One(ctx, tx)
	if err == nil && !ok {
		err = notFound("tenant")
	}
	return err
}
