package repository

import (
	"context"
	"time"

	"vaulty/internal/model"
)

// EmailCodeRepo: kode OTP verifikasi email & reset password (ADR-022). Hanya HMAC kode yang disimpan.
type EmailCodeRepo struct{ db DB }

type EmailCode struct {
	ID       int64
	Hash     string
	Attempts int
}

// Create membatalkan kode aktif lain dengan tujuan sama, lalu menyimpan kode baru.
func (r *EmailCodeRepo) Create(ctx context.Context, userID int64, purpose, hash string, expiresAt time.Time) error {
	if _, err := r.db.Exec(ctx, `UPDATE email_codes SET used_at = now() WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, userID, purpose); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `INSERT INTO email_codes (user_id, purpose, code_hash, expires_at) VALUES ($1, $2, $3, $4)`, userID, purpose, hash, expiresAt)
	return err
}

// LastSent: waktu kode terakhir dibuat (untuk jeda kirim ulang). Zero bila belum pernah.
func (r *EmailCodeRepo) LastSent(ctx context.Context, userID int64, purpose string) (time.Time, error) {
	var t *time.Time
	err := r.db.QueryRow(ctx, `SELECT max(created_at) FROM email_codes WHERE user_id = $1 AND purpose = $2`, userID, purpose).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, err
	}
	return *t, nil
}

// Active: kode terbaru yang belum dipakai dan belum kedaluwarsa.
func (r *EmailCodeRepo) Active(ctx context.Context, userID int64, purpose string) (EmailCode, error) {
	var c EmailCode
	err := r.db.QueryRow(ctx, `SELECT id, code_hash, attempts FROM email_codes
		WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC LIMIT 1`, userID, purpose).Scan(&c.ID, &c.Hash, &c.Attempts)
	if err != nil {
		return c, mapErr(err)
	}
	return c, nil
}

func (r *EmailCodeRepo) Fail(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE email_codes SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}

func (r *EmailCodeRepo) Use(ctx context.Context, id int64) error {
	t, err := r.db.Exec(ctx, `UPDATE email_codes SET used_at = now() WHERE id = $1 AND used_at IS NULL`, id)
	if err == nil && t.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return err
}

// Cleanup menghapus kode lama (dipanggil housekeeping worker).
func (r *EmailCodeRepo) Cleanup(ctx context.Context) (int64, error) {
	t, err := r.db.Exec(ctx, `DELETE FROM email_codes WHERE created_at < now() - interval '7 days'`)
	return t.RowsAffected(), err
}
