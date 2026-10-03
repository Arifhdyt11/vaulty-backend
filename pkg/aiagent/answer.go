package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

// Turn adalah satu pesan sebelumnya dalam percakapan (Role "user" atau "assistant"),
// supaya pertanyaan lanjutan ("tolong analogikan") punya konteks.
type Turn struct {
	Role, Content string
}

type Answerer interface {
	// Answer menjawab pertanyaan hanya dari sources (catatan user). Bila sources tidak memuat
	// jawabannya, Vee bilang tidak ada di catatan (FromNotes=false), tanpa mengarang.
	Answer(ctx context.Context, question string, sources []Source, history []Turn) (Answer, error)
	// Ask menjawab pertanyaan umum dari pengetahuan model, tanpa catatan user.
	Ask(ctx context.Context, question string, history []Turn) (string, error)
}

const askPrompt = `Kamu Vee, asisten yang menjawab pertanyaan umum dalam Bahasa Indonesia secara jelas dan ringkas (maksimal sekitar 150 kata). Pesan user bisa berupa lanjutan dari percakapan sebelumnya (mis. "tolong analogikan", "contohnya?"): jawab sesuai topik percakapan itu. User adalah software developer: jika istilah ambigu, pakai makna di dunia software, AI, dan DevOps lebih dulu (mis. MCP = Model Context Protocol), tanpa mendaftar makna lain kecuali diminta. Teks polos tanpa markdown, kecuali ` + "`kode`" + ` untuk istilah teknis, path, atau perintah. Jika tidak yakin, katakan tidak yakin.`

const answerPrompt = `Kamu Vee, asisten catatan pribadi. Jawab pertanyaan user dalam Bahasa Indonesia, langsung dan singkat, tepat sesuai yang diminta. Contoh: diminta URL → berikan URL-nya dengan satu kalimat konteks, jangan menyalin seluruh catatan.

Aturan:
- Pertanyaan bisa berupa lanjutan dari percakapan sebelumnya; pahami maksudnya dari riwayat percakapan.
- Sumber jawaban hanya CATATAN di bawah. Jangan mengarang URL, IP, path, port, nama server, atau kredensial yang tidak ada di catatan.
- Baca SEMUA catatan dengan teliti; informasi bisa ada di judul atau di tengah isi. Jika ada yang relevan walau sebagian, jawab dengan informasi itu.
- Catatan bertipe command tidak disertakan isinya. Jika relevan, cukup sebut judulnya; sistem akan menampilkan command aslinya. Jangan menulis ulang command.
- Jika catatan benar-benar tidak memuat jawabannya, tulis singkat bahwa hal itu tidak ada di catatan. Jangan menjawab dari pengetahuan umum.
- Teks polos tanpa markdown, kecuali ` + "`kode`" + ` untuk URL, path, atau perintah.

Format balasan WAJIB: jawaban, lalu baris terakhir berisi id catatan yang kamu pakai:
SUMBER: 10, 12
Jika tidak memakai catatan apa pun: SUMBER: -`

// Konteks per note dipotong supaya prompt tetap kecil untuk model gratis.
const maxSourceChars = 1500

func (o *OpenAI) Answer(ctx context.Context, question string, sources []Source, history []Turn) (Answer, error) {
	content, err := o.complete(ctx, answerPrompt, history, formatSources(sources)+"\n\nPERTANYAAN:\n"+Truncate(question, 2000))
	if err != nil {
		return Answer{}, err
	}
	return parseAnswer(content, sources)
}

func (o *OpenAI) Ask(ctx context.Context, question string, history []Turn) (string, error) {
	content, err := o.complete(ctx, askPrompt, history, Truncate(question, 2000))
	if err != nil {
		return "", err
	}
	if content = strings.TrimSpace(content); content == "" {
		return "", fmt.Errorf("openai chat: jawaban kosong")
	}
	return content, nil
}

// Riwayat per pesan dipotong supaya prompt tetap kecil untuk model gratis.
const maxTurnChars = 1500

// complete mengirim system + riwayat + pesan user dan mengembalikan isi jawaban.
func (o *OpenAI) complete(ctx context.Context, system string, history []Turn, user string) (string, error) {
	msgs := []map[string]string{{"role": "system", "content": system}}
	for _, t := range history {
		if t.Role == "user" || t.Role == "assistant" {
			msgs = append(msgs, map[string]string{"role": t.Role, "content": Truncate(t.Content, maxTurnChars)})
		}
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": user})
	req := map[string]any{"model": o.chat.Model, "messages": msgs}
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

var (
	// "SUMBER: 10, 12" di akhir jawaban, di baris sendiri atau menempel di kalimat terakhir.
	sourceLine = regexp.MustCompile(`(?i)[\s(]*\**\s*sumber\s*\**\s*:\s*\**\s*([-\d,\s]*?)\s*[).]?\s*$`)
	digits     = regexp.MustCompile(`\d+`)
	// Sisa format JSON yang kadang tetap ditulis model lewat Hermes.
	jsonLeftover = regexp.MustCompile(`(?i)[\s,;]*(used_ids|from_notes)\s*:.*$`)
)

// parseAnswer membaca "jawaban + SUMBER: id, id". Tetap menerima JSON {answer, used_ids}
// dari model yang mengabaikan format. ID yang tidak ada di sources dibuang supaya LLM tidak
// bisa merujuk note lain.
func parseAnswer(content string, sources []Source) (Answer, error) {
	text, ids := strings.TrimSpace(content), []string{}
	var js struct {
		Answer  string            `json:"answer"`
		UsedIDs []json.RawMessage `json:"used_ids"`
	}
	if strings.HasPrefix(strings.TrimPrefix(text, "```json"), "{") || strings.HasPrefix(text, "```") {
		if err := json.Unmarshal([]byte(jsonObject(text)), &js); err == nil && strings.TrimSpace(js.Answer) != "" {
			text = js.Answer
			for _, raw := range js.UsedIDs {
				ids = append(ids, strings.Trim(string(raw), `"`))
			}
		}
	}
	if m := sourceLine.FindStringSubmatchIndex(text); m != nil {
		ids = append(ids, digits.FindAllString(text[m[2]:m[3]], -1)...)
		text = text[:m[0]]
	}
	text = strings.TrimSpace(jsonLeftover.ReplaceAllString(strings.TrimSpace(text), ""))
	if text == "" {
		return Answer{}, fmt.Errorf("openai chat: jawaban kosong")
	}

	known := make(map[int64]bool, len(sources))
	for _, s := range sources {
		known[s.ID] = true
	}
	a := Answer{Text: text}
	seen := map[int64]bool{}
	for _, raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && known[id] && !seen[id] {
			seen[id] = true
			a.UsedIDs = append(a.UsedIDs, id)
		}
	}
	a.FromNotes = len(a.UsedIDs) > 0
	return a, nil
}
