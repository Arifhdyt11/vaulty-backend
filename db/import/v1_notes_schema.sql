-- Skema staging untuk impor catatan Vaulty v1 (tabel notes lama) ke items.
-- Dijalankan oleh scripts/import-v1.sh sebelum memuat dump. Bukan migrasi goose:
-- skema v1 hanya hidup selama proses impor dan boleh dihapus setelahnya.
CREATE SCHEMA IF NOT EXISTS v1;

CREATE TABLE IF NOT EXISTS v1.notes (
  id              BIGINT PRIMARY KEY,
  user_id         BIGINT,
  type            TEXT,
  title           TEXT,
  body            TEXT,
  url             TEXT,
  tags            TEXT[],
  auto_tags       TEXT[],
  project         TEXT,
  metadata        JSONB,
  file_key        TEXT,
  file_name       TEXT,
  file_mime       TEXT,
  file_size       BIGINT,
  content_text    TEXT,
  embedding       vector,
  embedding_model TEXT,
  content_version INT,
  index_status    TEXT,
  index_error     TEXT,
  created_at      TIMESTAMPTZ,
  updated_at      TIMESTAMPTZ,
  deleted_at      TIMESTAMPTZ
);

-- Peta v1 → item baru, supaya impor bisa diulang tanpa duplikat.
CREATE TABLE IF NOT EXISTS v1.import_map (
  v1_id      BIGINT PRIMARY KEY,
  item_id    BIGINT NOT NULL,
  imported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
