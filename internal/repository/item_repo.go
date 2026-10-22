package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"

	"vaulty/internal/model"
)

type ItemRepo struct{ db DB }

const itemCols = `i.id, i.user_id, i.kind, i.status, i.source_url, i.canonical_url, i.platform, i.content_type, i.title, i.description,
	i.thumbnail_key, i.user_note, i.body, i.summary, i.key_points, i.entities, i.tags, i.auto_tags, i.language, i.content_text,
	i.file_key, i.file_name, i.file_mime, i.file_size, i.attachment_key, i.duration_sec, i.published_at, i.content_version, i.ingest,
	(SELECT count(*) FROM reminders r WHERE r.item_id = i.id AND r.status IN ('suggested','pending','sent'))::int AS reminders_count,
	i.created_at, i.updated_at`

func scanItem(row interface{ Scan(...any) error }) (model.Item, error) {
	var it model.Item
	var keyPoints, entities, ingest []byte
	err := row.Scan(&it.ID, &it.UserID, &it.Kind, &it.Status, &it.SourceURL, &it.CanonicalURL, &it.Platform, &it.ContentType, &it.Title, &it.Description,
		&it.ThumbnailKey, &it.UserNote, &it.Body, &it.Summary, &keyPoints, &entities, &it.Tags, &it.AutoTags, &it.Language, &it.ContentText,
		&it.FileKey, &it.FileName, &it.FileMime, &it.FileSize, &it.AttachmentKey, &it.DurationSec, &it.PublishedAt, &it.ContentVersion, &ingest,
		&it.RemindersCount, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return it, mapErr(err)
	}
	_ = json.Unmarshal(keyPoints, &it.KeyPoints)
	_ = json.Unmarshal(entities, &it.Entities)
	_ = json.Unmarshal(ingest, &it.Ingest)
	it.EntitiesRaw = entities
	if it.KeyPoints == nil {
		it.KeyPoints = []string{}
	}
	if it.Tags == nil {
		it.Tags = []string{}
	}
	if it.AutoTags == nil {
		it.AutoTags = []string{}
	}
	normalizeIngest(&it.Ingest)
	return it, nil
}

func normalizeIngest(in *model.Ingest) {
	for _, s := range []*model.Stage{&in.Fetch, &in.Transcribe, &in.Understand, &in.Index} {
		if s.Status == "" {
			s.Status = "pending"
		}
	}
}

type NewItem struct {
	UserID       int64
	Kind         string
	SourceURL    *string
	CanonicalURL *string
	Platform     *string
	Title        *string
	UserNote     string
	Body         string
	Tags         []string
	FileKey      *string
	FileName     *string
	FileMime     *string
	FileSize     *int64
	Ingest       model.Ingest
}

func (r *ItemRepo) Create(ctx context.Context, n NewItem) (model.Item, error) {
	if n.Tags == nil {
		n.Tags = []string{}
	}
	ingest, _ := json.Marshal(n.Ingest)
	var id int64
	err := r.db.QueryRow(ctx, `INSERT INTO items (user_id, kind, source_url, canonical_url, platform, title, user_note, body, tags, file_key, file_name, file_mime, file_size, ingest)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		n.UserID, n.Kind, n.SourceURL, n.CanonicalURL, n.Platform, n.Title, n.UserNote, n.Body, n.Tags, n.FileKey, n.FileName, n.FileMime, n.FileSize, ingest).Scan(&id)
	if err != nil {
		return model.Item{}, err
	}
	return r.ByID(ctx, n.UserID, id)
}

func (r *ItemRepo) ByID(ctx context.Context, userID, id int64) (model.Item, error) {
	return scanItem(r.db.QueryRow(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id = $1 AND i.user_id = $2 AND i.deleted_at IS NULL`, id, userID))
}

// ByIDAnyState dipakai worker: item yang sudah dihapus tetap bisa dibaca untuk membatalkan kerja.
func (r *ItemRepo) ByIDAnyState(ctx context.Context, userID, id int64) (model.Item, bool, error) {
	var deleted *time.Time
	row := r.db.QueryRow(ctx, `SELECT `+itemCols+`, i.deleted_at FROM items i WHERE i.id = $1 AND i.user_id = $2`, id, userID)
	var it model.Item
	var keyPoints, entities, ingest []byte
	err := row.Scan(&it.ID, &it.UserID, &it.Kind, &it.Status, &it.SourceURL, &it.CanonicalURL, &it.Platform, &it.ContentType, &it.Title, &it.Description,
		&it.ThumbnailKey, &it.UserNote, &it.Body, &it.Summary, &keyPoints, &entities, &it.Tags, &it.AutoTags, &it.Language, &it.ContentText,
		&it.FileKey, &it.FileName, &it.FileMime, &it.FileSize, &it.AttachmentKey, &it.DurationSec, &it.PublishedAt, &it.ContentVersion, &ingest,
		&it.RemindersCount, &it.CreatedAt, &it.UpdatedAt, &deleted)
	if err != nil {
		return it, false, mapErr(err)
	}
	_ = json.Unmarshal(keyPoints, &it.KeyPoints)
	_ = json.Unmarshal(entities, &it.Entities)
	_ = json.Unmarshal(ingest, &it.Ingest)
	normalizeIngest(&it.Ingest)
	return it, deleted != nil, nil
}

