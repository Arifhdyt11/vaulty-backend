// Package googleauth memverifikasi id_token Google (tanda tangan JWKS, issuer, expiry, audience).
package googleauth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	googleIssuer  = "https://accounts.google.com"
	googleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
)

var ErrInvalidToken = errors.New("id_token Google tidak valid")

// Profile adalah klaim penting dari id_token Google.
type Profile struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

// Verifier memverifikasi id_token. OAuth-nya sendiri dijalankan client
// (NextAuth di web, Google Sign-In di iOS).
type Verifier struct {
	verifier  *oidc.IDTokenVerifier
	clientIDs []string
}

// NewVerifier menerima beberapa client ID (web, iOS) karena audience id_token
// berbeda per platform.
func NewVerifier(ctx context.Context, clientIDs []string) *Verifier {
	return newVerifier(oidc.NewRemoteKeySet(ctx, googleJWKSURL), clientIDs)
}

func newVerifier(keys oidc.KeySet, clientIDs []string) *Verifier {
	return &Verifier{
		verifier:  oidc.NewVerifier(googleIssuer, keys, &oidc.Config{SkipClientIDCheck: true}),
		clientIDs: clientIDs,
	}
}

func (g *Verifier) Verify(ctx context.Context, rawIDToken string) (Profile, error) {
	tok, err := g.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !slices.ContainsFunc(tok.Audience, func(aud string) bool { return slices.Contains(g.clientIDs, aud) }) {
		return Profile{}, fmt.Errorf("%w: audience %v tidak dikenal", ErrInvalidToken, tok.Audience)
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := tok.Claims(&c); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return Profile{
		Sub:           tok.Subject,
		Email:         c.Email,
		EmailVerified: c.EmailVerified,
		Name:          c.Name,
		Picture:       c.Picture,
	}, nil
}
