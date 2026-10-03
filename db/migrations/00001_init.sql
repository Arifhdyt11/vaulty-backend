-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE users (
  id            BIGSERIAL PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  avatar_url    TEXT,
  password_hash TEXT,                    -- NULL untuk akun yang hanya login via Google
  google_sub    TEXT UNIQUE,
  role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,       -- sha256 dari token; token asli hanya ada di cookie/client
  user_agent TEXT NOT NULL DEFAULT '',
  ip         TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON sessions (user_id);

-- Tipe bebas (note, link, command, document, ...), tetapi tipe credential ditolak (ADR-004).
CREATE TABLE notes (
  id            BIGSERIAL PRIMARY KEY,
  user_id       BIGINT NOT NULL REFERENCES users(id),
  type          TEXT NOT NULL CHECK (type ~ '^[a-z][a-z0-9_-]{0,31}$'),
  title         TEXT,
  body          TEXT NOT NULL DEFAULT '',  -- command disimpan verbatim
  url           TEXT,
  tags          TEXT[] NOT NULL DEFAULT '{}',
  auto_tags     TEXT[] NOT NULL DEFAULT '{}',
  project       TEXT,
  metadata      JSONB NOT NULL DEFAULT '{}',

  -- Lampiran file (type 'document')
  file_key      TEXT,
  file_name     TEXT,
  file_mime     TEXT,
  file_size     BIGINT,
  content_text  TEXT,                      -- teks hasil ekstraksi file, untuk search

  embedding       vector(1536),
  content_version INT NOT NULL DEFAULT 1, -- naik setiap konten berubah; worker hanya menulis hasil index untuk versi yang sama
  index_status  TEXT NOT NULL DEFAULT 'pending' CHECK (index_status IN ('pending', 'done', 'failed')),
  index_error   TEXT,
  tsv           tsvector GENERATED ALWAYS AS (
                  to_tsvector('simple',
                    coalesce(title, '') || ' ' || body || ' ' || coalesce(url, '') || ' ' ||
                    coalesce(file_name, '') || ' ' || coalesce(content_text, ''))
                ) STORED,

  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);
CREATE INDEX ON notes USING hnsw (embedding vector_cosine_ops);
CREATE INDEX ON notes USING gin (tsv);
CREATE INDEX ON notes USING gin (tags);
CREATE INDEX ON notes USING gin (auto_tags);
CREATE INDEX ON notes (user_id, type, project) WHERE deleted_at IS NULL;
CREATE INDEX ON notes (index_status, updated_at) WHERE index_status = 'pending';

CREATE TABLE audit_logs (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT REFERENCES users(id),
  action     TEXT NOT NULL,              -- mis. note.create, note.update, auth.login
  entity     TEXT NOT NULL DEFAULT '',
  entity_id  BIGINT,
  metadata   JSONB NOT NULL DEFAULT '{}',
  ip         TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON audit_logs (user_id, created_at);

-- +goose Down
DROP TABLE audit_logs;
DROP TABLE notes;
DROP TABLE sessions;
DROP TABLE users;
