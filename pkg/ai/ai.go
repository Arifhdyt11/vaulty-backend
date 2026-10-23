// Package ai: client untuk API kompatibel OpenAI. Empat kemampuan (chat multimodal, embedding,
// speech-to-text, text-to-speech) boleh menunjuk server berbeda (ADR-005). Tidak ada SDK vendor.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

var ErrDisabled = errors.New("AI tidak dikonfigurasi")

type Image struct {
	MIME string
	Data []byte
}

type Message struct {
	Role    string
	Content string
	Images  []Image
}

type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	JSON        bool // minta response_format json_object (server yang tidak mendukung akan mengabaikan)
	MaxTokens   int
	Temperature float32
}

type ChatResponse struct {
	Content      string
	PromptTokens int
	OutputTokens int
}

type Chat interface {
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Stream(ctx context.Context, req ChatRequest, onDelta func(string) error) (ChatResponse, error)
}

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
	Model() string
}

type Transcriber interface {
	Transcribe(ctx context.Context, audio io.Reader, filename, language string) (string, error)
}

// Speaker mengubah teks menjadi audio (text-to-speech). Mengembalikan byte audio dan MIME-nya.
type Speaker interface {
	Speak(ctx context.Context, text, voice string) ([]byte, string, error)
}

type Endpoint struct {
	BaseURL string
	APIKey  string
}

type Config struct {
	Chat       Endpoint
	ChatModel  string
	Embed      Endpoint // kosong → pakai Chat
	EmbedModel string
	EmbedDim   int
	STT        Endpoint // kosong → pakai Chat
	STTModel   string
	// TTS: endpoint /audio/speech. Model utama gagal → coba model cadangan dengan suaranya sendiri.
	TTS              Endpoint // kosong → pakai Embed
	TTSModel         string
	TTSVoice         string
	TTSFallbackModel string
	TTSFallbackVoice string
	Timeout          time.Duration
	HTTP             *http.Client
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	if cfg.Embed.BaseURL == "" {
		cfg.Embed = cfg.Chat
	}
	if cfg.STT.BaseURL == "" {
		cfg.STT = cfg.Chat
	}
	if cfg.TTS.BaseURL == "" {
		cfg.TTS = cfg.Embed
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, http: h}
}

// Enabled: Ollama lokal tidak butuh API key, jadi "enabled" berarti ada base URL.
func (c *Client) Enabled() bool { return c != nil && c.cfg.Chat.BaseURL != "" }
func (c *Client) Dim() int      { return c.cfg.EmbedDim }
func (c *Client) Model() string { return c.cfg.EmbedModel }

func (c *Client) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if !c.Enabled() {
		return ChatResponse{}, ErrDisabled
	}
	body := c.chatBody(req, false)
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := c.post(ctx, c.cfg.Chat, "/chat/completions", body, &resp); err != nil {
		return ChatResponse{}, err
	}
	if len(resp.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("chat: respons kosong")
	}
	return ChatResponse{Content: resp.Choices[0].Message.Content, PromptTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}, nil
}

