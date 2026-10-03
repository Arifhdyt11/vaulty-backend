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
	"vaulty-api/internal/model"
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

	users := linkedUsers(ctx, repository.NewUserRepository(q), cfg.TelegramUsers)
	answers := service.NewAnswerService(search, ai)
	b := bot.New(telegram.NewClient(cfg.TelegramBotToken), notes, search, answers, audit, users)
	slog.Info("bot telegram berjalan", "akun_terhubung", len(users))
	if err := b.Run(ctx); err != nil {
		bootstrap.Fatal("bot telegram", err)
	}
	slog.Info("bot telegram berhenti")
}

// linkedUsers mengubah TELEGRAM_USERS (ID Telegram -> email) menjadi ID Telegram -> user_id.
// Email yang belum terdaftar dilewati; bot perlu di-restart setelah akunnya dibuat.
func linkedUsers(ctx context.Context, repo *repository.UserRepository, emails map[int64]string) map[int64]int64 {
	out := make(map[int64]int64, len(emails))
	for tgID, email := range emails {
		u, _, err := repo.FindByEmail(ctx, email)
		if errors.Is(err, model.ErrNotFound) {
			slog.WarnContext(ctx, "akun Vaulty untuk TELEGRAM_USERS belum ada, dilewati", "telegram_id", tgID)
			continue
		}
		if err != nil {
			bootstrap.Fatal("cari akun TELEGRAM_USERS", err)
		}
		out[tgID] = u.ID
	}
	return out
}
