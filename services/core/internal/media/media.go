// Package media stores the images an instrument or an answer carries.
//
// Bytes live in object storage under <tenant>/<id>; Postgres holds one row
// per object, under row-level security, and that row is what decides who may
// read it. A store is pinned to one region and refuses tenants of any other:
// a tenant's media never leaves the region it was promised.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MaxBytes bounds one object. The studio downscales to 1400px WebP before
// upload, which lands far below this; the limit stops anything else.
const MaxBytes = 4 << 20

// ErrNotFound is returned by Get for a key the store does not hold.
var ErrNotFound = errors.New("media not found")

// Store is where the bytes go.
type Store interface {
	Put(ctx context.Context, key, contentType string, body []byte) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes an object for good; a key it does not hold is not an error.
	Delete(ctx context.Context, key string) error
}

// Regional is a store and the one region it serves.
type Regional struct {
	Store
	Region string
}

var keyShape = regexp.MustCompile(`^[0-9a-f-]{36}/[0-9a-f-]{36}$`)

// Key is where an object for this tenant lives. Both parts are UUIDs.
func Key(tenant, id string) (string, error) {
	k := tenant + "/" + id
	if !keyShape.MatchString(k) {
		return "", fmt.Errorf("malformed media key %q", k)
	}
	return k, nil
}

// FS keeps objects on local disk: development, and single-host self-installs.
type FS struct{ Dir string }

func (f FS) path(key string) string { return filepath.Join(f.Dir, filepath.FromSlash(key)) }

func (f FS) Put(_ context.Context, key, _ string, body []byte) error {
	p := f.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	// Write then rename, so a reader never sees half a file.
	tmp := p + ".part"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		os.Remove(tmp) // a disk that filled up mid-write keeps no half file
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (f FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	r, err := os.Open(f.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return r, err
}

func (f FS) Delete(_ context.Context, key string) error {
	err := os.Remove(f.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// S3 is any S3-compatible service: AWS, R2, MinIO, or a local provider.
type S3 struct {
	c      *minio.Client
	bucket string
}

// NewS3 connects to endpoint (host[:port]); secure selects https.
func NewS3(endpoint, accessKey, secretKey, bucket string, secure bool) (*S3, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure})
	if err != nil {
		return nil, err
	}
	return &S3{c: c, bucket: bucket}, nil
}

// Check confirms the bucket exists and the credentials can see it.
func (s *S3) Check(ctx context.Context) error {
	ok, err := s.c.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("media bucket %q: %w", s.bucket, err)
	}
	if !ok {
		return fmt.Errorf("media bucket %q does not exist", s.bucket)
	}
	return nil
}

func (s *S3) Put(ctx context.Context, key, contentType string, body []byte) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat surfaces a missing key now rather than mid-copy.
	if _, err := o.Stat(); err != nil {
		o.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

// Delete removes the object. On a versioned bucket this only hides it; the
// bucket for media must not keep versions (docs/13-operations.md).
func (s *S3) Delete(ctx context.Context, key string) error {
	return s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
