package service

import (
	"context"
	"log/slog"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/aiagent"
)

// SearchService: hybrid search (vector + full-text, RRF) yang di-scope user_id (ADR-002).
type SearchService struct {
	notes          *repository.NoteRepository
	embedder       aiagent.Embedder
	maxDistance    float64
	relativeMargin float64
}

// maxDistance & relativeMargin: batas jarak cosine kandidat semantic (lihat .env SEARCH_*).
func NewSearchService(notes *repository.NoteRepository, embedder aiagent.Embedder, maxDistance, relativeMargin float64) *SearchService {
	return &SearchService{
		notes:          notes,
		embedder:       embedder,
		maxDistance:    maxDistance,
		relativeMargin: relativeMargin,
	}
}

const (
	SearchModeHybrid   = "hybrid"
	SearchModeFulltext = "fulltext"
)

// Search mengembalikan hasil dan mode yang dipakai. Jika embedding gagal atau AI tidak
// dikonfigurasi, otomatis turun ke full-text saja.
func (s *SearchService) Search(ctx context.Context, userID int64, q string, f model.NoteFilter, limit int) ([]model.SearchHit, string, error) {
	var vec []float32
	mode := SearchModeFulltext
	if s.embedder.Enabled() {
		emb, err := s.embedder.Embed(ctx, q)
		if err != nil {
			slog.WarnContext(ctx, "embedding query gagal, pakai full-text", "err", err)
		} else if emb != nil {
			vec, mode = emb, SearchModeHybrid
		}
	}
	hits, err := s.notes.Search(ctx, repository.SearchParams{
		UserID:         userID,
		Query:          q,
		Filter:         f,
		QueryVec:       vec,
		EmbeddingModel: s.embedder.Model(),
		MaxDistance:    s.maxDistance,
		RelativeMargin: s.relativeMargin,
		Limit:          limit,
	})
	return hits, mode, err
}
