package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrDisabled dikembalikan provider Disabled untuk fitur yang wajib AI (menjawab pertanyaan).
var ErrDisabled = errors.New("AI tidak dikonfigurasi")

// Source adalah satu note yang diberikan ke LLM sebagai konteks jawaban.
// Content kosong untuk note command: isinya tidak boleh ditulis ulang LLM (ADR-010).
type Source struct {
	ID      int64
	Type    string
	Title   string
	URL     string
	Content string
}

// Answer adalah jawaban LLM. UsedIDs hanya berisi ID dari Source yang diberikan.
type Answer struct {
	Text      string
	UsedIDs   []int64
	FromNotes bool
}

type Answerer interface {
	// Answer menjawab pertanyaan hanya dari sources (catatan user). Bila sources tidak memuat
	// jawabannya, Vee bilang tidak ada di catatan (FromNotes=false), tanpa mengarang.
	Answer(ctx context.Context, question string, sources []Source) (Answer, error)
	// Ask menjawab pertanyaan umum dari pengetahuan model, tanpa catatan user.
	Ask(ctx context.Context, question string) (string, error)
}

const askPrompt = `Kamu Vee, asisten yang menjawab pertanyaan umum dalam Bahasa Indonesia secara jelas dan ringkas (maksimal sekitar 150 kata). Teks polos tanpa markdown, kecuali ` + "`kode`" + ` untuk istilah teknis, path, atau perintah. Jika tidak yakin, katakan tidak yakin.`

const answerPrompt = `Kamu Vee, asisten catatan pribadi. Jawab pertanyaan user dalam Bahasa Indonesia, langsung dan singkat, tepat sesuai yang diminta. Contoh: diminta URL → berikan URL-nya dengan satu kalimat konteks, jangan menyalin seluruh catatan.

Aturan:
- Sumber utama adalah CATATAN di bawah. Jangan mengarang URL, IP, path, port, nama server, atau kredensial yang tidak ada di catatan.
- Catatan bertipe command tidak disertakan isinya. Jika relevan, cukup sebut judulnya dan masukkan id-nya ke used_ids; sistem akan menampilkan command aslinya. Jangan menulis ulang command dari catatan.
- Jika catatan tidak memuat jawabannya, jawab singkat bahwa hal itu tidak ada di catatan, used_ids kosong, from_notes false. Jangan menjawab dari pengetahuan umum.
- Teks polos tanpa markdown, kecuali ` + "`kode`" + ` untuk URL, path, atau perintah.
- used_ids: id catatan yang benar-benar kamu pakai (boleh kosong).

Balas HANYA JSON: {"answer": "...", "used_ids": [1, 2], "from_notes": true}`

// Konteks per note dipotong supaya prompt tetap kecil untuk model gratis.
const maxSourceChars = 1500

func (o *OpenAI) Answer(ctx context.Context, question string, sources []Source) (Answer, error) {
	content, err := o.complete(ctx, answerPrompt, formatSources(sources)+"\n\nPERTANYAAN:\n"+Truncate(question, 2000), true)
	if err != nil {
		return Answer{}, err
	}
	return parseAnswer(content, sources)
}

func (o *OpenAI) Ask(ctx context.Context, question string) (string, error) {
	content, err := o.complete(ctx, askPrompt, Truncate(question, 2000), false)
	if err != nil {
		return "", err
	}
	if content = strings.TrimSpace(content); content == "" {
		return "", fmt.Errorf("openai chat: jawaban kosong")
	}
	return content, nil
}

// complete mengirim satu percakapan system+user dan mengembalikan isi jawaban.
func (o *OpenAI) complete(ctx context.Context, system, user string, jsonMode bool) (string, error) {
	req := map[string]any{
		"model": o.chat.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if jsonMode {
		req["response_format"] = map[string]string{"type": "json_object"}
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := o.post(ctx, o.chat, "/chat/completions", req, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai chat: respons kosong")
	}
	return resp.Choices[0].Message.Content, nil
}

func formatSources(sources []Source) string {
	if len(sources) == 0 {
		return "CATATAN: (tidak ada catatan yang cocok)"
	}
	var sb strings.Builder
	sb.WriteString("CATATAN:")
	for _, s := range sources {
		fmt.Fprintf(&sb, "\n\n[id=%d] tipe=%s", s.ID, s.Type)
		if s.Title != "" {
			fmt.Fprintf(&sb, "\njudul: %s", s.Title)
		}
		if s.URL != "" {
			fmt.Fprintf(&sb, "\nurl: %s", s.URL)
		}
		if s.Content != "" {
			fmt.Fprintf(&sb, "\nisi: %s", Truncate(s.Content, maxSourceChars))
		}
	}
	return sb.String()
}

// parseAnswer toleran: jika model tidak membalas JSON, seluruh teks dipakai sebagai jawaban.
// ID yang tidak ada di sources dibuang supaya LLM tidak bisa merujuk note lain.
func parseAnswer(content string, sources []Source) (Answer, error) {
	var out struct {
		Answer    string            `json:"answer"`
		UsedIDs   []json.RawMessage `json:"used_ids"`
		FromNotes *bool             `json:"from_notes"`
	}
	if err := json.Unmarshal([]byte(jsonObject(content)), &out); err != nil || strings.TrimSpace(out.Answer) == "" {
		text := strings.TrimSpace(content)
		if text == "" {
			return Answer{}, fmt.Errorf("openai chat: jawaban kosong")
		}
		return Answer{Text: text}, nil
	}
	known := make(map[int64]bool, len(sources))
	for _, s := range sources {
		known[s.ID] = true
	}
	a := Answer{Text: strings.TrimSpace(out.Answer)}
	seen := map[int64]bool{}
	for _, raw := range out.UsedIDs {
		// Model kadang mengirim id sebagai string ("12").
		id, err := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
		if err == nil && known[id] && !seen[id] {
			seen[id] = true
			a.UsedIDs = append(a.UsedIDs, id)
		}
	}
	a.FromNotes = len(a.UsedIDs) > 0
	if out.FromNotes != nil {
		a.FromNotes = *out.FromNotes && len(sources) > 0
	}
	return a, nil
}
