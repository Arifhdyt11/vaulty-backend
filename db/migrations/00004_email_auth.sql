-- +goose Up
-- Verifikasi email + reset password dengan OTP (ADR-022).

-- NULL = belum verifikasi. Akun lama dianggap terverifikasi supaya tidak terkunci;
-- akun Google selalu terverifikasi (Google sudah memverifikasi emailnya).
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
UPDATE users SET email_verified_at = created_at;

-- Kode OTP 6 digit; hanya HMAC-nya yang disimpan. Kode baru membatalkan kode lama
-- dengan tujuan yang sama; percobaan salah dibatasi.
CREATE TABLE email_codes (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose    TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
  code_hash  TEXT NOT NULL,
  attempts   INT NOT NULL DEFAULT 0,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON email_codes (user_id, purpose, created_at DESC);

-- +goose Down
DROP TABLE email_codes;
ALTER TABLE users DROP COLUMN email_verified_at;
