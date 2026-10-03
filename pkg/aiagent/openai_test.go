package aiagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// fakeServer mencatat path + Authorization yang diterima, lalu membalas body tetap.
func fakeServer(t *testing.T, body string, got *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = append(*got, r.URL.Path+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAIEmbedDanChatKeServerBerbeda(t *testing.T) {
	var embedHits, chatHits []string
	emb := fakeServer(t, `{"data":[{"embedding":[0.6,0.8]}]}`, &embedHits)
	chat := fakeServer(t, `{"choices":[{"message":{"content":"{\"tags\":[\"nginx\"]}"}}]}`, &chatHits)

	o := NewOpenAI(Endpoint{BaseURL: emb.URL, APIKey: "k-embed", Model: "e"},
		Endpoint{BaseURL: chat.URL, APIKey: "k-chat", Model: "c"})
	ctx := context.Background()
	if _, err := o.Embed(ctx, "teks"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if _, err := o.SuggestTags(ctx, "teks"); err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}
	if want := []string{"/embeddings Bearer k-embed"}; !reflect.DeepEqual(embedHits, want) {
		t.Errorf("server embedding menerima %v; want %v", embedHits, want)
	}
	if want := []string{"/chat/completions Bearer k-chat"}; !reflect.DeepEqual(chatHits, want) {
		t.Errorf("server chat menerima %v; want %v", chatHits, want)
	}
	if got, want := o.Model(), emb.URL+"#e"; got != want {
		t.Errorf("Model() = %q; want %q", got, want)
	}
}

func TestSuggestTagsJawabanTidakMurniJSON(t *testing.T) {
	cases := map[string][]string{
		`{"tags":["docker"]}`:                               {"docker"},
		"```json\n{\"tags\": [\"Nginx\", \"docker\"]}\n```": {"nginx", "docker"},
		`Berikut tag-nya: {"tags":["go"]} semoga membantu`:  {"go"},
	}
	for content, want := range cases {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
		var hits []string
		srv := fakeServer(t, string(b), &hits)
		o := NewOpenAI(Endpoint{}, Endpoint{BaseURL: srv.URL, Model: "c"})
		got, err := o.SuggestTags(context.Background(), "teks")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("SuggestTags(%q) = %v, %v; want %v", content, got, err, want)
		}
	}
}

func TestSuggestTagsResponsDenganSisaDONE(t *testing.T) {
	var hits []string
	body := `{"choices":[{"message":{"content":"{\"tags\":[\"mcp\"]}"}}]}` + "\ndata: [DONE]\n"
	srv := fakeServer(t, body, &hits)
	o := NewOpenAI(Endpoint{}, Endpoint{BaseURL: srv.URL, Model: "c"})
	got, err := o.SuggestTags(context.Background(), "teks")
	if err != nil || !reflect.DeepEqual(got, []string{"mcp"}) {
		t.Fatalf("SuggestTags = %v, %v; want [mcp]", got, err)
	}
}

func TestSuggestTagsTanpaJSONError(t *testing.T) {
	var hits []string
	srv := fakeServer(t, `{"choices":[{"message":{"content":"maaf, tidak bisa"}}]}`, &hits)
	o := NewOpenAI(Endpoint{}, Endpoint{BaseURL: srv.URL, Model: "c"})
	if _, err := o.SuggestTags(context.Background(), "teks"); err == nil {
		t.Fatal("jawaban tanpa JSON harus error")
	}
}
