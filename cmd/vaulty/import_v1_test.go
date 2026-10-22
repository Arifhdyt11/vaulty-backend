package main

import (
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func TestMapV1NoteLinkJadiURL(t *testing.T) {
	n := v1Note{ID: 1, Type: "link", Title: sp("9router"), Body: "dashboard router AI", URL: sp("https://youtu.be/dQw4w9WgXcQ"), Project: sp("Sipantas"), CreatedAt: time.Now()}
	ni, reason := mapV1Note(7, n)
	if reason != "" {
		t.Fatal(reason)
	}
	if ni.Kind != "url" || ni.Platform == nil || *ni.Platform != "youtube" || ni.UserNote != "dashboard router AI" {
		t.Fatalf("%+v", ni)
	}
	if ni.Ingest.Fetch.Status != "pending" {
		t.Fatal("link harus di-fetch ulang")
	}
	want := map[string]bool{"sipantas": true, "impor-v1": true}
	for _, tg := range ni.Tags {
		delete(want, tg)
	}
	if len(want) != 0 {
		t.Fatalf("tag kurang: %v dari %v", want, ni.Tags)
	}
}

func TestMapV1NoteCommandVerbatim(t *testing.T) {
	cmd := "docker compose -f docker-compose.yml up -d --build"
	ni, reason := mapV1Note(7, v1Note{ID: 2, Type: "command", Title: sp("Deploy"), Body: cmd})
	if reason != "" {
		t.Fatal(reason)
	}
	if ni.Kind != "text" || ni.Body != cmd || ni.Ingest.Fetch.Status != "skipped" {
		t.Fatalf("%+v", ni)
	}
	found := false
	for _, tg := range ni.Tags {
		if tg == "command" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tag command hilang: %v", ni.Tags)
	}
}

func TestMapV1NoteCredentialDilewati(t *testing.T) {
	_, reason := mapV1Note(7, v1Note{ID: 3, Type: "note", Body: "api_key=abc123"})
	if reason == "" {
		t.Fatal("catatan berisi credential harus dilewati")
	}
}

func TestMapV1NoteKosongDilewati(t *testing.T) {
	if _, reason := mapV1Note(7, v1Note{ID: 4, Type: "note"}); reason == "" {
		t.Fatal("catatan kosong harus dilewati")
	}
}
