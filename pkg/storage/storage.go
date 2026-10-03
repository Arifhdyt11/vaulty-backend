// Package storage menyimpan file upload. Implementasi lokal sekarang; bisa diganti S3/R2 nanti.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Storage interface {
	Save(ctx context.Context, userID int64, filename string, r io.Reader) (key string, size int64, err error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

type Local struct{ root string }

func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

func (l *Local) Save(_ context.Context, userID int64, filename string, r io.Reader) (string, int64, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", 0, err
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if len(ext) > 10 || strings.ContainsAny(ext, `/\`) {
		ext = ""
	}
	key := fmt.Sprintf("%d/%s%s", userID, hex.EncodeToString(b), ext)
	path, err := l.path(key)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return "", 0, err
	}
	return key, n, nil
}

func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (l *Local) Delete(_ context.Context, key string) error {
	path, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// path memastikan key tidak keluar dari root (path traversal).
func (l *Local) path(key string) (string, error) {
	p := filepath.Join(l.root, filepath.FromSlash(key))
	if !strings.HasPrefix(p, l.root+string(filepath.Separator)) {
		return "", fmt.Errorf("storage key tidak valid: %q", key)
	}
	return p, nil
}
