// Package storage is the object-store backend for raw scrape payloads (P2-04).
//
// MinIO in every environment (DEVELOPMENT_RULE: no managed services in the
// MVP). The bucket is created on first use; keys are caller-owned and are
// stored verbatim in raw_payload.s3_key.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinIO implements port.RawStorage over the MinIO S3 API.
type MinIO struct {
	client *minio.Client
	bucket string
}

// Config connects to a MinIO endpoint. The bucket is made on demand.
type Config struct {
	Endpoint   string
	AccessKey  string
	SecretKey  string
	Bucket     string
	UseSSL     bool
	MakeBucket bool
}

// New validates config and pings the endpoint. Bucket creation is deferred to
// first Put unless MakeBucket is set (local dev creates it eagerly so the
// developer sees the bucket in the MinIO console immediately).
func New(ctx context.Context, cfg Config) (*MinIO, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("storage: endpoint is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("storage: credentials are required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("storage: bucket is required")
	}
	cli, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: minio client: %w", err)
	}
	m := &MinIO{client: cli, bucket: cfg.Bucket}
	if cfg.MakeBucket {
		if err := m.ensureBucket(ctx); err != nil {
			return nil, err
		}
	}
	return m, nil
}

var _ interface {
	Put(context.Context, string, []byte) (int64, error)
	Get(context.Context, string) ([]byte, error)
} = (*MinIO)(nil)

// ensureBucket creates the bucket when absent. Idempotent: an existing bucket
// is not an error (BucketAlreadyOwnedByYou / BucketAlreadyExists).
func (m *MinIO) ensureBucket(ctx context.Context) error {
	exists, err := m.client.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("storage: check bucket: %w", err)
	}
	if exists {
		return nil
	}
	if err := m.client.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("storage: make bucket %s: %w", m.bucket, err)
	}
	return nil
}

// Put stores one payload and returns the byte count. The bucket is created
// lazily so the first scrape after a fresh MinIO still succeeds.
func (m *MinIO) Put(ctx context.Context, key string, payload []byte) (int64, error) {
	if err := m.ensureBucket(ctx); err != nil {
		return 0, err
	}
	n, err := m.client.PutObject(ctx, m.bucket, key, bytesReader(payload), int64(len(payload)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return 0, fmt.Errorf("storage: put %s: %w", key, err)
	}
	return n.Size, nil
}

// Get reads a payload back. A missing key is a caller-visible error (the
// ingestor treats it as a lost payload, not a crash).
func (m *MinIO) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("storage: read %s: %w", key, err)
	}
	return data, nil
}

// bytesReader returns an io.Reader over a byte slice without dragging
// bytes.Buffer into the call site.
func bytesReader(p []byte) io.Reader { return &sliceReader{p: p} }

type sliceReader struct {
	p []byte
	i int
}

func (r *sliceReader) Read(b []byte) (int, error) {
	if r.i >= len(r.p) {
		return 0, io.EOF
	}
	n := copy(b, r.p[r.i:])
	r.i += n
	return n, nil
}
