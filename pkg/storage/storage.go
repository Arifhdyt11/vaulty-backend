// Package storage: object storage untuk file asli, thumbnail, audio, dan ekspor (ADR-004).
// Implementasi S3-compatible (R2/MinIO) untuk production dan disk lokal untuk development.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var ErrNotFound = errors.New("objek tidak ditemukan")

type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)
	Delete(ctx context.Context, key string) error
	// URL mengembalikan URL yang bisa dibuka client selama ttl (signed) atau URL publik.
	URL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ---- S3 ----

type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey, PublicURL string
	UseSSL                                                    bool
}

type S3 struct {
	c      *minio.Client
	bucket string
	public string
}

func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	ok, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3 bucket: %w", err)
	}
	if !ok {
		if err := c.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("s3 buat bucket: %w", err)
		}
	}
	return &S3{c: c, bucket: cfg.Bucket, public: strings.TrimRight(cfg.PublicURL, "/")}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	obj, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("s3 get %s: %w", key, err)
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("s3 stat %s: %w", key, err)
	}
	return obj, st.ContentType, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3) URL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s.public != "" {
		return s.public + "/" + key, nil
	}
	u, err := s.c.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("s3 presign %s: %w", key, err)
	}
	return u.String(), nil
}

// ---- Local ----

// Local menyimpan di disk; URL menunjuk ke endpoint API yang menyajikan file (diisi oleh router).
type Local struct {
	dir     string
	baseURL string // mis. http://localhost:8080/api/v1/files
	signer  func(key string, exp time.Time) string
}

func NewLocal(dir, baseURL string, signer func(key string, exp time.Time) string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage dir: %w", err)
	}
	return &Local{dir: dir, baseURL: strings.TrimRight(baseURL, "/"), signer: signer}, nil
}

func (l *Local) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("key tidak valid")
	}
	return filepath.Join(l.dir, clean), nil
}

func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	// Content-Type disimpan di sidecar supaya Get bisa mengembalikannya.
	return os.WriteFile(p+".ct", []byte(contentType), 0o644)
}

func (l *Local) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	ct, _ := os.ReadFile(p + ".ct")
	return f, string(ct), nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	_ = os.Remove(p + ".ct")
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) URL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	exp := time.Now().Add(ttl)
	q := url.Values{}
	q.Set("exp", fmt.Sprint(exp.Unix()))
	if l.signer != nil {
		q.Set("sig", l.signer(key, exp))
	}
	return l.baseURL + "/" + key + "?" + q.Encode(), nil
}
