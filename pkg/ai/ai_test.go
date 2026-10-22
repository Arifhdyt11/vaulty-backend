package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseJSONLoose(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	for _, s := range []string{`{"a":1}`, "Berikut hasilnya:\n```json\n{\"a\":1}\n```", "```\n{\"a\": 1}\n```\nselesai"} {
		if err := ParseJSONLoose(s, &v); err != nil || v.A != 1 {
			t.Fatalf("%q: %v %+v", s, err, v)
		}
	}
	if err := ParseJSONLoose("tidak ada json", &v); err == nil {
		t.Fatal("harus error")
	}
}

func TestStreamDanComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header hilang")
		}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hal\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"o\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Halo"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	c := New(Config{Chat: Endpoint{BaseURL: srv.URL, APIKey: "k"}, ChatModel: "m", EmbedDim: 3})
	var got strings.Builder
	resp, err := c.Stream(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hai"}}}, func(d string) error { got.WriteString(d); return nil })
	if err != nil || got.String() != "Halo" || resp.Content != "Halo" {
		t.Fatalf("stream: %v %q", err, got.String())
	}
	r2, err := c.Complete(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hai"}}})
	if err != nil || r2.Content != "Halo" || r2.PromptTokens != 3 {
		t.Fatalf("complete: %v %+v", err, r2)
	}
}

func TestEmbedDimensi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[1,2,3]},{"index":0,"embedding":[4,5,6]}]}`))
	}))
	defer srv.Close()
	c := New(Config{Chat: Endpoint{BaseURL: srv.URL}, EmbedDim: 3})
	out, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil || out[0][0] != 4 || out[1][0] != 1 {
		t.Fatalf("%v %v", err, out)
	}
	c2 := New(Config{Chat: Endpoint{BaseURL: srv.URL}, EmbedDim: 4})
	if _, err := c2.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("dimensi salah harus error")
	}
}

func TestDisabled(t *testing.T) {
	c := New(Config{})
	if _, err := c.Complete(context.Background(), ChatRequest{}); err != ErrDisabled {
		t.Fatal(err)
	}
}
