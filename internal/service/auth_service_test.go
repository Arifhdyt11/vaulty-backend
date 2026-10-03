package service

import "testing"

func TestCanRegister(t *testing.T) {
	closed := NewAuthService(nil, nil, nil, AuthConfig{AllowedEmails: []string{" Arif@Example.com "}})
	if !closed.canRegister("arif@example.com") || closed.canRegister("orang@lain.com") {
		t.Fatal("allowlist tidak diterapkan")
	}
	if !NewAuthService(nil, nil, nil, AuthConfig{RegistrationOpen: true}).canRegister("siapa@saja.com") {
		t.Fatal("registrasi terbuka harus menerima semua email")
	}
}
