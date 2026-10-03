package service

import (
	"context"
	"testing"

	"vaulty-api/internal/model"
	"vaulty-api/pkg/aiagent"
)

type fakeSearcher struct {
	hits   []model.SearchHit
	userID int64
	q      string
}

func (f *fakeSearcher) Search(_ context.Context, userID int64, q string, _ model.NoteFilter, _ int) ([]model.SearchHit, string, error) {
	f.userID, f.q = userID, q
	return f.hits, SearchModeHybrid, nil
}

type fakeAnswerer struct {
	got     []aiagent.Source
	history []aiagent.Turn
	resp    aiagent.Answer
}

func (f *fakeAnswerer) Answer(_ context.Context, _ string, src []aiagent.Source, h []aiagent.Turn) (aiagent.Answer, error) {
	f.got, f.history = src, h
	return f.resp, nil
}

func (f *fakeAnswerer) Ask(_ context.Context, _ string, h []aiagent.Turn) (string, error) {
	f.history = h
	return "umum", nil
}

func TestAnswerServiceCommandTidakDikirimKeLLM(t *testing.T) {
	title := "restart api"
	search := &fakeSearcher{hits: []model.SearchHit{
		{Note: model.Note{ID: 1, Type: model.NoteTypeNote, Body: "9router di :20128"}},
		{Note: model.Note{ID: 2, Type: model.NoteTypeCommand, Title: &title, Body: "docker compose up -d api"}},
	}}
	ai := &fakeAnswerer{resp: aiagent.Answer{Text: "pakai command restart api", UsedIDs: []int64{2}, FromNotes: true}}
	a, err := NewAnswerService(search, ai).Answer(context.Background(), 7, "restart api?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if search.userID != 7 {
		t.Errorf("search harus di-scope user 7, dapat %d", search.userID)
	}
	if ai.got[0].Content != "9router di :20128" || ai.got[1].Content != "" || ai.got[1].Title != "restart api" {
		t.Errorf("sources ke LLM = %+v", ai.got)
	}
	if len(a.Sources) != 1 || a.Sources[0].Body != "docker compose up -d api" || !a.FromNotes {
		t.Errorf("answer = %+v", a)
	}
}

func TestAnswerServicePertanyaanLanjutan(t *testing.T) {
	search, ai := &fakeSearcher{}, &fakeAnswerer{resp: aiagent.Answer{Text: "5433"}}
	history := []model.ChatTurn{{Role: "user", Content: "database sipantas apa"}, {Role: "assistant", Content: "PostgreSQL 17"}}
	if _, err := NewAnswerService(search, ai).Answer(context.Background(), 7, "port-nya berapa", history); err != nil {
		t.Fatal(err)
	}
	if search.q != "database or sipantas or port" {
		t.Errorf("query search = %q; harus menyertakan pertanyaan sebelumnya", search.q)
	}
	if len(ai.history) != 2 || ai.history[1].Content != "PostgreSQL 17" {
		t.Errorf("riwayat ke LLM = %+v", ai.history)
	}
}

func TestSearchQuery(t *testing.T) {
	cases := map[string]string{
		"database sipantas apa":                  "database or sipantas",
		"berikan saya url untuk membuka 9router": "url or membuka or 9router",
		"di sipantas databasenya apa?":           "sipantas or database",
		"apa":                                    "apa",
		"cara restart docker-compose di ~/apps":  "restart or docker-compose or apps",
	}
	for in, want := range cases {
		if got := searchQuery(in); got != want {
			t.Errorf("searchQuery(%q) = %q; want %q", in, got, want)
		}
	}
}
