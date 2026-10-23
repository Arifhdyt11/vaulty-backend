-- +goose Up
-- Siaran Vee: briefing audio harian per user, satu baris per tanggal di zona waktu user (ADR-016).
CREATE TABLE briefings (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  date       DATE NOT NULL,
  status     TEXT NOT NULL DEFAULT 'building' CHECK (status IN ('building', 'ready', 'failed', 'empty')),
  greeting   TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, date)
);
CREATE INDEX ON briefings (date);

-- Satu bab = satu item yang dibacakan. Item yang dihapus permanen ikut menghapus babnya.
CREATE TABLE briefing_chapters (
  id           BIGSERIAL PRIMARY KEY,
  briefing_id  BIGINT NOT NULL REFERENCES briefings(id) ON DELETE CASCADE,
  user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  seq          INT NOT NULL,
  item_id      BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  label        TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL DEFAULT '',
  quote        TEXT NOT NULL DEFAULT '',
  script       TEXT NOT NULL DEFAULT '',
  audio_key    TEXT,                     -- key object storage (audio/mp4), bukan URL (ADR-004)
  duration_sec INT NOT NULL DEFAULT 0,
  status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (briefing_id, seq)
);
-- Riwayat item yang pernah tampil per user (pemilihan bab: belum pernah tampil / tidak dalam 14 hari).
CREATE INDEX ON briefing_chapters (user_id, item_id);
CREATE INDEX ON briefing_chapters (item_id);

-- +goose Down
DROP TABLE briefing_chapters;
DROP TABLE briefings;
