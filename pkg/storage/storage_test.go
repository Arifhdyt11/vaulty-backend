package storage

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestLocalRoundTripAndTraversal(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, n, err := l.Save(ctx, 7, "../../etc/passwd.PDF", strings.NewReader("isi"))
	if err != nil || n != 3 {
		t.Fatalf("save: %v n=%d", err, n)
	}
	if !strings.HasPrefix(key, "7/") || !strings.HasSuffix(key, ".pdf") {
		t.Fatalf("key tidak terduga: %s", key)
	}
	rc, err := l.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "isi" {
		t.Fatalf("isi: %q", b)
	}
	if _, err := l.Open(ctx, "../outside"); err == nil {
		t.Fatal("path traversal harus ditolak")
	}
}
