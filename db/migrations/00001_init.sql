-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

-- Akun. Semua data lain ber-user_id (ADR-007).
CREATE TABLE users (
  id            BIGSERIAL PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  avatar_url    TEXT,
  password_hash TEXT,                       -- NULL untuk akun yang hanya login Google
  google_sub    TEXT UNIQUE,
  timezone      TEXT NOT NULL DEFAULT 'Asia/Jakarta',
  plan          TEXT NOT NULL DEFAULT 'free' CHECK (plan IN ('free', 'pro')),
  role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);

-- Access token 1 jam + refresh token 90 hari, keduanya hanya disimpan sebagai hash (ADR-008).
CREATE TABLE sessions (
  id                 BIGSERIAL PRIMARY KEY,
  user_id            BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  access_token_hash  TEXT NOT NULL UNIQUE,
  refresh_token_hash TEXT NOT NULL UNIQUE,
  access_expires_at  TIMESTAMPTZ NOT NULL,
  refresh_expires_at TIMESTAMPTZ NOT NULL,
  user_agent         TEXT NOT NULL DEFAULT '',
  ip                 TEXT NOT NULL DEFAULT '',
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  rotated_at         TIMESTAMPTZ
);
CREATE INDEX ON sessions (user_id);
CREATE INDEX ON sessions (refresh_expires_at);