type ItemFilter struct {
	Kind, Platform, Tag, Status string
	HasReminder                 *bool
}

func (r *ItemRepo) List(ctx context.Context, userID int64, f ItemFilter, cursor int64, limit int) ([]model.Item, *int64, error) {
	args := []any{userID}
	where := []string{"i.user_id = $1", "i.deleted_at IS NULL"}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Kind != "" {
		add("i.kind = $%d", f.Kind)
	}
	if f.Platform != "" {
		add("i.platform = $%d", f.Platform)
	}
	if f.Status != "" {
		add("i.status = $%d", f.Status)
	}
	if f.Tag != "" {
		add("($%d = ANY(i.tags) OR $%[1]d = ANY(i.auto_tags))", f.Tag)
	}
	if f.HasReminder != nil {
		if *f.HasReminder {
			where = append(where, "EXISTS (SELECT 1 FROM reminders r WHERE r.item_id = i.id AND r.status IN ('suggested','pending','sent'))")
		} else {
			where = append(where, "NOT EXISTS (SELECT 1 FROM reminders r WHERE r.item_id = i.id AND r.status IN ('suggested','pending','sent'))")
		}
	}
	if cursor > 0 {
		add("i.id < $%d", cursor)
	}
	args = append(args, limit+1)
	q := `SELECT ` + itemCols + ` FROM items i WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY i.id DESC LIMIT $%d", len(args))
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *int64
	if len(out) > limit {
		out = out[:limit]
		id := out[len(out)-1].ID
		next = &id
	}
	return out, next, nil
}

func (r *ItemRepo) UpdatedSince(ctx context.Context, userID int64, since time.Time, limit int) ([]model.Item, error) {
	rows, err := r.db.Query(ctx, `SELECT `+itemCols+` FROM items i WHERE i.user_id = $1 AND i.deleted_at IS NULL AND i.updated_at > $2 ORDER BY i.updated_at ASC LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *ItemRepo) ByIDs(ctx context.Context, userID int64, ids []int64) (map[int64]model.Item, error) {
	out := map[int64]model.Item{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT `+itemCols+` FROM items i WHERE i.user_id = $1 AND i.deleted_at IS NULL AND i.id = ANY($2)`, userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out[it.ID] = it
	}
	return out, rows.Err()
}

type ItemPatch struct {
	UserNote *string
	Tags     []string
	Title    *string
}

// Patch mengubah field milik user dan menaikkan content_version karena teks pencarian berubah.
func (r *ItemRepo) Patch(ctx context.Context, userID, id int64, p ItemPatch) (model.Item, error) {
	_, err := r.db.Exec(ctx, `UPDATE items SET user_note = COALESCE($3, user_note), tags = COALESCE($4, tags), title = COALESCE($5, title),
		content_version = content_version + 1, updated_at = now() WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		id, userID, p.UserNote, p.Tags, p.Title)
	if err != nil {
		return model.Item{}, err
	}
	return r.ByID(ctx, userID, id)
}

func (r *ItemRepo) SoftDelete(ctx context.Context, userID, id int64) error {
	t, err := r.db.Exec(ctx, `UPDATE items SET deleted_at = now(), updated_at = now() WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	_, _ = r.db.Exec(ctx, `UPDATE reminders SET status = 'cancelled', updated_at = now() WHERE item_id = $1 AND user_id = $2 AND status IN ('suggested','pending')`, id, userID)
	return nil
}

// PurgeDeleted menghapus permanen item yang sudah di sampah lebih lama dari retensi.
func (r *ItemRepo) PurgeDeleted(ctx context.Context, olderThan time.Duration) ([]model.Item, error) {
	rows, err := r.db.Query(ctx, `SELECT `+itemCols+` FROM items i WHERE i.deleted_at IS NOT NULL AND i.deleted_at < now() - $1::interval LIMIT 200`, olderThan.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, it := range out {
		if _, err := r.db.Exec(ctx, `DELETE FROM items WHERE id = $1`, it.ID); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ---- dipakai worker ----

// BumpVersion menaikkan content_version dan mereset tahap yang diminta ke pending (reprocess).
func (r *ItemRepo) BumpVersion(ctx context.Context, userID, id int64, stages []string) (model.Item, error) {
	it, err := r.ByID(ctx, userID, id)
	if err != nil {
		return it, err
	}
	in := it.Ingest
	for _, s := range stages {
		switch s {
		case "fetch":
			in.Fetch = model.Stage{Status: "pending"}
		case "transcribe":
			in.Transcribe = model.Stage{Status: "pending"}
		case "understand":
			in.Understand = model.Stage{Status: "pending"}
		case "index":
			in.Index = model.Stage{Status: "pending"}
		}
	}
	b, _ := json.Marshal(in)
	_, err = r.db.Exec(ctx, `UPDATE items SET content_version = content_version + 1, status = 'queued', ingest = $3, updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID, b)
	if err != nil {
		return it, err
	}
	return r.ByID(ctx, userID, id)
}

