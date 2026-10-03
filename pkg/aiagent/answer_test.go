package aiagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseAnswer(t *testing.T) {
	src := []Source{{ID: 10}, {ID: 12}}
	cases := []struct {
		name, content string
		want          Answer
	}{
		{"json", `{"answer":"Buka http://x:20128","used_ids":[10],"from_notes":true}`,
			Answer{Text: "Buka http://x:20128", UsedIDs: []int64{10}, FromNotes: true}},
		{"code fence + id string + id asing dibuang", "```json\n{\"answer\":\"a\",\"used_ids\":[\"12\",99,12]}\n```",
			Answer{Text: "a", UsedIDs: []int64{12}, FromNotes: true}},
		{"tidak ada di catatan", `{"answer":"Tidak ada di catatanmu.","used_ids":[],"from_notes":false}`,
			Answer{Text: "Tidak ada di catatanmu."}},
		{"bukan json", "MCP adalah protokol.", Answer{Text: "MCP adalah protokol."}},
	}
	for _, c := range cases {
		got, err := parseAnswer(c.content, src)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: parseAnswer = %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
	if _, err := parseAnswer("  ", src); err == nil {
		t.Error("jawaban kosong harus error")
	}
}

func TestAnswerIsiCommandTidakDikirim(t *testing.T) {
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		prompt = req.Messages[1].Content
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"answer\":\"pakai command restart\",\"used_ids\":[3]}"}}]}`))
	}))
	defer srv.Close()
	o := NewOpenAI(Endpoint{}, Endpoint{BaseURL: srv.URL, Model: "c"})
	// Pemanggil (service) mengosongkan Content untuk command; pastikan judul tetap terkirim.
	a, err := o.Answer(context.Background(), "cara restart?", []Source{{ID: 3, Type: "command", Title: "restart api"}})
	if err != nil || a.UsedIDs[0] != 3 {
		t.Fatalf("Answer = %+v, %v", a, err)
	}
	if !strings.Contains(prompt, "[id=3] tipe=command\njudul: restart api") || strings.Contains(prompt, "isi:") {
		t.Errorf("prompt = %q", prompt)
	}
}
