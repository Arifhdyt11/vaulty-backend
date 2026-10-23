// vaulty: satu binary, tiga mode: api | worker | migrate (ADR-002).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vaulty/internal/bootstrap"
	"vaulty/internal/config"
	"vaulty/internal/database"
	v1 "vaulty/internal/handler/v1"
	"vaulty/internal/router"
	"vaulty/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "pemakaian: vaulty <api|worker|migrate [status]|requeue|import-v1 --email EMAIL [--dry-run]|refresh-thumbs [--dry-run]>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	bootstrap.Logger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "migrate":
		if len(os.Args) > 2 && os.Args[2] == "status" {
			err = database.MigrateStatus(ctx, cfg.DatabaseURL)
		} else {
			err = database.Migrate(ctx, cfg.DatabaseURL)
		}
	case "api":
		err = runAPI(ctx, cfg)
	case "requeue":
		err = runRequeue(ctx, cfg)
	case "refresh-thumbs":
		err = runRefreshThumbs(ctx, cfg, os.Args[2:])
	case "import-v1":
		err = runImportV1(ctx, cfg, os.Args[2:])
	case "worker":
		err = runWorker(ctx, cfg)
	default:
		err = fmt.Errorf("perintah tidak dikenal: %s", os.Args[1])
	}
	if err != nil {
		slog.Error("keluar dengan error", "err", err)
		os.Exit(1)
	}
}

func runAPI(ctx context.Context, cfg config.Config) error {
	if cfg.AutoMigrate {
		if err := database.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrasi: %w", err)
		}
	}
	app, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.Close()

	h := &v1.Handlers{Auth: app.Auth, Account: app.Account, Items: app.Items, Search: app.Search, Answer: app.Answer, Remind: app.Remind,
		Ent: app.Ent, Billing: app.Billing, Briefing: app.Briefing, Repos: app.Repos.Access(), WebhookSecret: cfg.RevenueCatWebhookSecret}
	mux := router.New(router.Deps{
		Handlers: h, Auth: app.Auth,
		ReadyDB:    func(c context.Context) error { return app.Pool.Ping(c) },
		ReadyRedis: func(c context.Context) error { return app.Redis.Ping(c).Err() },
		LocalStore: app.LocalStore, Secret: cfg.AppSecret, MaxBodyMB: cfg.MaxUploadProMB + 2, IsProd: cfg.IsProd(),
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("api listening", "addr", cfg.HTTPAddr, "docs", cfg.BaseURL+"/api/docs", "env", cfg.AppEnv)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runWorker(ctx context.Context, cfg config.Config) error {
	app, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.Close()
	srv := worker.NewServer(app.RedisOpt, cfg.WorkerConcurrency, worker.Deps{
		Ingest: app.Ingest, Account: app.Account, Briefing: app.Briefing, Reminders: app.Remind, Auth: app.Auth, Billing: app.Billing,
		Chats: app.Repos.Chats, Items: app.Repos.Items, Push: app.Push, Queue: app.Queue, Store: app.Store,
	})
	slog.Info("worker started", "concurrency", cfg.WorkerConcurrency, "ai", app.AIOn)
	return srv.Run(ctx)
}
