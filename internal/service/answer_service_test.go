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
}

func (f *fakeSearcher) Search(_ context.Context, userID int64, _ string, _ model.NoteFilter, _ int) ([]model.SearchHit, string, error) {
	f.userID = userID
	return f.hits, SearchModeHybrid, nil
}

type fakeAnswerer struct {
	got  []aiagent.Source
	resp aiagent.Answer
}

func (f *fakeAnswerer) Answer(_ context.Context, _ string, src []aiagent.Source) (aiagent.Answer, error) {
	f.got = src
	return f.resp, nil
}

func (f *fakeAnswerer) Ask(context.Context, string) (string, error) { return "umum", nil }

func TestAnswerServiceCommandTidakDikirimKeLLM(t *testing.T) {
	title := "restart api"
	search := &fakeSearcher{hits: []model.SearchHit{
		{Note: model.Note{ID: 1, Type: model.NoteTypeNote, Body: "9router di :20128"}},
		{Note: model.Note{ID: 2, Type: model.NoteTypeCommand, Title: &title, Body: "docker compose up -d api"}},
	}}
	ai := &fakeAnswerer{resp: aiagent.Answer{Text: "pakai command restart api", UsedIDs: []int64{2}, FromNotes: true}}
	a, err := NewAnswerService(search, ai).Answer(context.Background(), 7, "restart api?")
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
