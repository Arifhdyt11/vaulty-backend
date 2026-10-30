// Package bootstrap: inisialisasi bersama api & worker (config, logger, DB, Redis, storage, AI, service).
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"vaulty/internal/config"
	"vaulty/internal/database"
	"vaulty/internal/repository"
	"vaulty/internal/router"
	"vaulty/internal/service"
	"vaulty/internal/worker"
	"vaulty/pkg/ai"
	"vaulty/pkg/fetch"
	"vaulty/pkg/googleauth"
	"vaulty/pkg/mail"
	"vaulty/pkg/media"
	"vaulty/pkg/push"
	"vaulty/pkg/storage"
)

type App struct {
	Cfg        config.Config
	Pool       *pgxpool.Pool
	Redis      *redis.Client
	RedisOpt   asynq.RedisConnOpt
	Repos      *repository.Repos
	Store      storage.Storage
	LocalStore *storage.Local
	AI         *ai.Client
	AIOn       bool
	Queue      *worker.Client
	Push       push.Sender

	Auth     *service.AuthService
	Ent      *service.EntitlementService
	Remind   *service.ReminderService
	Items    *service.ItemService
	Search   *service.SearchService
	Answer   *service.AnswerService
	Ingest   *service.IngestService
	Billing  *service.BillingService
	Account  *service.AccountService
	Briefing *service.BriefingService
	Settings *service.SettingsService
	Admin    *service.AdminService
}

