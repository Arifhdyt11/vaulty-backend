package service

import (
	"context"
	"fmt"

	"vaulty-api/internal/model"
	"vaulty-api/pkg/aiagent"
)

// NoteSearcher dipenuhi *SearchService.
type NoteSearcher interface {
	Search(ctx context.Context, userID int64, q string, f model.NoteFilter, limit int) ([]model.SearchHit, string, error)
}

// AnswerService: Vee menjawab pertanyaan dari catatan user (RAG): hybrid search → top-k → LLM.
// Dipanggil bot Telegram (proses sendiri), bukan request HTTP, jadi boleh menunggu LLM.
type AnswerService struct {
	search NoteSearcher
	ai     aiagent.Answerer
}

func NewAnswerService(search NoteSearcher, ai aiagent.Answerer) *AnswerService {
	return &AnswerService{search: search, ai: ai}
}

// answerTopK kecil supaya prompt muat di model gratis dan jawaban fokus.
const answerTopK = 5

// Answer mencari catatan yang relevan lalu meminta LLM menjawab hanya dari catatan itu.
func (s *AnswerService) Answer(ctx context.Context, userID int64, question string) (model.Answer, error) {
	hits, _, err := s.search.Search(ctx, userID, question, model.NoteFilter{}, answerTopK)
	if err != nil {
		return model.Answer{}, fmt.Errorf("cari konteks jawaban: %w", err)
	}
	byID := make(map[int64]model.Note, len(hits))
	sources := make([]aiagent.Source, 0, len(hits))
	for _, h := range hits {
		byID[h.Note.ID] = h.Note
		sources = append(sources, toSource(h.Note))
	}
	a, err := s.ai.Answer(ctx, question, sources)
	if err != nil {
		return model.Answer{}, fmt.Errorf("jawab pertanyaan: %w", err)
	}
	out := model.Answer{Text: a.Text, FromNotes: a.FromNotes}
	for _, id := range a.UsedIDs {
		out.Sources = append(out.Sources, byID[id])
	}
	return out, nil
}

// Ask: pertanyaan umum ke LLM, tanpa catatan user.
func (s *AnswerService) Ask(ctx context.Context, question string) (string, error) {
	text, err := s.ai.Ask(ctx, question)
	if err != nil {
		return "", fmt.Errorf("tanya AI: %w", err)
	}
	return text, nil
}

func toSource(n model.Note) aiagent.Source {
	src := aiagent.Source{ID: n.ID, Type: n.Type}
	if n.Title != nil {
		src.Title = *n.Title
	}
	if n.URL != nil {
		src.URL = *n.URL
	}
	// ADR-010: isi command tidak dikirim ke LLM; LLM cukup memilih id-nya, body ditampilkan verbatim.
	if n.Type != model.NoteTypeCommand {
		src.Content = n.Body
	}
	return src
}
