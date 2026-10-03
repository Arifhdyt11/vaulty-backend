// Command worker menjalankan task asynq: indexing note (ekstraksi, auto-tag, embedding),
// safety net, dan pembersihan session.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"

	"vaulty-api/internal/bootstrap"
	"vaulty-api/internal/database"
	"vaulty-api/internal/repository"
	"vaulty-api/internal/repository/queries"
	"vaulty-api/internal/service"
	"vaulty-api/internal/worker"
	"vaulty-api/pkg/storage"
)

func main() {
	cfg := bootstrap.Config()
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
	indexSvc := service.NewIndexService(noteRepo, store, ai, ai, worker.NewEnqueuer(queue))
	authSvc := service.NewAuthService(repository.NewUserRepository(q), repository.NewSessionRepository(q), nil, service.AuthConfig{})

	w, err := worker.New(redis, cfg.WorkerConcurrency, indexSvc, authSvc)
	if err != nil {
		bootstrap.Fatal("worker", err)
	}
	if err := w.Start(ctx); err != nil {
		bootstrap.Fatal("start worker", err)
	}
	slog.Info("worker berjalan", "concurrency", cfg.WorkerConcurrency)

	<-ctx.Done()
	slog.Info("mematikan worker...")
	w.Shutdown()
}
