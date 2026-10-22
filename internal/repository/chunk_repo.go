package repository

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"vaulty/internal/model"
)

type ChunkRepo struct{ db DB }

type NewChunk struct {
	Seq       int
	Source    string
	Text      string
	Embedding []float32
}

// Replace mengganti semua chunk item untuk versi tertentu (hapus lama, tulis baru).
func (r *ChunkRepo) Replace(ctx context.Context, userID, itemID int64, version int32, modelName string, chunks []NewChunk) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM item_chunks WHERE item_id = $1 AND user_id = $2`, itemID, userID); err != nil {
		return err
	}
	for _, c := range chunks {
		var emb any
		if c.Embedding != nil {
			emb = pgvector.NewVector(padVector(c.Embedding))
		}
		if _, err := r.db.Exec(ctx, `INSERT INTO item_chunks (item_id, user_id, content_version, seq, source, text, embedding, embedding_model)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, itemID, userID, version, c.Seq, c.Source, c.Text, emb, modelName); err != nil {
			return fmt.Errorf("simpan chunk: %w", err)
		}
	}
	return nil
}

// Hybrid: BM25 (ts_rank) + vektor (cosine) digabung dengan Reciprocal Rank Fusion (ADR-003).
// queryEmb boleh nil → hanya full-text.
func (r *ChunkRepo) Hybrid(ctx context.Context, userID int64, query string, queryEmb []float32, k int) ([]model.ChunkHit, error) {
	const rrfK = 60
	var rows interface {
		Next() bool
		Scan(dest ...any) error
		Close()
		Err() error
	}
	var err error
	if queryEmb != nil {
		rows, err = r.db.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('simple', $2) AS tsq, $3::vector AS v),
		ft AS (
		  SELECT c.id, row_number() OVER (ORDER BY ts_rank(c.tsv, q.tsq) DESC, c.id) AS rnk
		  FROM item_chunks c JOIN items i ON i.id = c.item_id, q
		  WHERE c.user_id = $1 AND i.deleted_at IS NULL AND c.tsv @@ q.tsq LIMIT 40
		),
		vec AS (
		  SELECT c.id, row_number() OVER (ORDER BY c.embedding <=> q.v, c.id) AS rnk
		  FROM item_chunks c JOIN items i ON i.id = c.item_id, q
		  WHERE c.user_id = $1 AND i.deleted_at IS NULL AND c.embedding IS NOT NULL
		  ORDER BY c.embedding <=> q.v LIMIT 40
		),
		fused AS (
		  SELECT id, sum(1.0 / ($5 + rnk)) AS score FROM (SELECT * FROM ft UNION ALL SELECT * FROM vec) u GROUP BY id
		)
		SELECT c.id, c.item_id, c.seq, c.source, c.text, f.score, `+itemCols+`
		FROM fused f JOIN item_chunks c ON c.id = f.id JOIN items i ON i.id = c.item_id
		ORDER BY f.score DESC LIMIT $4`, userID, query, pgvector.NewVector(padVector(queryEmb)), k, rrfK)
	} else {
		rows, err = r.db.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('simple', $2) AS tsq)
		SELECT c.id, c.item_id, c.seq, c.source, c.text, ts_rank(c.tsv, q.tsq) AS score, `+itemCols+`
		FROM item_chunks c JOIN items i ON i.id = c.item_id, q
		WHERE c.user_id = $1 AND i.deleted_at IS NULL AND c.tsv @@ q.tsq
		ORDER BY score DESC LIMIT $3`, userID, query, k)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ChunkHit
	for rows.Next() {
		var h model.ChunkHit
		var it model.Item
		var keyPoints, entities, ingest []byte
		if err := rows.Scan(&h.Chunk.ID, &h.Chunk.ItemID, &h.Chunk.Seq, &h.Chunk.Source, &h.Chunk.Text, &h.Score,
			&it.ID, &it.UserID, &it.Kind, &it.Status, &it.SourceURL, &it.CanonicalURL, &it.Platform, &it.ContentType, &it.Title, &it.Description,
			&it.ThumbnailKey, &it.UserNote, &it.Body, &it.Summary, &keyPoints, &entities, &it.Tags, &it.AutoTags, &it.Language, &it.ContentText,
			&it.FileKey, &it.FileName, &it.FileMime, &it.FileSize, &it.AttachmentKey, &it.DurationSec, &it.PublishedAt, &it.ContentVersion, &ingest,
			&it.RemindersCount, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		h.Item = it
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *ChunkRepo) ByIDs(ctx context.Context, userID int64, ids []int64) (map[int64]model.Chunk, error) {
	out := map[int64]model.Chunk{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT id, item_id, seq, source, text FROM item_chunks WHERE user_id = $1 AND id = ANY($2)`, userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c model.Chunk
		if err := rows.Scan(&c.ID, &c.ItemID, &c.Seq, &c.Source, &c.Text); err != nil {
			return nil, err
		}
		out[c.ID] = c
	}
	return out, rows.Err()
}

// StaleEmbeddingItems: item yang embedding-nya dari model lain (ganti model → re-index).
func (r *ChunkRepo) StaleEmbeddingItems(ctx context.Context, modelName string, limit int) ([][2]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT DISTINCT c.item_id, c.user_id FROM item_chunks c JOIN items i ON i.id = c.item_id
		WHERE i.deleted_at IS NULL AND c.embedding IS NOT NULL AND c.embedding_model IS DISTINCT FROM $1 LIMIT $2`, modelName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]int64
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
