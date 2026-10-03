package repository

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
	pgvector "github.com/pgvector/pgvector-go"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository/queries"
)

type NoteRepository struct{ q *queries.Queries }

func NewNoteRepository(q *queries.Queries) *NoteRepository { return &NoteRepository{q: q} }

type NewNote struct {
	UserID   int64
	Type     string
	Title    string
	Body     string
	URL      string
	Tags     []string
	Project  string
	Metadata []byte
	FileKey  string
	FileName string
	FileMime string
	FileSize int64
}

func (r *NoteRepository) Create(ctx context.Context, n NewNote) (model.Note, error) {
	row, err := r.q.CreateNote(ctx, queries.CreateNoteParams{
		UserID:   n.UserID,
		Type:     n.Type,
		Title:    text(n.Title),
		Body:     n.Body,
		Url:      text(n.URL),
		Tags:     n.Tags,
		Project:  text(n.Project),
		Metadata: n.Metadata,
		FileKey:  text(n.FileKey),
		FileName: text(n.FileName),
		FileMime: text(n.FileMime),
		FileSize: pgtype.Int8{Int64: n.FileSize, Valid: n.FileKey != ""},
	})
	return toNote(queries.GetNoteRow(row)), err
}

func (r *NoteRepository) FindByID(ctx context.Context, userID, id int64) (model.Note, error) {
	row, err := r.q.GetNote(ctx, queries.GetNoteParams{ID: id, UserID: userID})
	return toNote(row), notFound(err)
}

// FileKey mengembalikan key storage lampiran note ("" jika tidak ada).
func (r *NoteRepository) FileKey(ctx context.Context, userID, id int64) (string, model.Note, error) {
	row, err := r.q.GetNote(ctx, queries.GetNoteParams{ID: id, UserID: userID})
	return row.FileKey.String, toNote(row), notFound(err)
}

