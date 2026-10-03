// Command api menjalankan HTTP server Vaulty (/api/v1).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"vaulty-api/internal/bootstrap"
	"vaulty-api/internal/database"
	"vaulty-api/internal/repository"
	"vaulty-api/internal/repository/queries"
	"vaulty-api/internal/router"
	"vaulty-api/internal/service"
	"vaulty-api/internal/worker"
	"vaulty-api/pkg/googleauth"
	"vaulty-api/pkg/storage"
)

func main() {
	cfg := bootstrap.Config()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Infrastruktur
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

	var google *googleauth.Verifier
	if len(cfg.GoogleClientIDs) > 0 {
		google = googleauth.NewVerifier(ctx, cfg.GoogleClientIDs)
	} else {
		slog.Warn("GOOGLE_CLIENT_IDS kosong: login Google nonaktif")
	}
	if !cfg.RegistrationOpen && len(cfg.AllowedEmails) == 0 {
		slog.Warn("registrasi tertutup dan ALLOWED_EMAILS kosong: tidak ada akun baru yang bisa dibuat")
	}

	// Repository -> service. Handler & versi API (/api/v1, ...) dirakit di internal/router.
	q := queries.New(pool)
	userRepo := repository.NewUserRepository(q)
	sessionRepo := repository.NewSessionRepository(q)
	noteRepo := repository.NewNoteRepository(q)
	auditRepo := repository.NewAuditRepository(q)

	services := router.Services{
		Auth: service.NewAuthService(userRepo, sessionRepo, google, service.AuthConfig{
			SessionTTL:       cfg.SessionTTL,
			RegistrationOpen: cfg.RegistrationOpen,
			AllowedEmails:    cfg.AllowedEmails,
		}),
		Audit:  service.NewAuditService(auditRepo),
		Note:   service.NewNoteService(noteRepo, store, worker.NewEnqueuer(queue)),
		Search: service.NewSearchService(noteRepo, ai, cfg.SearchMaxDistance, cfg.SearchRelativeMargin),
	}
	r := router.New(cfg, services, map[string]router.Checker{
		"database": func(ctx context.Context) error { return pool.Ping(ctx) },
		"redis":    func(context.Context) error { return queue.Ping() },
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout cukup longgar untuk upload besar; WriteTimeout tidak diset agar streaming (SSE Vee) tidak terputus.
		ReadTimeout: 5 * time.Minute,
		IdleTimeout: 120 * time.Second,
	}
	go func() {
		slog.Info("API berjalan", "addr", cfg.HTTPAddr, "docs", router.DocsPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			bootstrap.Fatal("http server", err)
		}
	}()

	<-ctx.Done()
	slog.Info("mematikan API...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
}
