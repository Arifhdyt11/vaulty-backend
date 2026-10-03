package service

import (
	"context"
	"fmt"
	"strings"
	"unicode"

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

// answerTopK: cukup lebar supaya note yang relevan tidak terlewat ranking, tapi prompt tetap kecil.
const answerTopK = 8

// Answer mencari catatan yang relevan lalu meminta LLM menjawab hanya dari catatan itu.
func (s *AnswerService) Answer(ctx context.Context, userID int64, question string) (model.Answer, error) {
	hits, _, err := s.search.Search(ctx, userID, searchQuery(question), model.NoteFilter{}, answerTopK)
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

// Kata tanya/perintah yang tidak membantu pencarian.
var stopwords = map[string]bool{
	"apa": true, "apakah": true, "apa-apa": true, "bagaimana": true, "gimana": true, "gmn": true, "cara": true,
	"berikan": true, "kasih": true, "tolong": true, "coba": true, "saya": true, "aku": true, "gue": true, "kamu": true,
	"untuk": true, "buat": true, "yang": true, "yg": true, "di": true, "ke": true, "dari": true, "dan": true,
	"atau": true, "itu": true, "ini": true, "ada": true, "mana": true, "dimana": true, "kapan": true, "berapa": true,
	"siapa": true, "kenapa": true, "mengapa": true, "dong": true, "sih": true, "ya": true, "yaa": true, "deh": true,
	"mau": true, "ingin": true, "bisa": true, "lagi": true, "punya": true, "adalah": true, "the": true, "is": true,
	"what": true, "how": true, "to": true, "of": true,
}

// searchQuery mengubah pertanyaan natural menjadi kata kunci "a or b or c" untuk hybrid search.
// websearch_to_tsquery meng-AND semua kata, sehingga kata tanya ("apa", "berikan saya") membuat
// full-text tidak menemukan apa pun. Pertanyaan asli tetap dikirim utuh ke LLM.
func searchQuery(question string) string {
	words := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_./:", r)
	})
	var kw []string
	for _, w := range words {
		w = strings.Trim(w, "-_./:")
		if len(w) > 5 && strings.HasSuffix(w, "nya") {
			w = strings.TrimSuffix(w, "nya") // databasenya → database
		}
		if w != "" && !stopwords[w] {
			kw = append(kw, w)
		}
	}
	if len(kw) == 0 {
		return question
	}
	return strings.Join(kw, " or ")
}
