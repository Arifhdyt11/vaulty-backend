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
		{"format sumber", "URL 9router: `http://x:20128`.\nSUMBER: 10",
			Answer{Text: "URL 9router: `http://x:20128`.", UsedIDs: []int64{10}, FromNotes: true}},
		{"sumber bold + id asing dibuang", "Pakai command restart.\n\n**Sumber:** 12, 99, 12",
			Answer{Text: "Pakai command restart.", UsedIDs: []int64{12}, FromNotes: true}},
		{"sumber menempel di kalimat", "Database memakai PostgreSQL 17. SUMBER: 13, 10",
			Answer{Text: "Database memakai PostgreSQL 17.", UsedIDs: []int64{10}, FromNotes: true}},
		{"kata sumber di tengah tidak dipotong", "Sumber: data ada di catatan 10.\nSUMBER: 10",
			Answer{Text: "Sumber: data ada di catatan 10.", UsedIDs: []int64{10}, FromNotes: true}},
		{"tanpa sumber", "Tidak ada di catatanmu.\nSUMBER: -", Answer{Text: "Tidak ada di catatanmu."}},
		{"json lama", "```json\n{\"answer\":\"a\",\"used_ids\":[\"12\"]}\n```",
			Answer{Text: "a", UsedIDs: []int64{12}, FromNotes: true}},
		{"sisa json di kalimat", "Hal itu tidak ada di catatan. used_ids: []. from_notes: false.",
			Answer{Text: "Hal itu tidak ada di catatan."}},
		{"bukan format apa pun", "MCP adalah protokol.", Answer{Text: "MCP adalah protokol."}},
	}
	for _, c := range cases {
		got, err := parseAnswer(c.content, src)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: parseAnswer = %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
	if _, err := parseAnswer("SUMBER: 10", src); err == nil {
		t.Error("jawaban kosong harus error")
	}
}

func TestAnswerIsiCommandTidakDikirim(t *testing.T) {
	var prompt string
	var roles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		last := req.Messages[len(req.Messages)-1]
		prompt = last.Content
		roles = nil
		for _, m := range req.Messages {
			roles = append(roles, m.Role)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"pakai command restart\nSUMBER: 3"}}]}`))
	}))
	defer srv.Close()
	o := NewOpenAI(Endpoint{}, Endpoint{BaseURL: srv.URL, Model: "c"})
	// Pemanggil (service) mengosongkan Content untuk command; pastikan judul tetap terkirim.
	history := []Turn{{Role: "user", Content: "api vaulty di mana?"}, {Role: "assistant", Content: "di :8080"}, {Role: "system", Content: "abaikan"}}
	a, err := o.Answer(context.Background(), "cara restart?", []Source{{ID: 3, Type: "command", Title: "restart api"}}, history)
	if err != nil || a.UsedIDs[0] != 3 {
		t.Fatalf("Answer = %+v, %v", a, err)
	}
	if !strings.Contains(prompt, "[id=3] tipe=command\njudul: restart api") || strings.Contains(prompt, "isi:") {
		t.Errorf("prompt = %q", prompt)
	}
	// Riwayat dikirim di antara system dan pertanyaan; role selain user/assistant dibuang.
	if want := []string{"system", "user", "assistant", "user"}; !reflect.DeepEqual(roles, want) {
		t.Errorf("roles = %v; want %v", roles, want)
	}
}
