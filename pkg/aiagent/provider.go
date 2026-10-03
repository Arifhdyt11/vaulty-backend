// Package aiagent berisi layer AI agent Vee: provider LLM dan embedding di belakang interface
// (ADR-008), sehingga OpenAI, Ollama, atau provider lain bisa dipertukarkan.
package aiagent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// EmbeddingDims harus sama dengan kolom notes.embedding vector(1536).
const EmbeddingDims = 1536

type Embedder interface {
	// Embed mengembalikan vector 1536 dimensi, atau nil (tanpa error) jika provider tidak dikonfigurasi.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Model mengidentifikasi ruang vector (provider + model); disimpan di notes.embedding_model.
	Model() string
	Enabled() bool
}

type Tagger interface {
	// SuggestTags mengembalikan tag untuk teks. Nil jika provider tidak dikonfigurasi.
	SuggestTags(ctx context.Context, text string) ([]string, error)
	Enabled() bool
}

// Provider menggabungkan embedding, tagging, dan menjawab pertanyaan.
type Provider interface {
	Embedder
	Tagger
	Answerer
}

// Disabled dipakai jika OPENAI_API_KEY kosong: search jadi full-text saja, auto-tag dilewati.
type Disabled struct{}

func (Disabled) Embed(context.Context, string) ([]float32, error)      { return nil, nil }
func (Disabled) SuggestTags(context.Context, string) ([]string, error) { return nil, nil }
func (Disabled) Enabled() bool                                         { return false }
func (Disabled) Model() string                                         { return "" }
func (Disabled) Answer(context.Context, string, []Source, []Turn) (Answer, error) {
	return Answer{}, ErrDisabled
}
func (Disabled) Ask(context.Context, string, []Turn) (string, error) { return "", ErrDisabled }

var nonTagChars = regexp.MustCompile(`[^a-z0-9\-]+`)

// NormalizeTags membuat tag lowercase kebab-case, unik, dan maksimal max buah.
func NormalizeTags(tags []string, max int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")))
		t = strings.Trim(nonTagChars.ReplaceAllString(strings.ReplaceAll(t, " ", "-"), "-"), "-")
		if t == "" || len(t) > 40 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == max {
			break
		}
	}
	return out
}

// Truncate memotong teks ke n rune.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// PadEmbedding melengkapi vector yang lebih pendek (mis. bge-m3 1024 dimensi di Ollama) dengan nol
// sampai EmbeddingDims. Cosine similarity tidak berubah karena nol tidak menambah dot product
// maupun norma, sehingga kolom vector(1536) bisa dipakai model mana pun yang <= 1536 dimensi.
func PadEmbedding(v []float32) ([]float32, error) {
	if len(v) > EmbeddingDims {
		return nil, fmt.Errorf("dimensi embedding %d melebihi %d", len(v), EmbeddingDims)
	}
	if len(v) == EmbeddingDims {
		return v, nil
	}
	out := make([]float32, EmbeddingDims)
	copy(out, v)
	return out, nil
}