func Logger(cfg config.Config) {
	var h slog.Handler
	if cfg.LogJSON {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	a := &App{Cfg: cfg}
	var err error
	if a.Pool, err = database.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns); err != nil {
		return nil, err
	}
	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	a.Redis = redis.NewClient(ropt)
	a.RedisOpt = asynq.RedisClientOpt{Addr: ropt.Addr, Password: ropt.Password, DB: ropt.DB, Username: ropt.Username, TLSConfig: ropt.TLSConfig}
	a.Repos = repository.New(a.Pool)

	// Storage
	if cfg.S3Endpoint != "" {
		s3, err := storage.NewS3(ctx, storage.S3Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, UseSSL: cfg.S3UseSSL, PublicURL: cfg.S3PublicURL})
		if err != nil {
			return nil, err
		}
		a.Store = s3
	} else {
		local, err := storage.NewLocal(cfg.LocalStorageDir, cfg.BaseURL+"/api/v1/files", router.Sign(cfg.AppSecret))
		if err != nil {
			return nil, err
		}
		a.Store, a.LocalStore = local, local
		slog.Warn("object storage: disk lokal (hanya development)", "dir", cfg.LocalStorageDir)
	}

	// AI (ADR-005)
	a.AI = ai.New(ai.Config{
		Chat: ai.Endpoint{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey}, ChatModel: cfg.AIChatModel,
		Embed: ai.Endpoint{BaseURL: cfg.AIEmbedBaseURL, APIKey: cfg.AIEmbedAPIKey}, EmbedModel: cfg.AIEmbedModel, EmbedDim: cfg.AIEmbedDim,
		STT: ai.Endpoint{BaseURL: cfg.AISTTBaseURL, APIKey: cfg.AISTTAPIKey}, STTModel: cfg.AISTTModel,
		TTS: ai.Endpoint{BaseURL: cfg.AITTSBaseURL, APIKey: cfg.AITTSAPIKey}, TTSModel: cfg.AITTSModel, TTSVoice: cfg.AITTSVoice,
		TTSFallbackModel: cfg.AITTSFallbackModel, TTSFallbackVoice: cfg.AITTSFallbackVoice,
		HTTP: &http.Client{Timeout: 180 * time.Second},
	})
	a.AIOn = a.AI.Enabled()
	if !a.AIOn {
		slog.Warn("AI nonaktif: AI_BASE_URL kosong; search full-text saja, chat & ringkasan dimatikan")
	}

	a.Queue = worker.NewClient(a.RedisOpt)

	// Push
	a.Push = push.Disabled{}
	if cfg.FCMCredentialsFile != "" {
		b, err := os.ReadFile(cfg.FCMCredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("FCM_CREDENTIALS_FILE: %w", err)
		}
		f, err := push.NewFCM(ctx, b)
		if err != nil {
			return nil, err
		}
		a.Push = f
		slog.Info("push FCM aktif", "project_id", f.ProjectID())
	} else {
		slog.Warn("push nonaktif: FCM_CREDENTIALS_FILE kosong; reminder ditandai terkirim tanpa notifikasi")
	}

	gv, err := googleauth.New(ctx, cfg.GoogleClientIDs)
	if err != nil {
		return nil, err
	}

	// Services
	a.Auth = service.NewAuthService(a.Repos, gv, service.AuthConfig{AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL,
		RegistrationOpen: cfg.RegistrationOpen, AllowedEmails: cfg.AllowedEmails, Secret: cfg.AppSecret, Mail: newMailer(cfg)})
	a.Settings = service.NewSettingsService(a.Repos)
	if err := a.Settings.Reload(ctx); err != nil {
		slog.Warn("muat pengaturan admin gagal; pakai default", "err", err)
	}
	a.Ent = service.NewEntitlementService(a.Repos, a.Settings)
	a.Admin = service.NewAdminService(a.Repos, a.Auth, a.Ent, a.Settings, cfg.MaxUploadProMB)
	a.Remind = service.NewReminderService(a.Repos, a.AI, cfg.AIChatModel, a.AIOn)
	a.Items = service.NewItemService(a.Repos, a.Store, a.Queue, a.Ent, a.Remind)
	a.Remind.SetItems(a.Items)
	a.Search = service.NewSearchService(a.Repos, a.Items, a.AI, a.AIOn)
	a.Answer = service.NewAnswerService(a.Repos, a.Search, a.Items, a.AI, a.Ent, cfg.AIChatModel, cfg.AIChatModelPro, a.AIOn)
	a.Billing = service.NewBillingService(a.Repos, cfg.RevenueCatAPIKey)
	a.Account = service.NewAccountService(a.Repos, a.Store, a.Queue, a.Ent)
	fetcher := fetch.New(fetch.Options{AllowPrivate: cfg.FetchAllowPrivate})
	// Vision (gambar & frame video) boleh di server berbeda dari chat: Hermes hanya teks,
	// jadi vision diarahkan langsung ke 9router (ADR-015).
	vision := ai.New(ai.Config{Chat: ai.Endpoint{BaseURL: cfg.AIVisionBaseURL, APIKey: cfg.AIVisionAPIKey}, ChatModel: cfg.AIVisionModel,
		EmbedDim: cfg.AIEmbedDim, HTTP: &http.Client{Timeout: 180 * time.Second}})
	mediaTool := media.New(media.Options{})
	a.Ingest = service.NewIngestService(service.IngestDeps{Repos: a.Repos, Store: a.Store, Fetcher: fetcher, Chat: a.AI, Vision: vision, Embedder: a.AI, STT: a.AI,
		Media: mediaTool, VideoMaxFrames: cfg.VideoMaxFrames, MaxVideoMinutes: cfg.AIMaxSTTMinutes,
		VisionModel: cfg.AIVisionModel, ChatModel: cfg.AIChatModel, Reminders: a.Remind, Entitlement: a.Ent, AIEnabled: a.AIOn})
	// Siaran Vee: naskah lewat chat (Hermes), suara lewat TTS 9router (ADR-016).
	a.Briefing = service.NewBriefingService(service.BriefingDeps{Repos: a.Repos, Items: a.Items, Store: a.Store, Queue: a.Queue,
		Chat: a.AI, ChatModel: cfg.AIChatModel, TTS: a.AI, Media: mediaTool, AIEnabled: a.AIOn})
	return a, nil
}

func (a *App) Close() {
	if a.Queue != nil {
		_ = a.Queue.Close()
	}
	if a.Redis != nil {
		_ = a.Redis.Close()
	}
	if a.Pool != nil {
		a.Pool.Close()
	}
}

// newMailer: SMTP bila dikonfigurasi; tanpa SMTP, development menulis email ke log dan production mematikan fitur email.
func newMailer(cfg config.Config) mail.Sender {
	switch {
	case cfg.SMTPHost != "":
		return mail.NewSMTP(mail.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.MailFrom, ReplyTo: cfg.MailReplyTo})
	case cfg.IsProd():
		slog.Warn("SMTP_HOST kosong: verifikasi email & lupa password dimatikan")
		return mail.Disabled{}
	default:
		return mail.Log{}
	}
}
