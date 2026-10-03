// Package bootstrap berisi inisialisasi bersama untuk cmd/api dan cmd/worker.
package bootstrap

import (
	"context"
	"log/slog"
	"os"

	"github.com/joho/godotenv"

	"vaulty-api/internal/config"
	"vaulty-api/pkg/aiagent"
)

// Config memuat .env (jika ada) dan konfigurasi, lalu menyiapkan logger. Keluar jika tidak valid.
func Config() config.Config {
	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		Fatal("konfigurasi tidak valid", err)
	}
	var h slog.Handler = slog.NewTextHandler(os.Stdout, nil)
	if cfg.Env == "production" {
		h = slog.NewJSONHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(h))
	return cfg
}

// AIProvider mengembalikan provider OpenAI-compatible (OpenAI, Ollama, atau Hermes + 9router),
// atau Disabled jika OPENAI_API_KEY kosong.
func AIProvider(ctx context.Context, cfg config.Config) aiagent.Provider {
	if !cfg.AIEnabled() {
		slog.WarnContext(ctx, "OPENAI_API_KEY kosong: auto-tag nonaktif, search hanya full-text")
		return aiagent.Disabled{}
	}
	slog.InfoContext(ctx, "provider AI",
		"chat_base_url", cfg.OpenAIBaseURL, "chat", cfg.OpenAIChatModel,
		"embedding_base_url", cfg.OpenAIEmbeddingBaseURL, "embedding", cfg.OpenAIEmbeddingModel)
	return aiagent.NewOpenAI(
		aiagent.Endpoint{BaseURL: cfg.OpenAIEmbeddingBaseURL, APIKey: cfg.OpenAIEmbeddingAPIKey, Model: cfg.OpenAIEmbeddingModel},
		aiagent.Endpoint{BaseURL: cfg.OpenAIBaseURL, APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIChatModel},
	)
}

func Fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
