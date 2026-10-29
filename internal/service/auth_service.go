// Package service: logika bisnis. Handler tidak boleh berisi SQL, repository tidak boleh berisi aturan bisnis.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/googleauth"
	mailer "vaulty/pkg/mail"
	"vaulty/pkg/password"
)

type ClientMeta struct{ UserAgent, IP string }

type AuthConfig struct {
	AccessTTL, RefreshTTL time.Duration
	RegistrationOpen      bool
	AllowedEmails         []string
	Secret                string        // HMAC kode OTP (APP_SECRET)
	Mail                  mailer.Sender // nil/Disabled = fitur email mati (ADR-022)
}

type AuthService struct {
	repos  *repository.Repos
	google *googleauth.Verifier
	cfg    AuthConfig
}

func NewAuthService(repos *repository.Repos, google *googleauth.Verifier, cfg AuthConfig) *AuthService {
	return &AuthService{repos: repos, google: google, cfg: cfg}
}

func (s *AuthService) canRegister(email string) bool {
	if s.cfg.RegistrationOpen {
		return true
	}
	for _, e := range s.cfg.AllowedEmails {
		if strings.EqualFold(e, email) {
			return true
		}
	}
	return false
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil || email == "" {
		return "", model.Invalid("email tidak valid")
	}
	return email, nil
}

func (s *AuthService) Register(ctx context.Context, email, pass, name string, meta ClientMeta) (model.User, model.Session, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return model.User{}, model.Session{}, err
	}
	if len(pass) < 8 {
		return model.User{}, model.Session{}, model.Invalid("password minimal 8 karakter")
	}
	if !s.canRegister(email) {
		return model.User{}, model.Session{}, model.ErrRegistrationOff
	}
	hash, err := password.Hash(pass)
	if err != nil {
		return model.User{}, model.Session{}, err
	}
	u, err := s.repos.Users.Create(ctx, repository.NewUser{Email: email, Name: strings.TrimSpace(name), PasswordHash: &hash})
	if err != nil {
		return model.User{}, model.Session{}, err
	}
	sess, err := s.issue(ctx, u, meta)
	s.audit(ctx, u.ID, "auth.register", meta)
	// Kode verifikasi dikirim langsung; gagal kirim tidak menggagalkan daftar (user bisa minta ulang).
	if err == nil && s.EmailEnabled() {
		if serr := s.sendCode(ctx, u, purposeVerify, true); serr != nil {
			slog.WarnContext(ctx, "kirim kode verifikasi saat daftar gagal", "user_id", u.ID, "err", serr)
		}
	}
	return u, sess, err
}

func (s *AuthService) Login(ctx context.Context, email, pass string, meta ClientMeta) (model.User, model.Session, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return model.User{}, model.Session{}, model.ErrBadCredentials
	}
	u, hash, err := s.repos.Users.ByEmail(ctx, email)
	if err != nil || hash == "" {
		return model.User{}, model.Session{}, model.ErrBadCredentials
	}
	if err := password.Verify(pass, hash); err != nil {
		return model.User{}, model.Session{}, model.ErrBadCredentials
	}
	sess, err := s.issue(ctx, u, meta)
	s.audit(ctx, u.ID, "auth.login", meta)
	return u, sess, err
}

func (s *AuthService) LoginWithGoogle(ctx context.Context, idToken string, meta ClientMeta) (model.User, model.Session, bool, error) {
	p, err := s.google.Verify(ctx, idToken)
	if err != nil {
		if errors.Is(err, googleauth.ErrNotConfigured) {
			return model.User{}, model.Session{}, false, model.Invalid("login Google belum dikonfigurasi di server")
		}
		return model.User{}, model.Session{}, false, fmt.Errorf("%w: id_token google tidak valid", model.ErrUnauthorized)
	}
	if !p.EmailVerified {
		return model.User{}, model.Session{}, false, model.Invalid("email Google belum terverifikasi")
	}
	created := false
	u, err := s.repos.Users.ByGoogleSub(ctx, p.Sub)
	if errors.Is(err, model.ErrNotFound) {
		email, nerr := normalizeEmail(p.Email)
		if nerr != nil {
			return model.User{}, model.Session{}, false, nerr
		}
		existing, _, lerr := s.repos.Users.ByEmail(ctx, email)
		switch {
		case lerr == nil:
			var avatar *string
			if p.Picture != "" {
				avatar = &p.Picture
			}
			if err := s.repos.Users.LinkGoogle(ctx, existing.ID, p.Sub, avatar); err != nil {
				return model.User{}, model.Session{}, false, err
			}
			u, err = s.repos.Users.ByID(ctx, existing.ID)
		case errors.Is(lerr, model.ErrNotFound):
			if !s.canRegister(email) {
				return model.User{}, model.Session{}, false, model.ErrRegistrationOff
			}
			var avatar *string
			if p.Picture != "" {
				avatar = &p.Picture
			}
			u, err = s.repos.Users.Create(ctx, repository.NewUser{Email: email, Name: p.Name, GoogleSub: &p.Sub, AvatarURL: avatar})
			created = true
		default:
			return model.User{}, model.Session{}, false, lerr
		}
	}
	if err != nil {
		return model.User{}, model.Session{}, false, err
	}
	sess, err := s.issue(ctx, u, meta)
	s.audit(ctx, u.ID, "auth.google", meta)
	return u, sess, created, err
}

