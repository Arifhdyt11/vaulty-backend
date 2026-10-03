package googleauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

func signToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v := newVerifier(&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, []string{"web-id", "ios-id"})
	now := time.Now()
	base := func() map[string]any {
		return map[string]any{
			"iss": "https://accounts.google.com", "aud": "ios-id", "sub": "123",
			"email": "arif@example.com", "email_verified": true, "name": "Arif",
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		}
	}
	ctx := context.Background()

	p, err := v.Verify(ctx, signToken(t, key, base()))
	if err != nil {
		t.Fatalf("token valid ditolak: %v", err)
	}
	if p.Sub != "123" || p.Email != "arif@example.com" || !p.EmailVerified || p.Name != "Arif" {
		t.Fatalf("profil tidak sesuai: %+v", p)
	}

	cases := map[string]func(map[string]any){
		"audience asing": func(c map[string]any) { c["aud"] = "client-lain" },
		"issuer palsu":   func(c map[string]any) { c["iss"] = "https://evil.example.com" },
		"kedaluwarsa":    func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() },
	}
	for name, mutate := range cases {
		c := base()
		mutate(c)
		if _, err := v.Verify(ctx, signToken(t, key, c)); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: err = %v; want ErrInvalidToken", name, err)
		}
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := v.Verify(ctx, signToken(t, other, base())); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("tanda tangan asing: err = %v", err)
	}
}
