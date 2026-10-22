package fetch

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive memukul jaringan sungguhan; hanya jalan bila VAULTY_LIVE=1 supaya
// CI tetap deterministik. Berguna untuk mengecek struktur halaman YouTube
// yang bisa berubah sewaktu-waktu.
func TestLive(t *testing.T) {
	if os.Getenv("VAULTY_LIVE") != "1" {
		t.Skip("set VAULTY_LIVE=1 untuk menjalankan")
	}
	f := New(Options{Timeout: 20 * time.Second})
	for _, u := range []string{
		"https://www.youtube.com/watch?v=jNQXAC9IVRw",
		"https://go.dev/blog/go1.22",
		"https://x.com/jack/status/20",
	} {
		res, err := f.Fetch(context.Background(), u)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		t.Logf("%s\n  platform=%s type=%s title=%q author=%q lang=%q dur=%d thumb=%s\n  text=%d transcript=%d warnings=%v",
			u, res.Platform, res.ContentType, res.Title, res.Author, res.Language, res.Duration, res.ThumbnailURL,
			len(res.Text), len(res.Transcript), res.Warnings)
	}
}