func (s *AuthService) Refresh(ctx context.Context, refreshToken string, meta ClientMeta) (model.User, model.Session, error) {
	access, refresh := newToken(), newToken()
	now := time.Now()
	n := repository.NewSession{AccessHash: hashToken(access), RefreshHash: hashToken(refresh),
		AccessExpiresAt: now.Add(s.cfg.AccessTTL), RefreshExpiresAt: now.Add(s.cfg.RefreshTTL), UserAgent: meta.UserAgent, IP: meta.IP}
	_, userID, err := s.repos.Sessions.Rotate(ctx, hashToken(refreshToken), n)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return model.User{}, model.Session{}, model.ErrUnauthorized
		}
		return model.User{}, model.Session{}, err
	}
	u, err := s.repos.Users.ByID(ctx, userID)
	if err != nil {
		return model.User{}, model.Session{}, model.ErrUnauthorized
	}
	return u, model.Session{UserID: u.ID, AccessToken: access, RefreshToken: refresh, AccessExpiresAt: n.AccessExpiresAt, RefreshExpiresAt: n.RefreshExpiresAt}, nil
}

func (s *AuthService) Logout(ctx context.Context, userID int64, accessToken, refreshToken string, all bool) error {
	if all {
		return s.repos.Sessions.DeleteAll(ctx, userID)
	}
	if refreshToken != "" {
		return s.repos.Sessions.DeleteByRefresh(ctx, userID, hashToken(refreshToken))
	}
	return s.repos.Sessions.DeleteByAccess(ctx, hashToken(accessToken))
}

// UserByToken dipakai middleware Bearer.
func (s *AuthService) UserByToken(ctx context.Context, token string) (model.User, error) {
	u, err := s.repos.Sessions.UserByAccess(ctx, hashToken(token))
	if err != nil {
		return model.User{}, model.ErrUnauthorized
	}
	return u, nil
}

// CleanupExpired membersihkan sesi kedaluwarsa dan kode OTP lama (ADR-022).
func (s *AuthService) CleanupExpired(ctx context.Context) (int64, error) {
	if _, err := s.repos.Codes.Cleanup(ctx); err != nil {
		slog.WarnContext(ctx, "bersihkan kode email", "err", err)
	}
	return s.repos.Sessions.CleanupExpired(ctx)
}

func (s *AuthService) issue(ctx context.Context, u model.User, meta ClientMeta) (model.Session, error) {
	// Semua jalur login (email, Google, register) lewat sini, jadi cukup dicek sekali.
	if off, err := s.repos.Users.Suspended(ctx, u.ID); err == nil && off {
		return model.Session{}, model.ErrSuspended
	}
	access, refresh := newToken(), newToken()
	now := time.Now()
	n := repository.NewSession{UserID: u.ID, AccessHash: hashToken(access), RefreshHash: hashToken(refresh),
		AccessExpiresAt: now.Add(s.cfg.AccessTTL), RefreshExpiresAt: now.Add(s.cfg.RefreshTTL), UserAgent: meta.UserAgent, IP: meta.IP}
	id, err := s.repos.Sessions.Create(ctx, n)
	if err != nil {
		return model.Session{}, fmt.Errorf("buat sesi: %w", err)
	}
	return model.Session{ID: id, UserID: u.ID, AccessToken: access, RefreshToken: refresh, AccessExpiresAt: n.AccessExpiresAt, RefreshExpiresAt: n.RefreshExpiresAt}, nil
}

func (s *AuthService) audit(ctx context.Context, userID int64, action string, meta ClientMeta) {
	_ = s.repos.Audit.Log(ctx, &userID, action, "user", &userID, map[string]any{"ua": meta.UserAgent}, meta.IP)
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}
