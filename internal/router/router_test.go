package router

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"vaulty/pkg/storage"
)

// Pemutar audio iOS meminta potongan byte dulu dan menolak server yang
// tidak menjawab 206; audio Siaran Vee bergantung pada ini.
func TestFileLokalMendukungRange(t *testing.T) {
	const secret = "rahasia"
	srv := httptest.NewUnstartedServer(nil)
	store, err := storage.NewLocal(t.TempDir(), "http://"+srv.Listener.Addr().String()+"/files", Sign(secret))
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Get("/files/*", localFileHandler(store, secret))
	srv.Config.Handler = r
	srv.Start()
	defer srv.Close()

	ctx := context.Background()
	data := bytes.Repeat([]byte("0123456789"), 100)
	key := "u/1/briefing/2026-10-23/1-1.m4a"
	if err := store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "audio/mp4"); err != nil {
		t.Fatal(err)
	}
	u, _ := store.URL(ctx, key, time.Hour)

	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Range", "bytes=0-1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(body) != "01" {
		t.Fatalf("range: status %d body %q", res.StatusCode, body)
	}
	if got := res.Header.Get("Content-Range"); got != "bytes 0-1/1000" {
		t.Fatalf("Content-Range %q", got)
	}
	if got := res.Header.Get("Content-Type"); got != "audio/mp4" {
		t.Fatalf("Content-Type %q", got)
	}

	// Tanpa Range tetap 200 dengan isi utuh.
	res, err = http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(body) != len(data) {
		t.Fatalf("utuh: status %d len %d", res.StatusCode, len(body))
	}

	// Tanda tangan salah tetap ditolak.
	res, err = http.Get(strings.Replace(u, "sig=", "sig=x", 1))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("sig salah: status %d", res.StatusCode)
	}
}