func (c *Client) Stream(ctx context.Context, req ChatRequest, onDelta func(string) error) (ChatResponse, error) {
	if !c.Enabled() {
		return ChatResponse{}, ErrDisabled
	}
	body := c.chatBody(req, true)
	httpReq, err := c.newRequest(ctx, c.cfg.Chat, "/chat/completions", body)
	if err != nil {
		return ChatResponse{}, err
	}
	res, err := c.http.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("chat stream: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return ChatResponse{}, fmt.Errorf("chat stream: status %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var full strings.Builder
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		for _, ch := range ev.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			full.WriteString(ch.Delta.Content)
			if onDelta != nil {
				if err := onDelta(ch.Delta.Content); err != nil {
					return ChatResponse{Content: full.String()}, err
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return ChatResponse{Content: full.String()}, fmt.Errorf("chat stream: %w", err)
	}
	return ChatResponse{Content: full.String()}, nil
}

func (c *Client) chatBody(req ChatRequest, stream bool) map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		if len(m.Images) == 0 {
			msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
			continue
		}
		parts := []map[string]any{}
		if m.Content != "" {
			parts = append(parts, map[string]any{"type": "text", "text": m.Content})
		}
		for _, img := range m.Images {
			url := "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		}
		msgs = append(msgs, map[string]any{"role": m.Role, "content": parts})
	}
	model := req.Model
	if model == "" {
		model = c.cfg.ChatModel
	}
	body := map[string]any{"model": model, "messages": msgs, "stream": stream}
	if req.JSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	return body
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if len(texts) == 0 {
		return nil, nil
	}
	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := c.post(ctx, c.cfg.Embed, "/embeddings", map[string]any{"model": c.cfg.EmbedModel, "input": texts}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: jumlah hasil %d != %d", len(resp.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embeddings: index %d di luar jangkauan", d.Index)
		}
		if len(d.Embedding) != c.cfg.EmbedDim {
			return nil, fmt.Errorf("embeddings: dimensi %d != %d (AI_EMBED_DIM)", len(d.Embedding), c.cfg.EmbedDim)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

func (c *Client) Transcribe(ctx context.Context, audio io.Reader, filename, language string) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, audio); err != nil {
		return "", fmt.Errorf("transcribe: baca audio: %w", err)
	}
	_ = mw.WriteField("model", c.cfg.STTModel)
	_ = mw.WriteField("response_format", "json")
	if language != "" {
		_ = mw.WriteField("language", language)
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.STT.BaseURL, "/")+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.cfg.STT.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.STT.APIKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcribe: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", fmt.Errorf("transcribe: status %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("transcribe: decode: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// Speak memanggil POST /audio/speech. voice kosong → TTSVoice. Bila model utama gagal (5xx, batas
// kuota, jaringan) dicoba TTSFallbackModel dengan TTSFallbackVoice. Format keluaran bergantung model
// (mis. Gemini mengembalikan audio/wav walau diminta mp3), jadi MIME dari header dikembalikan apa adanya.
func (c *Client) Speak(ctx context.Context, text, voice string) ([]byte, string, error) {
	if c == nil || c.cfg.TTS.BaseURL == "" || c.cfg.TTSModel == "" {
		return nil, "", ErrDisabled
	}
	if strings.TrimSpace(text) == "" {
		return nil, "", fmt.Errorf("speech: teks kosong")
	}
	if voice == "" {
		voice = c.cfg.TTSVoice
	}
	data, mime, err := c.speak(ctx, c.cfg.TTSModel, voice, text)
	if err == nil || c.cfg.TTSFallbackModel == "" || ctx.Err() != nil {
		return data, mime, err
	}
	data, mime, err2 := c.speak(ctx, c.cfg.TTSFallbackModel, c.cfg.TTSFallbackVoice, text)
	if err2 != nil {
		return nil, "", fmt.Errorf("%w; cadangan: %v", err, err2)
	}
	return data, mime, nil
}

func (c *Client) speak(ctx context.Context, model, voice, text string) ([]byte, string, error) {
	body := map[string]any{"model": model, "input": text, "response_format": "mp3"}
	if voice != "" {
		body["voice"] = voice
	}
	req, err := c.newRequest(ctx, c.cfg.TTS, "/audio/speech", body)
	if err != nil {
		return nil, "", err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("speech %s: %w", model, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, "", fmt.Errorf("speech %s: status %d: %s", model, res.StatusCode, Truncate(strings.TrimSpace(string(b)), 300))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 30<<20))
	if err != nil {
		return nil, "", fmt.Errorf("speech %s: baca audio: %w", model, err)
	}
	mime := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	// Sebagian proxy mengirim error sebagai JSON dengan status 200.
	if strings.HasPrefix(mime, "application/json") || strings.HasPrefix(mime, "text/") {
		return nil, "", fmt.Errorf("speech %s: respons bukan audio: %s", model, Truncate(strings.TrimSpace(string(data)), 300))
	}
	if len(data) < 256 {
		return nil, "", fmt.Errorf("speech %s: audio kosong", model)
	}
	if mime == "" || mime == "application/octet-stream" {
		mime = "audio/mpeg"
	}
	return data, mime, nil
}

func (c *Client) newRequest(ctx context.Context, ep Endpoint, path string, body any) (*http.Request, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ep.BaseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.APIKey)
	}
	return req, nil
}

func (c *Client) post(ctx context.Context, ep Endpoint, path string, body any, out any) error {
	req, err := c.newRequest(ctx, ep, path, body)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ai %s: %w", path, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("ai %s: status %d: %s", path, res.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("ai %s: decode: %w", path, err)
	}
	return nil
}

// ParseJSONLoose membaca JSON dari jawaban model yang kadang dibungkus ``` atau diberi kalimat pembuka.
func ParseJSONLoose(s string, v any) error {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "json")
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return fmt.Errorf("tidak ada JSON di jawaban")
	}
	end := strings.LastIndexAny(s, "}]")
	if end < start {
		return fmt.Errorf("JSON tidak lengkap")
	}
	return json.Unmarshal([]byte(s[start:end+1]), v)
}

// Truncate memotong teks ke n rune supaya prompt tetap kecil.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
