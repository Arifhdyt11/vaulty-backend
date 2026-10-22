package service

import (
	"context"
	"log/slog"
	"strings"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/ai"
)

type SearchService struct {
	repos    *repository.Repos
	items    *ItemService
	embedder ai.Embedder
	aiOn     bool
}

func NewSearchService(repos *repository.Repos, items *ItemService, embedder ai.Embedder, aiOn bool) *SearchService {
	return &SearchService{repos: repos, items: items, embedder: embedder, aiOn: aiOn}
}

// Quick: pencarian tanpa LLM untuk layar search (full-text saja, tidak dihitung kuota).
func (s *SearchService) Quick(ctx context.Context, u model.User, q string, f repository.ItemFilter, limit int) ([]model.SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []model.SearchHit{}, nil
	}
	hits, err := s.repos.Items.FullText(ctx, u.ID, q, f, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 && s.aiOn && s.embedder != nil {
		// Tanpa kecocokan kata → coba semantik lewat chunk.
		ch, err := s.Chunks(ctx, u, q, limit)
		if err == nil {
			seen := map[int64]bool{}
			for _, h := range ch {
				if seen[h.Item.ID] {
					continue
				}
				seen[h.Item.ID] = true
				hits = append(hits, model.SearchHit{Item: s.items.Card(ctx, h.Item), Snippet: ai.Truncate(h.Chunk.Text, 160), Score: h.Score})
			}
		}
	}
	for i := range hits {
		if hits[i].Item.ThumbnailURL == nil {
			// FullText mengembalikan Card tanpa URL bertanda tangan; lengkapi.
			if it, err := s.repos.Items.ByID(ctx, u.ID, hits[i].Item.ID); err == nil {
				hits[i].Item = s.items.Card(ctx, it)
			}
		}
	}
	if hits == nil {
		hits = []model.SearchHit{}
	}
	return hits, nil
}

// Chunks: hybrid search level chunk untuk RAG (ADR-003).
func (s *SearchService) Chunks(ctx context.Context, u model.User, q string, k int) ([]model.ChunkHit, error) {
	var emb []float32
	if s.aiOn && s.embedder != nil {
		v, err := s.embedder.Embed(ctx, []string{q})
		if err != nil {
			slog.WarnContext(ctx, "embed query gagal, full-text saja", "err", err)
		} else if len(v) == 1 {
			emb = v[0]
		}
	}
	return s.repos.Chunks.Hybrid(ctx, u.ID, searchQuery(q), emb, k)
}

// searchQuery: websearch_to_tsquery meng-AND semua kata; untuk pertanyaan natural, buang kata tanya
// dan gabungkan dengan OR supaya recall tinggi (vektor + RRF yang menyusun peringkat).
func searchQuery(q string) string {
	stop := map[string]bool{"apa": true, "yang": true, "saya": true, "aku": true, "pernah": true, "simpan": true, "catat": true, "tentang": true, "soal": true,
		"itu": true, "ini": true, "di": true, "ke": true, "dari": true, "dan": true, "atau": true, "kemarin": true, "dulu": true, "tolong": true,
		"gimana": true, "bagaimana": true, "kenapa": true, "mengapa": true, "siapa": true, "kapan": true, "dimana": true, "mana": true, "ada": true,
		"the": true, "what": true, "about": true, "did": true, "i": true, "save": true, "is": true, "are": true, "a": true, "an": true, "of": true, "to": true}
	var words []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, "?!.,;:\"'()")
		w = strings.TrimSuffix(w, "nya")
		if len(w) < 2 || stop[w] {
			continue
		}
		words = append(words, w)
	}
	if len(words) == 0 {
		return q
	}
	return strings.Join(words, " or ")
}
