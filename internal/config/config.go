// Package config memuat konfigurasi aplikasi dari environment variable (.env).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env         string
	HTTPAddr    string
	DatabaseURL string
	// Maksimal koneksi pool per proses. Total = DB_MAX_CONNS x jumlah instance api/worker,
	// harus di bawah max_connections Postgres.
	DBMaxConns int32
	RedisURL   string

	SessionTTL       time.Duration
	RegistrationOpen bool
	AllowedEmails    []string

	StorageDir     string
	MaxUploadBytes int64

	// Client ID OAuth yang boleh menjadi audience id_token (web NextAuth, iOS).
	GoogleClientIDs []string

	// OPENAI_* dipakai untuk chat (auto-tag, Vee). Embedding bisa diarahkan ke server lain lewat
	// OPENAI_EMBEDDING_BASE_URL/API_KEY, karena Hermes Agent tidak punya /embeddings (ADR-020).
	OpenAIAPIKey           string
	OpenAIBaseURL          string
	OpenAIChatModel        string
	OpenAIEmbeddingAPIKey  string
	OpenAIEmbeddingBaseURL string
	OpenAIEmbeddingModel   string

	// Batas jarak cosine kandidat semantic search. Nilainya bergantung model embedding.
	SearchMaxDistance float64
	// Hanya ambil kandidat yang jaraknya <= jarak terbaik + margin, supaya hasil tidak berisik.
	SearchRelativeMargin float64

	WorkerConcurrency int

	// Bot Telegram (cmd/bot, ADR-021). TelegramUsers: ID Telegram -> email akun Vaulty.
	TelegramBotToken string
	TelegramUsers    map[int64]string
}

// AIEnabled: provider AI dianggap aktif jika OPENAI_API_KEY diisi (untuk Ollama cukup isi "ollama").
func (c Config) AIEnabled() bool { return c.OpenAIAPIKey != "" }

func Load() (Config, error) {
	c := Config{
		Env:         get("APP_ENV", "development"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		DBMaxConns:  int32(getInt("DB_MAX_CONNS", 10)),
		RedisURL:    get("REDIS_URL", "redis://localhost:6379/0"),

		SessionTTL:       getDuration("SESSION_TTL", 30*24*time.Hour),
		RegistrationOpen: getBool("REGISTRATION_OPEN", false),
		AllowedEmails:    list(os.Getenv("ALLOWED_EMAILS")),

		StorageDir:     get("STORAGE_DIR", "./storage"),
		MaxUploadBytes: int64(getInt("MAX_UPLOAD_MB", 25)) << 20,

		GoogleClientIDs: list(os.Getenv("GOOGLE_CLIENT_IDS")),

		OpenAIAPIKey:         os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:        strings.TrimRight(get("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		OpenAIChatModel:      get("OPENAI_CHAT_MODEL", "gpt-5-mini"),
		OpenAIEmbeddingModel: get("OPENAI_EMBEDDING_MODEL", "text-embedding-3-small"),

		SearchMaxDistance:    getFloat("SEARCH_MAX_DISTANCE", 0.6),
		SearchRelativeMargin: getFloat("SEARCH_RELATIVE_MARGIN", 0.08),

		WorkerConcurrency: getInt("WORKER_CONCURRENCY", 5),

		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
	}
	users, err := telegramUsers(os.Getenv("TELEGRAM_USERS"))
	if err != nil {
		return c, err
	}
	c.TelegramUsers = users
	// Tanpa override, embedding memakai server yang sama dengan chat (setup Ollama/OpenAI biasa).
	c.OpenAIEmbeddingAPIKey = get("OPENAI_EMBEDDING_API_KEY", c.OpenAIAPIKey)
	c.OpenAIEmbeddingBaseURL = strings.TrimRight(get("OPENAI_EMBEDDING_BASE_URL", c.OpenAIBaseURL), "/")
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL wajib diisi")
	}
	return c, nil
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// telegramUsers mem-parse "123456:a@b.com,789:c@d.com" (ID Telegram : email Vaulty).
func telegramUsers(v string) (map[int64]string, error) {
	out := map[int64]string{}
	for _, pair := range list(v) {
		id, email, ok := strings.Cut(pair, ":")
		n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if !ok || err != nil || strings.TrimSpace(email) == "" {
			return nil, fmt.Errorf("TELEGRAM_USERS tidak valid (format <id_telegram>:<email>): %q", pair)
		}
		out[n] = strings.ToLower(strings.TrimSpace(email))
	}
	return out, nil
}

func list(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
