package aiagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Endpoint adalah satu server API kompatibel OpenAI beserta model yang dipakai di sana.
type Endpoint struct {
	BaseURL, APIKey, Model string
}

// OpenAI memanggil API kompatibel OpenAI langsung via HTTP. Server lain yang kompatibel (mis. Ollama di
// http://localhost:11434/v1) bisa dipakai lewat OPENAI_BASE_URL.
//
// Embedding dan chat boleh di server berbeda: Hermes Agent hanya melayani chat, sedangkan
// embedding langsung ke 9router (ADR-020).
type OpenAI struct {
	embed, chat Endpoint
	http        *http.Client
}

func NewOpenAI(embed, chat Endpoint) *OpenAI {
	return &OpenAI{
		embed: embed, chat: chat,
		// Model lokal (Ollama) bisa lambat saat pertama kali di-load.
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *OpenAI) Enabled() bool { return true }

// Model menyertakan base URL embedding, karena nama model yang sama di server berbeda belum tentu identik.
func (o *OpenAI) Model() string { return o.embed.BaseURL + "#" + o.embed.Model }

// isOpenAI: parameter `dimensions` hanya dikirim ke OpenAI (model text-embedding-3-*).
func (o *OpenAI) isOpenAI() bool {
	return strings.HasPrefix(o.embed.BaseURL, "https://api.openai.com")
}

// Batas input embedding 8191 token; 16k karakter aman untuk teks campuran + kode.
const maxEmbedChars = 16_000

func (o *OpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]any{
		"model": o.embed.Model,
		"input": Truncate(text, maxEmbedChars),
	}
	if o.isOpenAI() {
		req["dimensions"] = EmbeddingDims
	}
	var resp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := o.post(ctx, o.embed, "/embeddings", req, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openai embeddings: respons kosong")
	}
	vec, err := PadEmbedding(resp.Data[0].Embedding)
	if err != nil {
		return nil, fmt.Errorf("openai embeddings (%s): %w", o.embed.Model, err)
	}
	return vec, nil
}

const tagPrompt = `Kamu memberi tag untuk catatan teknis di aplikasi knowledge base pribadi.
Berikan 3-6 tag singkat (lowercase, kebab-case) yang paling membantu untuk mencari catatan ini lagi,
misalnya nama teknologi atau tool, topik, dan nama project atau server yang disebut.
Jangan membuat tag generik seperti "catatan", "snippet", "project", atau "teknologi".
Jangan membuat tag dari data rahasia.
Contoh: {"tags": ["laravel", "queue", "docker-compose", "sipantas"]}
Balas HANYA JSON dengan format {"tags": [...]}.`

const maxTagChars = 6_000

func (o *OpenAI) SuggestTags(ctx context.Context, text string) ([]string, error) {
	req := map[string]any{
		"model": o.chat.Model,
		"messages": []map[string]string{
			{"role": "system", "content": tagPrompt},
			{"role": "user", "content": Truncate(text, maxTagChars)},
		},
		"response_format": map[string]string{"type": "json_object"},
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := o.post(ctx, o.chat, "/chat/completions", req, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat: respons kosong")
	}
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(jsonObject(resp.Choices[0].Message.Content)), &out); err != nil {
		return nil, fmt.Errorf("openai chat: JSON tag tidak valid: %w", err)
	}
	return NormalizeTags(out.Tags, 6), nil
}

// jsonObject mengambil objek JSON terluar dari jawaban LLM. Tidak semua server menghormati
// response_format (mis. Hermes Agent), sehingga jawaban bisa dibungkus ```json atau diberi kalimat.
func jsonObject(s string) string {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

func (o *OpenAI) post(ctx context.Context, ep Endpoint, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ep.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := o.http.Do(req)
	if err != nil {
		return fmt.Errorf("openai %s: %w", path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("openai %s: status %d: %s", path, res.StatusCode, Truncate(string(data), 500))
	}
	// Decoder (bukan Unmarshal) supaya teks sisa setelah objek JSON diabaikan: 9router menambahkan
	// "data: [DONE]" di akhir respons chat non-streaming.
	return json.NewDecoder(bytes.NewReader(data)).Decode(out)
}
