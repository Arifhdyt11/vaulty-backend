package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hibiken/asynq"

	"vaulty-api/internal/service"
)

// Worker menjalankan asynq server (pemroses task) dan scheduler (task periodik).
type Worker struct {
	server    *asynq.Server
	scheduler *asynq.Scheduler
	mux       *asynq.ServeMux
	index     *service.IndexService
	auth      *service.AuthService
	reminders *service.ReminderService
	notifier  service.ReminderNotifier // nil jika Telegram belum dikonfigurasi
}

func New(redis asynq.RedisConnOpt, concurrency int, index *service.IndexService, auth *service.AuthService,
	reminders *service.ReminderService, notifier service.ReminderNotifier) (*Worker, error) {
	w := &Worker{
		server: asynq.NewServer(redis, asynq.Config{
			Concurrency: concurrency,
			Logger:      slogAdapter{},
			ErrorHandler: asynq.ErrorHandlerFunc(func(_ context.Context, t *asynq.Task, err error) {
				slog.Warn("task gagal", "type", t.Type(), "err", err)
			}),
		}),
		scheduler: asynq.NewScheduler(redis, &asynq.SchedulerOpts{Logger: slogAdapter{}}),
		mux:       asynq.NewServeMux(),
		index:     index,
		auth:      auth,
		reminders: reminders,
		notifier:  notifier,
	}
	w.mux.HandleFunc(TaskIndexNote, w.handleIndexNote)
	w.mux.HandleFunc(TaskRequeuePending, w.handleRequeuePending)
	w.mux.HandleFunc(TaskCleanupSessions, w.handleCleanupSessions)
	w.mux.HandleFunc(TaskDeliverReminders, w.handleDeliverReminders)

	for spec, task := range map[string]string{
		"@every 2m":  TaskRequeuePending,
		"@every 1h":  TaskCleanupSessions,
		"@every 20s": TaskDeliverReminders, // NF6: telat < 1 menit
	} {
		// Unique supaya task periodik tidak menumpuk saat worker sibuk.
		if _, err := w.scheduler.Register(spec, asynq.NewTask(task, nil), asynq.MaxRetry(0), asynq.Unique(time.Minute)); err != nil {
			return nil, fmt.Errorf("register jadwal %s: %w", task, err)
		}
	}
	return w, nil
}

// Start menjalankan worker. Model embedding yang berubah (mis. Ollama -> OpenAI) langsung
// dijadwalkan embed ulang.
func (w *Worker) Start(ctx context.Context) error {
	if err := w.index.RequeueStaleEmbeddings(ctx); err != nil {
		slog.Error("cek model embedding gagal", "err", err)
	}
	if err := w.server.Start(w.mux); err != nil {
		return err
	}
	return w.scheduler.Start()
}

func (w *Worker) Shutdown() {
	w.scheduler.Shutdown()
	w.server.Shutdown()
}

func (w *Worker) handleIndexNote(ctx context.Context, t *asynq.Task) error {
	var p indexPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("payload tidak valid: %v: %w", err, asynq.SkipRetry)
	}
	err := w.index.Index(ctx, p.NoteID, p.UserID, p.Version)
	if err == nil {
		return nil
	}
	retry, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	if retry >= maxRetry {
		slog.Error("index note gagal permanen", "note_id", p.NoteID, "err", err)
		w.index.MarkFailed(ctx, p.NoteID, p.UserID, p.Version, err)
	} else {
		slog.Warn("index note gagal, akan dicoba ulang", "note_id", p.NoteID, "retry", retry, "err", err)
	}
	return err
}

func (w *Worker) handleRequeuePending(ctx context.Context, _ *asynq.Task) error {
	return w.index.RequeuePending(ctx)
}

func (w *Worker) handleCleanupSessions(ctx context.Context, _ *asynq.Task) error {
	n, err := w.auth.CleanupExpiredSessions(ctx)
	if n > 0 {
		slog.Info("session kedaluwarsa dihapus", "count", n)
	}
	return err
}

func (w *Worker) handleDeliverReminders(ctx context.Context, _ *asynq.Task) error {
	if w.notifier == nil {
		return nil // tanpa channel, reminder tetap pending dan terkirim setelah Telegram dikonfigurasi
	}
	n, err := w.reminders.DeliverDue(ctx, w.notifier)
	if n > 0 {
		slog.Info("reminder terkirim", "count", n)
	}
	return err
}

// slogAdapter menyalurkan log asynq ke slog.
type slogAdapter struct{}

func (slogAdapter) Debug(args ...any) {}
func (slogAdapter) Info(args ...any)  { slog.Info("asynq", "msg", args) }
func (slogAdapter) Warn(args ...any)  { slog.Warn("asynq", "msg", args) }
func (slogAdapter) Error(args ...any) { slog.Error("asynq", "msg", args) }
func (slogAdapter) Fatal(args ...any) { slog.Error("asynq fatal", "msg", args); os.Exit(1) }
