package service

import (
	"regexp"
	"strings"
	"testing"

	"vaulty/internal/model"
)

func TestKodeOTPSelaluEnamDigit(t *testing.T) {
	re := regexp.MustCompile(`^[0-9]{6}$`)
	for range 200 {
		if c := newCode(); !re.MatchString(c) {
			t.Fatalf("kode %q bukan 6 digit", c)
		}
	}
}

func TestHashKodeTerikatUserDanTujuan(t *testing.T) {
	s := &AuthService{cfg: AuthConfig{Secret: "rahasia"}}
	base := s.codeHash(1, purposeVerify, "123456")
	if base == s.codeHash(2, purposeVerify, "123456") {
		t.Error("hash harus beda untuk user lain")
	}
	if base == s.codeHash(1, purposeReset, "123456") {
		t.Error("hash harus beda untuk tujuan lain (kode verifikasi tidak bisa dipakai reset)")
	}
	if base != s.codeHash(1, purposeVerify, "123456") {
		t.Error("hash harus deterministik")
	}
	if strings.Contains(base, "123456") {
		t.Error("kode tidak boleh tersimpan polos")
	}
}

func TestEmailKodeBerisiKodeDanTanpaHTMLMentah(t *testing.T) {
	m := codeEmail(model.User{Email: "a@b.id", Name: "<script>"}, purposeReset, "654321", codeResetTTL)
	if m.To != "a@b.id" || !strings.Contains(m.Subject, "reset") {
		t.Fatalf("header salah: %+v", m)
	}
	if !strings.Contains(m.Text, "654321") || !strings.Contains(m.HTML, "654321") {
		t.Error("kode harus ada di teks dan HTML")
	}
	if strings.Contains(m.HTML, "<script>") {
		t.Error("nama user harus di-escape di HTML")
	}
	if !strings.Contains(m.Text, "15 menit") {
		t.Errorf("masa berlaku tidak disebut: %q", m.Text)
	}
}

func TestEmailMatiBilaSenderKosong(t *testing.T) {
	if (&AuthService{}).EmailEnabled() {
		t.Error("tanpa mailer, fitur email harus mati")
	}
}
