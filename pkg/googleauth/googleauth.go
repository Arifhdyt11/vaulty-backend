// Package googleauth memverifikasi id_token Google yang dikirim client (OAuth dijalankan di app).
package googleauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Profile struct {
	Sub, Email, Name, Picture string
	EmailVerified             bool
}

type Verifier struct {
	clientIDs []string
	provider  *oidc.Provider
}

var ErrNotConfigured = errors.New("login Google belum dikonfigurasi")

func New(ctx context.Context, clientIDs []string) (*Verifier, error) {
	if len(clientIDs) == 0 {
		return &Verifier{}, nil
	}
	p, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("oidc provider google: %w", err)
	}
	return &Verifier{clientIDs: clientIDs, provider: p}, nil
}

func (v *Verifier) Enabled() bool { return v != nil && v.provider != nil }

func (v *Verifier) Verify(ctx context.Context, idToken string) (Profile, error) {
	if !v.Enabled() {
		return Profile{}, ErrNotConfigured
	}
	var lastErr error
	for _, cid := range v.clientIDs {
		tok, err := v.provider.Verifier(&oidc.Config{ClientID: cid}).Verify(ctx, idToken)
		if err != nil {
			lastErr = err
			continue
		}
		var c struct {
			Sub           string `json:"sub"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
			Name          string `json:"name"`
			Picture       string `json:"picture"`
		}
		if err := tok.Claims(&c); err != nil {
			return Profile{}, fmt.Errorf("claims: %w", err)
		}
		return Profile{Sub: c.Sub, Email: c.Email, Name: c.Name, Picture: c.Picture, EmailVerified: c.EmailVerified}, nil
	}
	return Profile{}, fmt.Errorf("verifikasi id_token: %w", lastErr)
}
