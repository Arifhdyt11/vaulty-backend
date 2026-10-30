// Package config membaca konfigurasi dari environment (.env di development).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv      string
	HTTPAddr    string
	BaseURL     string // URL publik API, dipakai untuk link di push/export
	AppSecret   string // HMAC untuk URL file lokal
	AutoMigrate bool

	DatabaseURL string
	RedisURL    string
	DBMaxConns  int32

	AccessTTL  time.Duration
	RefreshTTL time.Duration

	RegistrationOpen bool
	AllowedEmails    []string
	GoogleClientIDs  []string

	// Object storage S3-compatible (R2 / MinIO).
	S3Endpoint      string
	S3Region        string
	S3Bucket        string
	S3AccessKey     string
	S3SecretKey     string
	S3UseSSL        bool
	S3PublicURL     string // opsional: CDN publik; kosong → signed URL
	LocalStorageDir string // fallback tanpa S3 (development)

	MaxUploadMB    int64
	MaxUploadProMB int64

	// Provider AI, API kompatibel OpenAI. Tiga kemampuan bisa beda server (ADR-005).
	AIBaseURL       string
	AIAPIKey        string
	AIChatModel     string
	AIChatModelPro  string
	AIVisionModel   string
	AIVisionBaseURL string // kosong → server chat
	AIVisionAPIKey  string
	VideoMaxFrames  int
	AIEmbedBaseURL  string
	AIEmbedAPIKey   string
	AIEmbedModel    string
	AIEmbedDim      int
	AISTTBaseURL    string
	AISTTAPIKey     string
	AISTTModel      string
	AIMaxSTTMinutes int
	// Text-to-speech Siaran Vee (ADR-016): /audio/speech kompatibel OpenAI, default server embedding (9router).
	AITTSBaseURL       string
	AITTSAPIKey        string
	AITTSModel         string
	AITTSVoice         string
	AITTSFallbackModel string
	AITTSFallbackVoice string

	FetchAllowPrivate bool

	FCMCredentialsFile string

	RevenueCatWebhookSecret string
	RevenueCatAPIKey        string

	// SMTP untuk email OTP (ADR-022). Kosong: development menulis email ke log, production mematikan fitur email.
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	MailFrom string

	WorkerConcurrency int
	LogJSON           bool
}

