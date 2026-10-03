// Command bot menjalankan bot Telegram Vaulty (quick-capture + pencarian) dengan long polling,
// sehingga tidak butuh HTTPS atau port publik (ADR-021).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"

	"vaulty-api/internal/bootstrap"
	"vaulty-api/internal/bot"
	"vaulty-api/internal/database"
	"vaulty-api/internal/repository"
	"vaulty-api/internal/repository/queries"
	"vaulty-api/internal/service"
	"vaulty-api/internal/worker"
	"vaulty-api/pkg/storage"
	"vaulty-api/pkg/telegram"
)

func main() {
	cfg := bootstrap.Config()
	if cfg.TelegramBotToken == "" {
		bootstrap.Fatal("bot telegram", errors.New("TELEGRAM_BOT_TOKEN wajib diisi"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		bootstrap.Fatal("koneksi database", err)
	}
	defer pool.Close()
	redis, err := worker.RedisOpt(cfg.RedisURL)
	if err != nil {
		bootstrap.Fatal("redis", err)
	}
	queue := asynq.NewClient(redis)
	defer queue.Close()
	store, err := storage.NewLocal(cfg.StorageDir)
	if err != nil {
		bootstrap.Fatal("storage", err)
	}
	ai := bootstrap.AIProvider(ctx, cfg)

	q := queries.New(pool)
	noteRepo := repository.NewNoteRepository(q)
	notes := service.NewNoteService(noteRepo, store, worker.NewEnqueuer(queue))
	search := service.NewSearchService(noteRepo, ai, cfg.SearchMaxDistance, cfg.SearchRelativeMargin)
	audit := service.NewAuditService(repository.NewAuditRepository(q))

	users := bootstrap.TelegramUsers(ctx, repository.NewUserRepository(q), cfg.TelegramUsers)
	b := bot.New(bot.Deps{
		TG:        telegram.NewClient(cfg.TelegramBotToken),
		Notes:     notes,
		Search:    search,
		Answers:   service.NewAnswerService(search, ai),
		Reminders: service.NewReminderService(repository.NewReminderRepository(q), repository.NewNoteRepository(q)),
		Parser:    ai,
		Audit:     audit,
		Users:     users,
	})
	slog.Info("bot telegram berjalan", "akun_terhubung", len(users))
	if err := b.Run(ctx); err != nil {
		bootstrap.Fatal("bot telegram", err)
	}
	slog.Info("bot telegram berhenti")
}