CREATE TABLE devices (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform    TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
  push_token  TEXT NOT NULL UNIQUE,
  app_version TEXT NOT NULL DEFAULT '',
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON devices (user_id);

-- Item = apa pun yang disimpan user (ADR-006 pipeline ingest).
CREATE TABLE items (
  id              BIGSERIAL PRIMARY KEY,
  user_id         BIGINT NOT NULL REFERENCES users(id),
  kind            TEXT NOT NULL CHECK (kind IN ('url', 'image', 'text', 'audio', 'file')),
  status          TEXT NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued', 'processing', 'ready', 'partial', 'failed')),
  source_url      TEXT,
  canonical_url   TEXT,
  platform        TEXT,
  content_type    TEXT,                     -- video|post|article|unknown (dari fetch)
  title           TEXT,
  description     TEXT,
  thumbnail_key   TEXT,                     -- key di object storage, bukan URL CDN (ADR-004)
  user_note       TEXT NOT NULL DEFAULT '',
  body            TEXT NOT NULL DEFAULT '', -- teks yang diketik user (kind=text)
  summary         TEXT,
  key_points      JSONB NOT NULL DEFAULT '[]',
  entities        JSONB NOT NULL DEFAULT '{}',
  tags            TEXT[] NOT NULL DEFAULT '{}',
  auto_tags       TEXT[] NOT NULL DEFAULT '{}',
  language        TEXT,
  content_text    TEXT,                     -- artikel / transkrip / OCR / teks file
  file_key        TEXT,
  file_name       TEXT,
  file_mime       TEXT,
  file_size       BIGINT,
  attachment_key  TEXT,                     -- screenshot pendamping untuk link sosmed yang gagal di-fetch
  duration_sec    INT NOT NULL DEFAULT 0,
  published_at    TIMESTAMPTZ,
  content_version INT NOT NULL DEFAULT 1,   -- naik tiap konten berubah; worker hanya menulis untuk versi yang sama
  ingest          JSONB NOT NULL DEFAULT '{}', -- {fetch:{status,error},transcribe:{...},understand:{...},index:{...}}
  embedding       vector(1536),             -- embedding ringkasan, untuk "item terkait"
  embedding_model TEXT,
  tsv             tsvector GENERATED ALWAYS AS (
                    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
                    setweight(to_tsvector('simple', user_note), 'A') ||
                    setweight(array_to_tsvector(tags || auto_tags), 'B') ||
                    setweight(to_tsvector('simple', coalesce(summary, '') || ' ' || coalesce(description, '')), 'B') ||
                    setweight(to_tsvector('simple', body), 'C')
                  ) STORED,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE INDEX ON items (user_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX ON items (user_id, updated_at DESC);
CREATE INDEX ON items (user_id, kind) WHERE deleted_at IS NULL;
CREATE INDEX ON items (user_id, platform) WHERE deleted_at IS NULL;
CREATE INDEX ON items USING gin (tsv);
CREATE INDEX ON items USING gin (tags);
CREATE INDEX ON items USING hnsw (embedding vector_cosine_ops);
CREATE INDEX ON items (status) WHERE status IN ('queued', 'processing');

-- Unit pencarian & citation (ADR-003).
CREATE TABLE item_chunks (
  id              BIGSERIAL PRIMARY KEY,
  item_id         BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  user_id         BIGINT NOT NULL REFERENCES users(id),
  content_version INT NOT NULL,
  seq             INT NOT NULL,
  source          TEXT NOT NULL DEFAULT 'content', -- summary|note|content
  text            TEXT NOT NULL,
  embedding       vector(1536),
  embedding_model TEXT,
  tsv             tsvector GENERATED ALWAYS AS (to_tsvector('simple', text)) STORED,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON item_chunks (item_id);
CREATE INDEX ON item_chunks (user_id);
CREATE INDEX ON item_chunks USING gin (tsv);
CREATE INDEX ON item_chunks USING hnsw (embedding vector_cosine_ops);

CREATE TABLE conversations (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id),
  title      TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
CREATE INDEX ON conversations (user_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE messages (
  id              BIGSERIAL PRIMARY KEY,
  conversation_id BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  user_id         BIGINT NOT NULL REFERENCES users(id),
  role            TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
  mode            TEXT NOT NULL DEFAULT 'notes' CHECK (mode IN ('notes', 'general')),
  content         TEXT NOT NULL,
  citations       JSONB NOT NULL DEFAULT '[]', -- [{item_id, chunk_id, quote}] (ADR-009)
  from_notes      BOOLEAN,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON messages (conversation_id, id);

-- Tabel sebagai sumber kebenaran reminder; worker memindai notify_at (ADR-010).
CREATE TABLE reminders (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES users(id),
  item_id      BIGINT REFERENCES items(id) ON DELETE SET NULL,
  text         TEXT NOT NULL CHECK (length(text) BETWEEN 1 AND 500),
  origin       TEXT NOT NULL DEFAULT 'user' CHECK (origin IN ('user', 'item', 'ai')),
  remind_at    TIMESTAMPTZ NOT NULL,
  repeat       TEXT NOT NULL DEFAULT '' CHECK (repeat IN ('', 'daily', 'weekly', 'monthly', 'yearly')),
  lead_minutes INT[] NOT NULL DEFAULT '{}',
  notify_at    TIMESTAMPTZ NOT NULL,
  notify_kind  TEXT NOT NULL DEFAULT 'event' CHECK (notify_kind IN ('event', 'lead', 'snooze')),
  status       TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('suggested', 'pending', 'sending', 'sent', 'done', 'cancelled', 'failed')),
  attempts     INT NOT NULL DEFAULT 0,
  claimed_at   TIMESTAMPTZ,
  last_sent_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reminders_due_idx ON reminders (notify_at) WHERE status = 'pending';
CREATE INDEX ON reminders (user_id, status, notify_at);
CREATE INDEX ON reminders (item_id);

-- Cache hasil fetch per URL, tanpa data user.
CREATE TABLE fetch_cache (
  canonical_url TEXT PRIMARY KEY,
  payload       JSONB NOT NULL,
  fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT,
  action     TEXT NOT NULL,
  entity     TEXT NOT NULL DEFAULT '',
  entity_id  BIGINT,
  metadata   JSONB NOT NULL DEFAULT '{}',
  ip         TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON audit_logs (user_id, created_at);

-- Billing (ADR-011). users.plan adalah cache dari subscriptions aktif.
CREATE TABLE subscriptions (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan         TEXT NOT NULL CHECK (plan IN ('free', 'pro')),
  provider     TEXT NOT NULL CHECK (provider IN ('revenuecat', 'stripe', 'midtrans', 'manual')),
  provider_ref TEXT NOT NULL DEFAULT '',
  product_id   TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL CHECK (status IN ('active', 'grace', 'expired', 'cancelled')),
  period_end   TIMESTAMPTZ,
  raw          JSONB NOT NULL DEFAULT '{}',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON subscriptions (user_id, status);
CREATE UNIQUE INDEX ON subscriptions (provider, provider_ref) WHERE provider_ref <> '';

CREATE TABLE usage_counters (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  metric  TEXT NOT NULL,
  period  TEXT NOT NULL,   -- 'YYYY-MM' atau 'YYYY-MM-DD' di zona waktu user
  used    INT NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, metric, period)
);

CREATE TABLE plan_limits (
  plan    TEXT NOT NULL,
  metric  TEXT NOT NULL,
  period  TEXT NOT NULL CHECK (period IN ('day', 'month')),
  "limit" INT NOT NULL,     -- -1 = tanpa batas
  PRIMARY KEY (plan, metric)
);
INSERT INTO plan_limits (plan, metric, period, "limit") VALUES
  ('free', 'items',       'month', 50),
  ('free', 'questions',   'day',   20),
  ('free', 'images',      'month', 10),
  ('free', 'stt_minutes', 'month', 0),
  ('free', 'files',       'month', 10),
  ('pro',  'items',       'month', -1),
  ('pro',  'questions',   'day',   -1),
  ('pro',  'images',      'month', -1),
  ('pro',  'stt_minutes', 'month', 300),
  ('pro',  'files',       'month', -1);

CREATE TABLE export_jobs (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
  file_key   TEXT,
  error      TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON export_jobs (user_id, created_at DESC);

-- +goose Down
DROP TABLE export_jobs;
DROP TABLE plan_limits;
DROP TABLE usage_counters;
DROP TABLE subscriptions;
DROP TABLE audit_logs;
DROP TABLE fetch_cache;
DROP TABLE reminders;
DROP TABLE messages;
DROP TABLE conversations;
DROP TABLE item_chunks;
DROP TABLE items;
DROP TABLE devices;
DROP TABLE sessions;
DROP TABLE users;
