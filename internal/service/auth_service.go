package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/googleauth"
	"vaulty-api/pkg/password"
)

// AuthService menangani akun, login (password & Google), dan access token (ADR-015, ADR-018).
type AuthService struct {
	users      *repository.UserRepository
	sessions   *repository.SessionRepository
	google     *googleauth.Verifier // nil jika GOOGLE_CLIENT_IDS kosong
	sessionTTL time.Duration
	dummyHash  string
	// Registrasi publik baru dibuka di fase multi-tenant (ADR-019). Sebelum itu, akun baru
	// hanya bisa dibuat untuk email di allowlist.
	registrationOpen bool
	allowedEmails    map[string]bool
}

type AuthConfig struct {
	SessionTTL       time.Duration
	RegistrationOpen bool
	AllowedEmails    []string
}

func NewAuthService(users *repository.UserRepository, sessions *repository.SessionRepository, google *googleauth.Verifier, cfg AuthConfig) *AuthService {
	dummy, _ := password.Hash("dummy-password-for-timing")
	allowed := map[string]bool{}
	for _, e := range cfg.AllowedEmails {
		allowed[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return &AuthService{
		users: users, sessions: sessions, google: google, sessionTTL: cfg.SessionTTL, dummyHash: dummy,
		registrationOpen: cfg.RegistrationOpen, allowedEmails: allowed,
	}
}

func (s *AuthService) canRegister(email string) bool {
	return s.registrationOpen || s.allowedEmails[email]
}

// Register membuat akun baru dengan role user. Akun admin tidak bisa dibuat lewat sini (ADR-003).
func (s *AuthService) Register(ctx context.Context, email, pass, name string, meta ClientMeta) (model.Session, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return model.Session{}, err
	}
	if !s.canRegister(email) {
		return model.Session{}, model.ErrRegistrationClosed
	}
	if len([]rune(pass)) < 8 {
		return model.Session{}, model.ErrWeakPassword
	}
	hash, err := password.Hash(pass)
	if err != nil {
		return model.Session{}, err
	}
	u, err := s.users.Create(ctx, repository.NewUser{
		Email:        email,
		Name:         strings.TrimSpace(name),
		PasswordHash: hash,
	})
	if err != nil {
		return model.Session{}, err
	}
	return s.issue(ctx, u, meta)
}

func (s *AuthService) Login(ctx context.Context, email, pass string, meta ClientMeta) (model.Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, hash, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, model.ErrNotFound) {
		password.Verify(pass, s.dummyHash) // samakan waktu respons, cegah enumerasi email
		return model.Session{}, model.ErrInvalidCredentials
	}
	if err != nil {
		return model.Session{}, err
	}
	if hash == "" {
		return model.Session{}, model.ErrGoogleOnly
	}
	ok, err := password.Verify(pass, hash)
	if err != nil {
		return model.Session{}, err
	}
	if !ok {
		return model.Session{}, model.ErrInvalidCredentials
	}
	return s.issue(ctx, u, meta)
}

// LoginWithGoogle memverifikasi id_token, lalu mencari akun berdasarkan Google sub, lalu email
// (terverifikasi), atau membuat akun baru jika registrasi diizinkan untuk email tersebut.
// created=true jika akun baru dibuat.
func (s *AuthService) LoginWithGoogle(ctx context.Context, idToken string, meta ClientMeta) (sess model.Session, created bool, err error) {
	if s.google == nil {
		return model.Session{}, false, model.ErrGoogleDisabled
	}
	p, err := s.google.Verify(ctx, idToken)
	if err != nil {
		return model.Session{}, false, errors.Join(model.ErrInvalidGoogleToken, err)
	}
	if !p.EmailVerified {
		return model.Session{}, false, model.ErrEmailUnverified
	}

	u, err := s.users.FindByGoogleSub(ctx, p.Sub)
	if errors.Is(err, model.ErrNotFound) {
		u, created, err = s.linkOrCreateGoogleUser(ctx, p)
	}
	if err != nil {
		return model.Session{}, false, err
	}
	sess, err = s.issue(ctx, u, meta)
	return sess, created, err
}

func (s *AuthService) linkOrCreateGoogleUser(ctx context.Context, p googleauth.Profile) (model.User, bool, error) {
	email, err := normalizeEmail(p.Email)
	if err != nil {
		return model.User{}, false, err
	}
	u, _, err := s.users.FindByEmail(ctx, email)
	switch {
	case err == nil:
		u, err = s.users.LinkGoogle(ctx, u.ID, p.Sub, p.Picture, p.Name)
		return u, false, err
	case errors.Is(err, model.ErrNotFound):
		if !s.canRegister(email) {
			return model.User{}, false, model.ErrRegistrationClosed
		}
		u, err = s.users.Create(ctx, repository.NewUser{
			Email:     email,
			Name:      p.Name,
			GoogleSub: p.Sub,
			AvatarURL: p.Picture,
		})
		return u, true, err
	default:
		return model.User{}, false, err
	}
}

// UserByToken dipakai middleware auth untuk setiap request.
func (s *AuthService) UserByToken(ctx context.Context, token string) (model.User, error) {
	return s.sessions.FindUserByTokenHash(ctx, hashToken(token))
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	return s.sessions.Delete(ctx, hashToken(token))
}

func (s *AuthService) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	return s.sessions.DeleteExpired(ctx)
}

// ClientMeta dicatat bersama session untuk audit.
type ClientMeta struct{ UserAgent, IP string }

// issue membuat access token acak (opaque); hanya hash SHA-256-nya yang disimpan di DB,
// sehingga token bisa dicabut kapan saja (logout).
func (s *AuthService) issue(ctx context.Context, u model.User, meta ClientMeta) (model.Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return model.Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	expires := time.Now().Add(s.sessionTTL)
	if err := s.sessions.Create(ctx, u.ID, hashToken(token), meta.UserAgent, meta.IP, expires); err != nil {
		return model.Session{}, err
	}
	return model.Session{User: u, AccessToken: token, ExpiresAt: expires}, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", model.ErrInvalidEmail
	}
	return email, nil
}
