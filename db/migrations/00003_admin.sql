-- +goose Up
-- Konsol admin web (ADR-021): pengaturan platform tanpa deploy, suspend akun,
-- kuota tambahan per user, dan kupon.

-- Nilai JSON per kunci. Kunci yang belum ada = default di kode.
-- pro_enabled: false → semua UI upgrade/Pro disembunyikan (peluncuran Free saja).
-- plan_rules: override aturan non-kuota per plan (ukuran upload, transkrip, dll.).
CREATE TABLE app_settings (
  key        TEXT PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO app_settings (key, value) VALUES ('pro_enabled', 'true');

-- Akun yang dinonaktifkan admin tidak bisa login dan semua sesinya dihapus.
ALTER TABLE users ADD COLUMN suspended_at TIMESTAMPTZ;

-- Kuota tambahan untuk periode itu saja (dari admin atau kupon); batas efektif = limit + bonus.
ALTER TABLE usage_counters ADD COLUMN bonus INT NOT NULL DEFAULT 0;

CREATE TABLE coupons (
  id              BIGSERIAL PRIMARY KEY,
  code            TEXT NOT NULL UNIQUE,              -- huruf besar
  description     TEXT NOT NULL DEFAULT '',
  kind            TEXT NOT NULL CHECK (kind IN ('pro_days', 'bonus')),
  metric          TEXT NOT NULL DEFAULT '',          -- untuk kind = bonus
  amount          INT NOT NULL CHECK (amount > 0),   -- hari Pro atau unit kuota
  max_redemptions INT NOT NULL DEFAULT 0,            -- 0 = tanpa batas
  redeemed        INT NOT NULL DEFAULT 0,
  starts_at       TIMESTAMPTZ,
  expires_at      TIMESTAMPTZ,
  active          BOOLEAN NOT NULL DEFAULT true,
  created_by      BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE coupon_redemptions (
  coupon_id  BIGINT NOT NULL REFERENCES coupons(id) ON DELETE CASCADE,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (coupon_id, user_id)
);
CREATE INDEX ON coupon_redemptions (user_id);

CREATE INDEX ON audit_logs (action, created_at);
CREATE INDEX ON messages (created_at) WHERE role = 'user';

-- +goose Down
DROP INDEX IF EXISTS messages_created_at_idx;
DROP INDEX IF EXISTS audit_logs_action_created_at_idx;
DROP TABLE coupon_redemptions;
DROP TABLE coupons;
ALTER TABLE usage_counters DROP COLUMN bonus;
ALTER TABLE users DROP COLUMN suspended_at;
DROP TABLE app_settings;
