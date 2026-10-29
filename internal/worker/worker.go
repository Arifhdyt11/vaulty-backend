package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"vaulty/internal/model"
	"vaulty/internal/service"
	"vaulty/pkg/push"
)

type Deps struct {
	Ingest    *service.IngestService
	Account   *service.AccountService
	Briefing  *service.BriefingService
	Reminders *service.ReminderService
	Auth      *service.AuthService
	Billing   *service.BillingService
	Chats     ChatPruner
	Items     ItemPurger
	Push      push.Sender
	Queue     service.Enqueuer
	Store     ObjectDeleter
}

type ChatPruner interface {
	PruneConversations(ctx context.Context, plan string, olderThan time.Duration) (int64, error)
}

type ItemPurger interface {
	PurgeDeleted(ctx context.Context, olderThan time.Duration) ([]model.Item, error)
}

type ObjectDeleter interface {
	Delete(ctx context.Context, key string) error
}

// Server membungkus asynq.Server + pekerjaan periodik (ticker) di proses yang sama.
type Server struct {
	srv  *asynq.Server
	mux  *asynq.ServeMux
	deps Deps
}

func NewServer(redisOpt asynq.RedisConnOpt, concurrency int, d Deps) *Server {
	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: concurrency,
		Queues:      map[string]int{"critical": 6, "default": 3, "low": 1},
		Logger:      slogAdapter{},
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			slog.ErrorContext(ctx, "task gagal", "type", t.Type(), "err", err)
		}),
	})
	mux := asynq.NewServeMux()
	s := &Server{srv: srv, mux: mux, deps: d}
	mux.HandleFunc(TaskItemIngest, s.handleIngest)
	mux.HandleFunc(TaskAccountDelete, s.handleAccountDelete)
	mux.HandleFunc(TaskExportBuild, s.handleExport)
	mux.HandleFunc(TaskBriefingBuild, s.handleBriefing)
	return s
}

func (s *Server) Run(ctx context.Context) error {
	go s.periodic(ctx)
	if err := s.srv.Start(s.mux); err != nil {
		return err
	}
	<-ctx.Done()
	s.srv.Shutdown()
	return nil
}

func (s *Server) handleIngest(ctx context.Context, t *asynq.Task) error {
	var p IngestPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("payload: %w: %w", err, asynq.SkipRetry)
	}
	if err := s.deps.Ingest.Run(ctx, p.UserID, p.ItemID, p.Version); err != nil {
		return fmt.Errorf("ingest item %d: %w", p.ItemID, err)
	}
	return nil
}

func (s *Server) handleAccountDelete(ctx context.Context, t *asynq.Task) error {
	var p AccountPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("payload: %w: %w", err, asynq.SkipRetry)
	}
	return s.deps.Account.PurgeAccount(ctx, p.UserID)
}

func (s *Server) handleExport(ctx context.Context, t *asynq.Task) error {
	var p ExportPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("payload: %w: %w", err, asynq.SkipRetry)
	}
	return s.deps.Account.BuildExport(ctx, p.JobID, p.UserID)
}

func (s *Server) handleBriefing(ctx context.Context, t *asynq.Task) error {
	var p BriefingPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("payload: %w: %w", err, asynq.SkipRetry)
	}
	if s.deps.Briefing == nil {
		return fmt.Errorf("siaran tidak dikonfigurasi: %w", asynq.SkipRetry)
	}
	if err := s.deps.Briefing.Build(ctx, p.UserID, p.BriefingID); err != nil {
		return fmt.Errorf("siaran %d: %w", p.BriefingID, err)
	}
	return nil
}

// periodic: reminder tiap 20 detik; safety net tiap 5 menit; housekeeping tiap jam.
func (s *Server) periodic(ctx context.Context) {
	remind := time.NewTicker(20 * time.Second)
	safety := time.NewTicker(5 * time.Minute)
	hourly := time.NewTicker(time.Hour)
	defer remind.Stop()
	defer safety.Stop()
	defer hourly.Stop()
	s.housekeeping(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-remind.C:
			if n, err := s.deps.Reminders.DeliverDue(ctx, s.deps.Push); err != nil {
				slog.WarnContext(ctx, "deliver reminder", "err", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "reminder terkirim", "n", n)
			}
		case <-safety.C:
			if n, err := s.deps.Ingest.RequeueStuck(ctx, s.deps.Queue); err != nil {
				slog.WarnContext(ctx, "requeue stuck", "err", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "item macet di-enqueue ulang", "n", n)
			}
			if n, err := s.deps.Ingest.ReindexStale(ctx, s.deps.Queue); err == nil && n > 0 {
				slog.InfoContext(ctx, "re-index embedding lama", "n", n)
			}
		case <-hourly.C:
			s.housekeeping(ctx)
		}
	}
}

func (s *Server) housekeeping(ctx context.Context) {
	if n, err := s.deps.Auth.CleanupExpired(ctx); err == nil && n > 0 {
		slog.InfoContext(ctx, "sesi kedaluwarsa dibersihkan", "n", n)
	}
	if _, err := s.deps.Reminders.ReleaseStale(ctx); err != nil {
		slog.WarnContext(ctx, "release stale reminders", "err", err)
	}
	if n, err := s.deps.Billing.DowngradeExpired(ctx); err == nil && n > 0 {
		slog.InfoContext(ctx, "downgrade pro kedaluwarsa", "n", n)
	}
	// ChatHistory 0 = selamanya (bisa diatur admin), jadi jangan dipangkas.
	if keep := service.RulesFor("free").ChatHistory; s.deps.Chats != nil && keep > 0 {
		if n, err := s.deps.Chats.PruneConversations(ctx, "free", keep); err == nil && n > 0 {
			slog.InfoContext(ctx, "riwayat chat free dipangkas", "n", n)
		}
	}
	if s.deps.Briefing != nil {
		if n, err := s.deps.Briefing.Prune(ctx); err != nil {
			slog.WarnContext(ctx, "pangkas siaran lama", "err", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "audio siaran lama dihapus", "n", n)
		}
	}
	if s.deps.Items != nil {
		// Sampah dikosongkan memakai retensi terpanjang (pro) sebagai batas global yang aman.
		keep := max(service.RulesFor("pro").TrashRetention, service.RulesFor("free").TrashRetention)
		if n, err := s.deps.Items.PurgeDeleted(ctx, keep); err == nil && len(n) > 0 {
			for _, it := range n {
				for _, k := range []*string{it.ThumbnailKey, it.FileKey, it.AttachmentKey} {
					if k != nil && *k != "" && s.deps.Store != nil {
						_ = s.deps.Store.Delete(ctx, *k)
					}
				}
			}
			slog.InfoContext(ctx, "sampah dikosongkan", "n", len(n))
		}
	}
}

type slogAdapter struct{}

func (slogAdapter) Debug(args ...any) { slog.Debug(fmt.Sprint(args...)) }
func (slogAdapter) Info(args ...any)  { slog.Info(fmt.Sprint(args...)) }
func (slogAdapter) Warn(args ...any)  { slog.Warn(fmt.Sprint(args...)) }
func (slogAdapter) Error(args ...any) { slog.Error(fmt.Sprint(args...)) }
func (slogAdapter) Fatal(args ...any) { slog.Error(fmt.Sprint(args...)) }
