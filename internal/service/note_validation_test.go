package service

import (
	"testing"

	"vaulty-api/internal/model"
)

func TestNormalizeType(t *testing.T) {
	ok := map[string]string{"note": "note", " Command ": "command", "code-snippet": "code-snippet", "doc_2": "doc_2"}
	for in, want := range ok {
		got, err := NormalizeType(in)
		if err != nil || got != want {
			t.Errorf("NormalizeType(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1note", "no spaces", "a/b", "abcdefghijklmnopqrstuvwxyz1234567"} {
		if _, err := NormalizeType(in); err != model.ErrInvalidType {
			t.Errorf("NormalizeType(%q) err = %v; want model.ErrInvalidType", in, err)
		}
	}
	for _, in := range []string{"credential", "Password", "api-key", "secret"} {
		if _, err := NormalizeType(in); err != model.ErrBlockedType {
			t.Errorf("NormalizeType(%q) err = %v; want model.ErrBlockedType", in, err)
		}
	}
}

func TestInferType(t *testing.T) {
	cases := []struct{ body, url, typ, wantBody, wantURL string }{
		{"catatan biasa", "", "note", "catatan biasa", ""},
		{"  https://go.dev/doc  ", "", "link", "", "https://go.dev/doc"},
		{"baca ini https://go.dev nanti", "", "note", "baca ini https://go.dev nanti", ""},
		{"deskripsi", "https://x.io", "link", "deskripsi", "https://x.io"},
	}
	for _, c := range cases {
		typ, body, url := InferType(c.body, c.url)
		if typ != c.typ || body != c.wantBody || url != c.wantURL {
			t.Errorf("InferType(%q,%q) = %q,%q,%q", c.body, c.url, typ, body, url)
		}
	}
}

func TestDetectSecrets(t *testing.T) {
	positives := []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"export OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz123",
		"postgres://admin:hunter2secret@db:5432/app",
		"password: S3cretPassw0rd",
		"AKIAIOSFODNN7EXAMPLE",
	}
	for _, s := range positives {
		if len(DetectSecrets(s)) == 0 {
			t.Errorf("DetectSecrets(%q) tidak mendeteksi apa pun", s)
		}
	}
	negatives := []string{
		"reset password user lewat artisan tinker",
		"docker exec -it db psql -U $POSTGRES_USER",
		"password: ${DB_PASSWORD}",
		"postgres://vaulty@localhost:5434/vaulty",
	}
	for _, s := range negatives {
		if w := DetectSecrets(s); len(w) != 0 {
			t.Errorf("DetectSecrets(%q) false positive: %v", s, w)
		}
	}
}