func (r *ItemRepo) SetAttachment(ctx context.Context, userID, id int64, key string) error {
	_, err := r.db.Exec(ctx, `UPDATE items SET attachment_key = $3, updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID, key)
	return err
}

// SetStage menulis status satu tahap + status item, hanya bila versi masih sama (idempoten, ADR-006).
func (r *ItemRepo) SetStage(ctx context.Context, userID, id int64, version int32, stage string, st model.Stage, itemStatus string) error {
	stB, _ := json.Marshal(st)
	q := `UPDATE items SET ingest = jsonb_set(coalesce(ingest,'{}'::jsonb), ARRAY[$4::text], $5::jsonb, true), updated_at = now()`
	args := []any{id, userID, version, stage, stB}
	if itemStatus != "" {
		q += `, status = $6`
		args = append(args, itemStatus)
	}
	q += ` WHERE id = $1 AND user_id = $2 AND content_version = $3`
	_, err := r.db.Exec(ctx, q, args...)
	return err
}

type FetchResultPatch struct {
	CanonicalURL, Platform, ContentType, Title, Description, Language *string
	ThumbnailKey                                                      *string
	ContentText                                                       *string
	DurationSec                                                       int
	PublishedAt                                                       *time.Time
}

func (r *ItemRepo) ApplyFetch(ctx context.Context, userID, id int64, version int32, p FetchResultPatch) error {
	_, err := r.db.Exec(ctx, `UPDATE items SET
		canonical_url = COALESCE($4, canonical_url), platform = COALESCE($5, platform), content_type = COALESCE($6, content_type),
		title = COALESCE(NULLIF(title,''), $7, title), description = COALESCE($8, description), language = COALESCE($9, language),
		thumbnail_key = COALESCE($10, thumbnail_key), content_text = COALESCE($11, content_text),
		duration_sec = GREATEST(duration_sec, $12), published_at = COALESCE($13, published_at), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND content_version = $3`,
		id, userID, version, p.CanonicalURL, p.Platform, p.ContentType, p.Title, p.Description, p.Language, p.ThumbnailKey, p.ContentText, p.DurationSec, p.PublishedAt)
	return err
}

func (r *ItemRepo) SetContentText(ctx context.Context, userID, id int64, version int32, text string, title *string) error {
	_, err := r.db.Exec(ctx, `UPDATE items SET content_text = $4, title = COALESCE(NULLIF(title,''), $5, title), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND content_version = $3`, id, userID, version, text, title)
	return err
}

type UnderstandPatch struct {
	Title     *string
	Summary   string
	KeyPoints []string
	Entities  model.Entities
	AutoTags  []string
	Language  *string
}

func (r *ItemRepo) ApplyUnderstand(ctx context.Context, userID, id int64, version int32, p UnderstandPatch) error {
	kp, _ := json.Marshal(p.KeyPoints)
	en, _ := json.Marshal(p.Entities)
	_, err := r.db.Exec(ctx, `UPDATE items SET title = COALESCE(NULLIF(title,''), $4, title), summary = $5, key_points = $6, entities = $7,
		auto_tags = $8, language = COALESCE($9, language), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND content_version = $3`, id, userID, version, p.Title, p.Summary, kp, en, p.AutoTags, p.Language)
	return err
}

func (r *ItemRepo) SetEmbedding(ctx context.Context, userID, id int64, version int32, emb []float32, modelName string) error {
	_, err := r.db.Exec(ctx, `UPDATE items SET embedding = $4, embedding_model = $5 WHERE id = $1 AND user_id = $2 AND content_version = $3`,
		id, userID, version, pgvector.NewVector(padVector(emb)), modelName)
	return err
}

func (r *ItemRepo) Related(ctx context.Context, userID, id int64, limit int) ([]model.Item, error) {
	rows, err := r.db.Query(ctx, `SELECT `+itemCols+` FROM items i, (SELECT embedding FROM items WHERE id = $2 AND user_id = $1) me
		WHERE i.user_id = $1 AND i.deleted_at IS NULL AND i.id <> $2 AND i.embedding IS NOT NULL AND me.embedding IS NOT NULL
		ORDER BY i.embedding <=> me.embedding LIMIT $3`, userID, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Stuck mengembalikan item yang masih queued/processing lebih lama dari batas (safety net).
func (r *ItemRepo) Stuck(ctx context.Context, olderThan time.Duration, limit int) ([]model.Item, error) {
	rows, err := r.db.Query(ctx, `SELECT `+itemCols+` FROM items i WHERE i.deleted_at IS NULL AND i.status IN ('queued','processing') AND i.updated_at < now() - $1::interval LIMIT $2`, olderThan.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AllForExport membaca semua item user (tanpa pagination) untuk ekspor.
func (r *ItemRepo) AllForExport(ctx context.Context, userID int64) ([]model.Item, error) {
	rows, err := r.db.Query(ctx, `SELECT `+itemCols+` FROM items i WHERE i.user_id = $1 AND i.deleted_at IS NULL ORDER BY i.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// StorageKeys mengembalikan semua key object storage milik user (untuk hapus akun).
func (r *ItemRepo) StorageKeys(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT unnest(ARRAY[thumbnail_key, file_key, attachment_key]) FROM items WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k *string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k != nil && *k != "" {
			out = append(out, *k)
		}
	}
	return out, rows.Err()
}

// FullText untuk /search tanpa LLM: BM25-ish via ts_rank di tabel items + chunk.
func (r *ItemRepo) FullText(ctx context.Context, userID int64, query string, f ItemFilter, limit int) ([]model.SearchHit, error) {
	args := []any{userID, query, limit}
	extra := ""
	if f.Kind != "" {
		args = append(args, f.Kind)
		extra += fmt.Sprintf(" AND i.kind = $%d", len(args))
	}
	if f.Platform != "" {
		args = append(args, f.Platform)
		extra += fmt.Sprintf(" AND i.platform = $%d", len(args))
	}
	q := `WITH q AS (SELECT websearch_to_tsquery('simple', $2) AS tsq),
	item_hits AS (
	  SELECT i.id, ts_rank(i.tsv, q.tsq) AS score, ts_headline('simple', coalesce(i.summary, i.user_note, ''), q.tsq, 'MaxWords=25, MinWords=10') AS snippet
	  FROM items i, q WHERE i.user_id = $1 AND i.deleted_at IS NULL AND i.tsv @@ q.tsq` + extra + `
	),
	chunk_hits AS (
	  SELECT c.item_id AS id, max(ts_rank(c.tsv, q.tsq)) * 0.8 AS score,
	         (array_agg(ts_headline('simple', c.text, q.tsq, 'MaxWords=25, MinWords=10') ORDER BY ts_rank(c.tsv, q.tsq) DESC))[1] AS snippet
	  FROM item_chunks c JOIN items i ON i.id = c.item_id, q
	  WHERE c.user_id = $1 AND i.deleted_at IS NULL AND c.tsv @@ q.tsq` + extra + `
	  GROUP BY c.item_id
	),
	merged AS (
	  SELECT id, max(score) AS score, (array_agg(snippet ORDER BY score DESC))[1] AS snippet
	  FROM (SELECT * FROM item_hits UNION ALL SELECT * FROM chunk_hits) u GROUP BY id
	)
	SELECT ` + itemCols + `, m.score, m.snippet FROM merged m JOIN items i ON i.id = m.id ORDER BY m.score DESC, i.id DESC LIMIT $3`
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SearchHit
	for rows.Next() {
		var it model.Item
		var keyPoints, entities, ingest []byte
		var score float64
		var snippet string
		err := rows.Scan(&it.ID, &it.UserID, &it.Kind, &it.Status, &it.SourceURL, &it.CanonicalURL, &it.Platform, &it.ContentType, &it.Title, &it.Description,
			&it.ThumbnailKey, &it.UserNote, &it.Body, &it.Summary, &keyPoints, &entities, &it.Tags, &it.AutoTags, &it.Language, &it.ContentText,
			&it.FileKey, &it.FileName, &it.FileMime, &it.FileSize, &it.AttachmentKey, &it.DurationSec, &it.PublishedAt, &it.ContentVersion, &ingest,
			&it.RemindersCount, &it.CreatedAt, &it.UpdatedAt, &score, &snippet)
		if err != nil {
			return nil, err
		}
		out = append(out, model.SearchHit{Item: it.Card(), Snippet: snippet, Score: score})
	}
	return out, rows.Err()
}