func Load() (Config, error) {
	_ = godotenv.Load() // .env opsional; di production env datang dari orchestrator

	c := Config{
		AppEnv:      get("APP_ENV", "development"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		BaseURL:     get("BASE_URL", "http://localhost:8080"),
		AppSecret:   get("APP_SECRET", "dev-secret-ganti-di-production"),
		AutoMigrate: getBool("AUTO_MIGRATE", true),

		DatabaseURL: get("DATABASE_URL", "postgres://vaulty:vaulty@localhost:5435/vaulty?sslmode=disable"),
		RedisURL:    get("REDIS_URL", "redis://localhost:6380/0"),
		DBMaxConns:  int32(getInt("DB_MAX_CONNS", 10)),

		AccessTTL:  getDur("ACCESS_TTL", time.Hour),
		RefreshTTL: getDur("REFRESH_TTL", 90*24*time.Hour),

		RegistrationOpen: getBool("REGISTRATION_OPEN", true),
		AllowedEmails:    getList("ALLOWED_EMAILS"),
		GoogleClientIDs:  getList("GOOGLE_CLIENT_IDS"),

		S3Endpoint:      get("S3_ENDPOINT", ""),
		S3Region:        get("S3_REGION", "auto"),
		S3Bucket:        get("S3_BUCKET", "vaulty"),
		S3AccessKey:     get("S3_ACCESS_KEY", ""),
		S3SecretKey:     get("S3_SECRET_KEY", ""),
		S3UseSSL:        getBool("S3_USE_SSL", true),
		S3PublicURL:     get("S3_PUBLIC_URL", ""),
		LocalStorageDir: get("LOCAL_STORAGE_DIR", "./storage"),

		MaxUploadMB:    int64(getInt("MAX_UPLOAD_MB", 5)),
		MaxUploadProMB: int64(getInt("MAX_UPLOAD_PRO_MB", 50)),

		AIBaseURL:       get("AI_BASE_URL", "http://localhost:11434/v1"),
		AIAPIKey:        get("AI_API_KEY", ""),
		AIChatModel:     get("AI_CHAT_MODEL", "qwen2.5:7b-instruct"),
		AIChatModelPro:  get("AI_CHAT_MODEL_PRO", ""),
		AIVisionModel:   get("AI_VISION_MODEL", ""),
		AIVisionBaseURL: get("AI_VISION_BASE_URL", ""),
		AIVisionAPIKey:  get("AI_VISION_API_KEY", ""),
		VideoMaxFrames:  getInt("VIDEO_MAX_FRAMES", 6),
		AIEmbedBaseURL:  get("AI_EMBED_BASE_URL", ""),
		AIEmbedAPIKey:   get("AI_EMBED_API_KEY", ""),
		AIEmbedModel:    get("AI_EMBED_MODEL", "bge-m3"),
		AIEmbedDim:      getInt("AI_EMBED_DIM", 1024),
		AISTTBaseURL:    get("AI_STT_BASE_URL", ""),
		AISTTAPIKey:     get("AI_STT_API_KEY", ""),
		AISTTModel:      get("AI_STT_MODEL", "whisper-1"),
		AIMaxSTTMinutes: getInt("AI_MAX_STT_MINUTES", 30),

		AITTSBaseURL:       get("AI_TTS_BASE_URL", ""),
		AITTSAPIKey:        get("AI_TTS_API_KEY", ""),
		AITTSModel:         get("AI_TTS_MODEL", "gemini/gemini-2.5-flash-preview-tts"),
		AITTSVoice:         get("AI_TTS_VOICE", "Leda"),
		AITTSFallbackModel: get("AI_TTS_FALLBACK_MODEL", "openai/nova"),
		AITTSFallbackVoice: get("AI_TTS_FALLBACK_VOICE", "nova"),

		FetchAllowPrivate: getBool("FETCH_ALLOW_PRIVATE", false),

		FCMCredentialsFile: get("FCM_CREDENTIALS_FILE", ""),

		RevenueCatWebhookSecret: get("REVENUECAT_WEBHOOK_SECRET", ""),
		RevenueCatAPIKey:        get("REVENUECAT_API_KEY", ""),

		SMTPHost: get("SMTP_HOST", ""),
		SMTPPort: getInt("SMTP_PORT", 587),
		SMTPUser: get("SMTP_USER", ""),
		SMTPPass: get("SMTP_PASS", ""),
		MailFrom: get("MAIL_FROM", "Vaulty <vaulty@mail.reepzs.site>"),

		WorkerConcurrency: getInt("WORKER_CONCURRENCY", 4),
		LogJSON:           getBool("LOG_JSON", false),
	}
	if c.AppEnv == "production" {
		c.LogJSON = true
	}
	if c.AIEmbedDim <= 0 || c.AIEmbedDim > 1536 {
		return c, fmt.Errorf("AI_EMBED_DIM harus 1..1536 (kolom vector(1536))")
	}
	if c.AIChatModelPro == "" {
		c.AIChatModelPro = c.AIChatModel
	}
	if c.AIVisionBaseURL == "" {
		c.AIVisionBaseURL, c.AIVisionAPIKey = c.AIBaseURL, c.AIAPIKey
	}
	if c.AITTSBaseURL == "" {
		c.AITTSBaseURL = c.AIEmbedBaseURL
		if c.AITTSAPIKey == "" {
			c.AITTSAPIKey = c.AIEmbedAPIKey
		}
	}
	if c.AIVisionModel == "" {
		c.AIVisionModel = c.AIChatModel
	}
	return c, nil
}

func (c Config) IsProd() bool { return c.AppEnv == "production" }

func get(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getInt(k string, def int) int {
	v := get(k, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getBool(k string, def bool) bool {
	v := strings.ToLower(get(k, ""))
	switch v {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	}
	return def
}

func getDur(k string, def time.Duration) time.Duration {
	v := get(k, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func getList(k string) []string {
	var out []string
	for _, p := range strings.Split(get(k, ""), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
