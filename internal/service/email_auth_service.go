package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"vaulty/internal/model"
	"vaulty/pkg/mail"
	"vaulty/pkg/password"
)

// OTP email (ADR-022): 6 digit, disimpan sebagai HMAC, dibatasi umur, percobaan, dan jeda kirim.
const (
	purposeVerify = "verify"
	purposeReset  = "reset"

	codeVerifyTTL  = 30 * time.Minute
	codeResetTTL   = 15 * time.Minute
	codeMaxTries   = 5
	codeResendWait = 60 * time.Second
	minPasswordLen = 8
)

// EmailEnabled: false = server tanpa SMTP; client tidak meminta verifikasi dan menyembunyikan lupa password.
func (s *AuthService) EmailEnabled() bool { return s.cfg.Mail != nil && s.cfg.Mail.Enabled() }

func (s *AuthService) codeHash(userID int64, purpose, code string) string {
	m := hmac.New(sha256.New, []byte(s.cfg.Secret))
	fmt.Fprintf(m, "%d:%s:%s", userID, purpose, code)
	return hex.EncodeToString(m.Sum(nil))
}

func newCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// sendCode membuat kode baru dan mengirimnya. force = lewati jeda kirim ulang (aksi admin).
func (s *AuthService) sendCode(ctx context.Context, u model.User, purpose string, force bool) error {
	if !s.EmailEnabled() {
		return model.ErrMailOff
	}
	if !force {
		last, err := s.repos.Codes.LastSent(ctx, u.ID, purpose)
		if err != nil {
			return err
		}
		if time.Since(last) < codeResendWait {
			return model.ErrTooMany
		}
	}
	code := newCode()
	ttl := codeVerifyTTL
	if purpose == purposeReset {
		ttl = codeResetTTL
	}
	if err := s.repos.Codes.Create(ctx, u.ID, purpose, s.codeHash(u.ID, purpose, code), time.Now().Add(ttl)); err != nil {
		return fmt.Errorf("simpan kode: %w", err)
	}
	if err := s.cfg.Mail.Send(ctx, codeEmail(u, purpose, code, ttl)); err != nil {
		slog.ErrorContext(ctx, "kirim email gagal", "user_id", u.ID, "purpose", purpose, "err", err)
		if errors.Is(err, mail.ErrNotConfigured) {
			return model.ErrMailOff
		}
		return fmt.Errorf("kirim email: %w", err)
	}
	return nil
}

// checkCode memvalidasi kode dan menandainya terpakai bila benar.
func (s *AuthService) checkCode(ctx context.Context, userID int64, purpose, code string) error {
	bad := model.Invalid("kode salah atau sudah kedaluwarsa")
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return bad
	}
	c, err := s.repos.Codes.Active(ctx, userID, purpose)
	if errors.Is(err, model.ErrNotFound) {
		return bad
	}
	if err != nil {
		return err
	}
	if c.Attempts >= codeMaxTries {
		_ = s.repos.Codes.Use(ctx, c.ID)
		return model.Invalid("terlalu banyak percobaan; minta kode baru")
	}
	if !hmac.Equal([]byte(c.Hash), []byte(s.codeHash(userID, purpose, code))) {
		_ = s.repos.Codes.Fail(ctx, c.ID)
		return bad
	}
	return s.repos.Codes.Use(ctx, c.ID)
}

// SendVerification mengirim OTP verifikasi ke email user sendiri.
func (s *AuthService) SendVerification(ctx context.Context, u model.User) error {
	if u.EmailVerified {
		return model.Invalid("email sudah terverifikasi")
	}
	return s.sendCode(ctx, u, purposeVerify, false)
}

func (s *AuthService) VerifyEmail(ctx context.Context, u model.User, code string, meta ClientMeta) (model.User, error) {
	if u.EmailVerified {
		return u, nil
	}
	if err := s.checkCode(ctx, u.ID, purposeVerify, code); err != nil {
		return model.User{}, err
	}
	if err := s.repos.Users.SetVerified(ctx, u.ID, true); err != nil {
		return model.User{}, err
	}
	s.audit(ctx, u.ID, "auth.email_verified", meta)
	return s.repos.Users.ByID(ctx, u.ID)
}

// ForgotPassword selalu sukses dari sisi client supaya tidak membocorkan email mana yang terdaftar.
func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	if !s.EmailEnabled() {
		return model.ErrMailOff
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	u, _, err := s.repos.Users.ByEmail(ctx, email)
	if err != nil {
		return nil
	}
	if off, _ := s.repos.Users.Suspended(ctx, u.ID); off {
		return nil
	}
	if err := s.sendCode(ctx, u, purposeReset, false); err != nil && !errors.Is(err, model.ErrTooMany) {
		return err
	}
	return nil
}

// ResetPassword: kode benar → password baru, email dianggap terverifikasi, semua sesi lama dihapus, lalu login.
func (s *AuthService) ResetPassword(ctx context.Context, email, code, newPass string, meta ClientMeta) (model.User, model.Session, error) {
	bad := model.Invalid("kode salah atau sudah kedaluwarsa")
	email, err := normalizeEmail(email)
	if err != nil {
		return model.User{}, model.Session{}, err
	}
	if len(newPass) < minPasswordLen {
		return model.User{}, model.Session{}, model.Invalid("password minimal %d karakter", minPasswordLen)
	}
	u, _, err := s.repos.Users.ByEmail(ctx, email)
	if err != nil {
		return model.User{}, model.Session{}, bad
	}
	if err := s.checkCode(ctx, u.ID, purposeReset, code); err != nil {
		return model.User{}, model.Session{}, err
	}
	if err := s.setPassword(ctx, u.ID, newPass); err != nil {
		return model.User{}, model.Session{}, err
	}
	_ = s.repos.Users.SetVerified(ctx, u.ID, true)
	if err := s.repos.Sessions.DeleteAll(ctx, u.ID); err != nil {
		return model.User{}, model.Session{}, err
	}
	s.audit(ctx, u.ID, "auth.password_reset", meta)
	u, err = s.repos.Users.ByID(ctx, u.ID)
	if err != nil {
		return model.User{}, model.Session{}, err
	}
	sess, err := s.issue(ctx, u, meta)
	return u, sess, err
}