func (r *NoteRepository) List(ctx context.Context, userID int64, f model.NoteFilter, beforeID int64, limit int) ([]model.Note, error) {
	rows, err := r.q.ListNotes(ctx, queries.ListNotesParams{
		UserID:   userID,
		Type:     text(f.Type),
		Tag:      text(f.Tag),
		Project:  text(f.Project),
		BeforeID: pgtype.Int8{Int64: beforeID, Valid: beforeID > 0},
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Note, 0, len(rows))
	for _, row := range rows {
		out = append(out, toNote(queries.GetNoteRow(row)))
	}
	return out, nil
}

type NoteUpdate struct {
	ID, UserID     int64
	Type           string
	Title          string
	Body           string
	URL            string
	Tags           []string
	Project        string
	Metadata       []byte
	ContentChanged bool // true: content_version naik dan note di-index ulang
}

func (r *NoteRepository) Update(ctx context.Context, u NoteUpdate) (model.Note, error) {
	row, err := r.q.UpdateNote(ctx, queries.UpdateNoteParams{
		ID:             u.ID,
		UserID:         u.UserID,
		Type:           u.Type,
		Title:          text(u.Title),
		Body:           u.Body,
		Url:            text(u.URL),
		Tags:           u.Tags,
		Project:        text(u.Project),
		Metadata:       u.Metadata,
		ContentChanged: u.ContentChanged,
	})
	return toNote(queries.GetNoteRow(row)), notFound(err)
}

func (r *NoteRepository) SoftDelete(ctx context.Context, userID, id int64) error {
	n, err := r.q.SoftDeleteNote(ctx, queries.SoftDeleteNoteParams{ID: id, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// Requeue menaikkan content_version dan menandai note 'pending' agar di-index ulang.
func (r *NoteRepository) Requeue(ctx context.Context, userID, id int64) (model.Note, error) {
	row, err := r.q.RequeueNote(ctx, queries.RequeueNoteParams{ID: id, UserID: userID})
	return toNote(queries.GetNoteRow(row)), notFound(err)
}

func (r *NoteRepository) Types(ctx context.Context, userID int64) ([]model.TypeCount, error) {
	rows, err := r.q.ListTypes(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.TypeCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.TypeCount{Type: row.Type, Total: row.Total})
	}
	return out, nil
}

type SearchParams struct {
	UserID         int64
	Query          string
	Filter         model.NoteFilter
	QueryVec       []float32 // nil: full-text saja
	EmbeddingModel string
	MaxDistance    float64
	RelativeMargin float64
	Limit          int
}

func (r *NoteRepository) Search(ctx context.Context, p SearchParams) ([]model.SearchHit, error) {
	var vec *pgvector.Vector
	if p.QueryVec != nil {
		v := pgvector.NewVector(p.QueryVec)
		vec = &v
	}
	rows, err := r.q.HybridSearch(ctx, queries.HybridSearchParams{
		UserID:         p.UserID,
		Query:          p.Query,
		QueryVec:       vec,
		EmbeddingModel: text(p.EmbeddingModel),
		MaxDistance:    p.MaxDistance,
		RelativeMargin: p.RelativeMargin,
		ResultLimit:    int32(p.Limit),
		Type:           text(p.Filter.Type),
		Tag:            text(p.Filter.Tag),
		Project:        text(p.Filter.Project),
	})
	if err != nil {
		return nil, err
	}
	hits := make([]model.SearchHit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, model.SearchHit{
			Note: toNote(queries.GetNoteRow{
				ID:             row.ID,
				UserID:         row.UserID,
				Type:           row.Type,
				Title:          row.Title,
				Body:           row.Body,
				Url:            row.Url,
				Tags:           row.Tags,
				AutoTags:       row.AutoTags,
				Project:        row.Project,
				Metadata:       row.Metadata,
				FileKey:        row.FileKey,
				FileName:       row.FileName,
				FileMime:       row.FileMime,
				FileSize:       row.FileSize,
				IndexStatus:    row.IndexStatus,
				ContentVersion: row.ContentVersion,
				CreatedAt:      row.CreatedAt,
				UpdatedAt:      row.UpdatedAt,
			}),
			Score:   row.Score,
			Snippet: row.Snippet,
		})
	}
	return hits, nil
}

// --- Dipakai worker (indexing) ---

func (r *NoteRepository) FindForIndex(ctx context.Context, userID, id int64) (model.IndexableNote, error) {
	row, err := r.q.GetNoteForIndex(ctx, queries.GetNoteForIndexParams{ID: id, UserID: userID})
	if err != nil {
		return model.IndexableNote{}, notFound(err)
	}
	return model.IndexableNote{
		ID:             row.ID,
		UserID:         row.UserID,
		Type:           row.Type,
		Title:          row.Title.String,
		URL:            row.Url.String,
		Body:           row.Body,
		Tags:           row.Tags,
		Project:        row.Project.String,
		FileKey:        row.FileKey.String,
		FileName:       row.FileName.String,
		FileMime:       row.FileMime.String,
		ContentText:    row.ContentText.String,
		HasContentText: row.ContentText.Valid,
		ContentVersion: row.ContentVersion,
	}, nil
}

func (r *NoteRepository) SaveContentText(ctx context.Context, userID, id int64, content string) error {
	return r.q.SaveContentText(ctx, queries.SaveContentTextParams{
		ID:          id,
		UserID:      userID,
		ContentText: pgtype.Text{String: content, Valid: true},
	})
}

// MarkIndexed menyimpan hasil index hanya jika content_version masih sama.
func (r *NoteRepository) MarkIndexed(ctx context.Context, n model.IndexableNote, embedding []float32, embeddingModel string, autoTags []string) error {
	var vec *pgvector.Vector
	if embedding != nil {
		v := pgvector.NewVector(embedding)
		vec = &v
	}
	_, err := r.q.MarkNoteIndexed(ctx, queries.MarkNoteIndexedParams{
		ID:             n.ID,
		UserID:         n.UserID,
		ContentVersion: n.ContentVersion,
		Embedding:      vec,
		EmbeddingModel: text(embeddingModel),
		AutoTags:       nonNil(autoTags),
	})
	return err
}

func (r *NoteRepository) MarkIndexFailed(ctx context.Context, n model.IndexableNote, reason string) error {
	return r.q.MarkNoteIndexFailed(ctx, queries.MarkNoteIndexFailedParams{
		ID:             n.ID,
		UserID:         n.UserID,
		ContentVersion: n.ContentVersion,
		IndexError:     pgtype.Text{String: reason, Valid: true},
	})
}

// PendingNote: referensi note yang tertahan 'pending' (tanpa isi, lintas user; hanya untuk worker).
type PendingNote struct {
	ID, UserID     int64
	ContentVersion int32
}

func (r *NoteRepository) ListPending(ctx context.Context, olderThanSecs float64, limit int) ([]PendingNote, error) {
	rows, err := r.q.ListPendingNotes(ctx, queries.ListPendingNotesParams{OlderThanSecs: olderThanSecs, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]PendingNote, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingNote{ID: row.ID, UserID: row.UserID, ContentVersion: row.ContentVersion})
	}
	return out, nil
}

// RequeueStaleEmbeddings menandai note yang embedding-nya dari model lain (lintas user; hanya worker).
func (r *NoteRepository) RequeueStaleEmbeddings(ctx context.Context, embeddingModel string, limit int) (int64, error) {
	return r.q.RequeueStaleEmbeddings(ctx, queries.RequeueStaleEmbeddingsParams{Model: embeddingModel, RowLimit: int32(limit)})
}

// toNote: semua query note (Create/Get/List/Update/Search) mengembalikan kolom yang sama,
// sehingga tipe row sqlc bisa dikonversi ke queries.GetNoteRow.
func toNote(r queries.GetNoteRow) model.Note {
	n := model.Note{
		ID:             r.ID,
		Type:           r.Type,
		Title:          nullable(r.Title),
		Body:           r.Body,
		URL:            nullable(r.Url),
		Tags:           nonNil(r.Tags),
		AutoTags:       nonNil(r.AutoTags),
		Project:        nullable(r.Project),
		Metadata:       map[string]any{},
		IndexStatus:    r.IndexStatus,
		ContentVersion: r.ContentVersion,
		CreatedAt:      r.CreatedAt.Time,
		UpdatedAt:      r.UpdatedAt.Time,
	}
	_ = json.Unmarshal(r.Metadata, &n.Metadata)
	if r.FileKey.Valid {
		n.File = &model.File{Name: r.FileName.String, Mime: r.FileMime.String, Size: r.FileSize.Int64}
	}
	return n
}
