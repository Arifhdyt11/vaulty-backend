-- Semua query di sini di-scope user_id (ADR-007), kecuali ListPendingNotes
-- yang hanya dipakai worker untuk safety net dan tidak mengembalikan isi note.

-- name: CreateNote :one
INSERT INTO notes (user_id, type, title, body, url, tags, project, metadata,
                   file_key, file_name, file_mime, file_size)
VALUES (sqlc.arg(user_id), sqlc.arg(type), sqlc.narg(title), sqlc.arg(body), sqlc.narg(url),
        sqlc.arg(tags), sqlc.narg(project), sqlc.arg(metadata),
        sqlc.narg(file_key), sqlc.narg(file_name), sqlc.narg(file_mime), sqlc.narg(file_size))
RETURNING id, user_id, type, title, body, url, tags, auto_tags, project, metadata,
          file_key, file_name, file_mime, file_size, index_status, content_version,
          created_at, updated_at;

-- name: GetNote :one
SELECT id, user_id, type, title, body, url, tags, auto_tags, project, metadata,
       file_key, file_name, file_mime, file_size, index_status, content_version,
       created_at, updated_at
FROM notes
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- name: ListNotes :many
SELECT id, user_id, type, title, body, url, tags, auto_tags, project, metadata,
       file_key, file_name, file_mime, file_size, index_status, content_version,
       created_at, updated_at
FROM notes
WHERE user_id = sqlc.arg(user_id)
  AND deleted_at IS NULL
  AND (sqlc.narg(type)::text IS NULL OR type = sqlc.narg(type)::text)
  AND (sqlc.narg(project)::text IS NULL OR project = sqlc.narg(project)::text)
  AND (sqlc.narg(tag)::text IS NULL
       OR sqlc.narg(tag)::text = ANY(tags) OR sqlc.narg(tag)::text = ANY(auto_tags))
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);

-- name: UpdateNote :one
UPDATE notes
SET type = sqlc.arg(type),
    title = sqlc.narg(title),
    body = sqlc.arg(body),
    url = sqlc.narg(url),
    tags = sqlc.arg(tags),
    project = sqlc.narg(project),
    metadata = sqlc.arg(metadata),
    content_version = CASE WHEN sqlc.arg(content_changed)::bool THEN content_version + 1 ELSE content_version END,
    index_status = CASE WHEN sqlc.arg(content_changed)::bool THEN 'pending' ELSE index_status END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING id, user_id, type, title, body, url, tags, auto_tags, project, metadata,
          file_key, file_name, file_mime, file_size, index_status, content_version,
          created_at, updated_at;

-- name: SoftDeleteNote :execrows
UPDATE notes SET deleted_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- name: ListTypes :many
SELECT type, count(*)::bigint AS total
FROM notes
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL
GROUP BY type
ORDER BY total DESC, type;

-- name: GetNoteForIndex :one
SELECT id, user_id, type, title, body, url, tags, project,
       file_key, file_name, file_mime, content_text, content_version
FROM notes
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL;

-- name: SaveContentText :exec
UPDATE notes SET content_text = sqlc.arg(content_text)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: MarkNoteIndexed :execrows
UPDATE notes
SET embedding = sqlc.narg(embedding),
    embedding_model = sqlc.narg(embedding_model),
    auto_tags = sqlc.arg(auto_tags),
    index_status = 'done',
    index_error = NULL
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND content_version = sqlc.arg(content_version);

-- name: MarkNoteIndexFailed :exec
UPDATE notes
SET index_status = 'failed', index_error = sqlc.arg(index_error)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND content_version = sqlc.arg(content_version);

-- name: ListPendingNotes :many
SELECT id, user_id, content_version
FROM notes
WHERE index_status = 'pending' AND deleted_at IS NULL
  AND updated_at < now() - make_interval(secs => sqlc.arg(older_than_secs)::float8)
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: RequeueNote :one
UPDATE notes
SET content_version = content_version + 1, index_status = 'pending', index_error = NULL
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND deleted_at IS NULL
RETURNING id, user_id, type, title, body, url, tags, auto_tags, project, metadata,
          file_key, file_name, file_mime, file_size, index_status, content_version,
          created_at, updated_at;

-- name: RequeueStaleEmbeddings :execrows
-- Tandai ulang note yang embedding-nya dibuat model lain (mis. setelah pindah Ollama -> OpenAI).
-- Sistem-level (lintas user) seperti ListPendingNotes; tidak mengembalikan isi note.
UPDATE notes
SET content_version = content_version + 1, index_status = 'pending', index_error = NULL
WHERE id IN (
  SELECT id FROM notes
  WHERE deleted_at IS NULL AND index_status <> 'pending'
    AND embedding_model IS DISTINCT FROM sqlc.arg(model)::text
  ORDER BY id
  LIMIT sqlc.arg(row_limit)
);