// ChangePassword dari pengaturan. Akun tanpa password (Google) boleh membuat password tanpa password lama.
// Sesi lain dikeluarkan; sesi ini tetap.
func (s *AuthService) ChangePassword(ctx context.Context, u model.User, current, newPass, accessToken string, meta ClientMeta) error {
	if len(newPass) < minPasswordLen {
		return model.Invalid("password minimal %d karakter", minPasswordLen)
	}
	hash, err := s.repos.Users.PasswordHash(ctx, u.ID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return err
	}
	if hash != "" {
		if password.Verify(current, hash) != nil {
			return model.Invalid("password saat ini salah")
		}
		if current == newPass {
			return model.Invalid("password baru harus berbeda")
		}
	}
	if err := s.setPassword(ctx, u.ID, newPass); err != nil {
		return err
	}
	if err := s.repos.Sessions.DeleteOthers(ctx, u.ID, hashToken(accessToken)); err != nil {
		return err
	}
	s.audit(ctx, u.ID, "auth.password_change", meta)
	return nil
}

func (s *AuthService) setPassword(ctx context.Context, userID int64, pass string) error {
	h, err := password.Hash(pass)
	if err != nil {
		return err
	}
	return s.repos.Users.SetPassword(ctx, userID, h)
}

// ---- dipanggil AdminService ----

// AdminSetPassword: admin mengganti password user; semua sesi user dikeluarkan.
func (s *AuthService) AdminSetPassword(ctx context.Context, userID int64, pass string) error {
	if len(pass) < minPasswordLen {
		return model.Invalid("password minimal %d karakter", minPasswordLen)
	}
	if err := s.setPassword(ctx, userID, pass); err != nil {
		return err
	}
	return s.repos.Sessions.DeleteAll(ctx, userID)
}

// AdminSendEmail mengirim kode verifikasi atau reset atas permintaan admin (tanpa jeda kirim ulang).
func (s *AuthService) AdminSendEmail(ctx context.Context, userID int64, kind string) error {
	u, err := s.repos.Users.ByID(ctx, userID)
	if err != nil {
		return err
	}
	switch kind {
	case purposeVerify:
		if u.EmailVerified {
			return model.Invalid("email pengguna ini sudah terverifikasi")
		}
	case purposeReset:
	default:
		return model.Invalid("jenis email harus verify atau reset")
	}
	return s.sendCode(ctx, u, kind, true)
}

// ---- isi email ----

func codeEmail(u model.User, purpose, code string, ttl time.Duration) mail.Message {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		name = "kamu"
	}
	mins := int(ttl.Minutes())
	subject, intro, outro := "Kode verifikasi Vaulty", "Ini kode untuk memverifikasi email akun Vaulty-mu:",
		"Kalau kamu tidak merasa mendaftar di Vaulty, abaikan email ini."
	if purpose == purposeReset {
		subject, intro, outro = "Kode reset password Vaulty", "Ada permintaan untuk mengganti password akun Vaulty-mu. Pakai kode ini:",
			"Kalau bukan kamu yang meminta, abaikan email ini; password-mu tidak berubah."
	}
	text := fmt.Sprintf("Halo %s,\n\n%s\n\n%s\n\nKode berlaku %d menit. Jangan bagikan kode ini ke siapa pun, termasuk yang mengaku dari Vaulty.\n\n%s\n\nVee, dari Vaulty", name, intro, code, mins, outro)
	h := html.EscapeString
	body := fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#F2F4F5;font-family:'Plus Jakarta Sans',Helvetica,Arial,sans-serif;color:#10241C">
<div style="max-width:480px;margin:0 auto;padding:32px 20px">
<div style="background:#fff;border:1px solid #E3E7E5;border-radius:22px;padding:28px">
<div style="font-weight:800;font-size:18px;color:#22594B">Vaulty</div>
<p style="font-size:15px;line-height:1.6;margin:20px 0 8px">Halo %s,</p>
<p style="font-size:15px;line-height:1.6;margin:0 0 20px">%s</p>
<div style="font-size:34px;font-weight:800;letter-spacing:8px;text-align:center;background:#EEF1EF;border-radius:14px;padding:16px 0">%s</div>
<p style="font-size:13px;line-height:1.6;color:#5E6A65;margin:20px 0 0">Kode berlaku %d menit. Jangan bagikan kode ini ke siapa pun, termasuk yang mengaku dari Vaulty.</p>
<p style="font-size:13px;line-height:1.6;color:#5E6A65;margin:8px 0 0">%s</p>
</div>
<p style="font-size:12px;color:#5E6A65;text-align:center;margin-top:16px">Vaulty · Simpan apa pun. Biar Vee yang ingat.</p>
</div></body></html>`, h(name), h(intro), code, mins, h(outro))
	return mail.Message{To: u.Email, Subject: subject, Text: text, HTML: body}
}
