package mail

import (
	"strings"
	"testing"
	"time"
)

func TestBuildMenyusunMultipartTeksDanHTML(t *testing.T) {
	b, err := Build("Vaulty <vaulty@mail.reepzs.site>", Message{To: "a@b.id", Subject: "Kode verifikasi Vaulty", Text: "Kode: 123456", HTML: "<b>123456</b>"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"To: a@b.id\r\n", "multipart/alternative", "text/plain; charset=utf-8", "text/html; charset=utf-8", "Kode: 123456", "<b>123456</b>"} {
		if !strings.Contains(s, want) {
			t.Errorf("pesan tidak berisi %q", want)
		}
	}
}

func TestBuildMenolakHeaderInjection(t *testing.T) {
	if _, err := Build("Vaulty <x@y.id>", Message{To: "a@b.id\r\nBcc: c@d.id", Subject: "x", Text: "x"}, time.Now()); err == nil {
		t.Fatal("harus menolak baris baru di header")
	}
}
