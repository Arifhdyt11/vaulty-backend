-- name: HybridSearch :many
-- Vector similarity + full-text, digabung dengan Reciprocal Rank Fusion (ADR-002).
-- query_vec NULL (provider embedding belum dikonfigurasi) => hanya full-text.
WITH q AS (
  SELECT websearch_to_tsquery('simple', sqlc.arg(query)::text) AS tsq
),
vec_candidates AS (
  SELECT n.id, n.embedding <=> sqlc.narg(query_vec)::vector AS dist
  FROM notes n
  WHERE sqlc.narg(query_vec)::vector IS NOT NULL
    AND n.user_id = sqlc.arg(user_id) AND n.deleted_at IS NULL AND n.embedding IS NOT NULL
    AND n.embedding_model = sqlc.narg(embedding_model)::text
    -- Buang kandidat yang terlalu jauh maknanya, supaya hasil tidak berisik.
    AND n.embedding <=> sqlc.narg(query_vec)::vector < sqlc.arg(max_distance)::float8
    AND (sqlc.narg(type)::text IS NULL OR n.type = sqlc.narg(type)::text)
    AND (sqlc.narg(project)::text IS NULL OR n.project = sqlc.narg(project)::text)
    AND (sqlc.narg(tag)::text IS NULL
         OR sqlc.narg(tag)::text = ANY(n.tags) OR sqlc.narg(tag)::text = ANY(n.auto_tags))
  ORDER BY dist
  LIMIT 50
),
vec AS (
  -- Hanya kandidat yang dekat dengan hasil terbaik (batas relatif).
  SELECT id, row_number() OVER (ORDER BY dist) AS rnk
  FROM vec_candidates
  WHERE dist <= (SELECT min(dist) FROM vec_candidates) + sqlc.arg(relative_margin)::float8
),
fts AS (
  SELECT n.id, row_number() OVER (ORDER BY ts_rank_cd(n.tsv, q.tsq) DESC) AS rnk
  FROM notes n, q
  WHERE n.user_id = sqlc.arg(user_id) AND n.deleted_at IS NULL AND n.tsv @@ q.tsq
    AND (sqlc.narg(type)::text IS NULL OR n.type = sqlc.narg(type)::text)
    AND (sqlc.narg(project)::text IS NULL OR n.project = sqlc.narg(project)::text)
    AND (sqlc.narg(tag)::text IS NULL
         OR sqlc.narg(tag)::text = ANY(n.tags) OR sqlc.narg(tag)::text = ANY(n.auto_tags))
  ORDER BY ts_rank_cd(n.tsv, q.tsq) DESC
  LIMIT 50
)
SELECT n.id, n.user_id, n.type, n.title, n.body, n.url, n.tags, n.auto_tags, n.project, n.metadata,
       n.file_key, n.file_name, n.file_mime, n.file_size, n.index_status, n.content_version,
       n.created_at, n.updated_at,
       (COALESCE(1.0 / (60 + vec.rnk), 0) + COALESCE(1.0 / (60 + fts.rnk), 0))::float8 AS score,
       CASE WHEN n.content_text IS NOT NULL
            THEN ts_headline('simple', n.content_text, q.tsq, 'MaxFragments=2, MaxWords=25, MinWords=8')
            ELSE '' END::text AS snippet
FROM vec
FULL OUTER JOIN fts ON vec.id = fts.id
JOIN notes n ON n.id = COALESCE(vec.id, fts.id)
CROSS JOIN q
WHERE n.user_id = sqlc.arg(user_id)
ORDER BY score DESC, n.id DESC
LIMIT sqlc.arg(result_limit);
