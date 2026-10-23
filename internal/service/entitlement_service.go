package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
)

// Metrik kuota (ADR-011). /search tidak dihitung; chat mode notes dan general sama-sama "questions".
const (
	MetricItems      = "items"
	MetricQuestions  = "questions"
	MetricImages     = "images"
	MetricSTTMinutes = "stt_minutes"
	MetricFiles      = "files"
)

// Batas non-metrik per plan (konstanta kode, bukan plan_limits).
type PlanRules struct {
	MaxUploadMB      int64
	TrashRetention   time.Duration
	ChatHistory      time.Duration // 0 = selamanya
	STTMaxMinutes    int           // per item
	AISuggestReminds bool
	QueuePriority    string // asynq queue
	BriefingChapters int    // jumlah bab Siaran Vee per hari (ADR-016)
}

var planRules = map[string]PlanRules{
	"free": {MaxUploadMB: 5, TrashRetention: 7 * 24 * time.Hour, ChatHistory: 7 * 24 * time.Hour, STTMaxMinutes: 0, AISuggestReminds: false, QueuePriority: "default", BriefingChapters: 3},
	"pro":  {MaxUploadMB: 50, TrashRetention: 30 * 24 * time.Hour, ChatHistory: 0, STTMaxMinutes: 30, AISuggestReminds: true, QueuePriority: "critical", BriefingChapters: 5},
}

func RulesFor(plan string) PlanRules {
	if r, ok := planRules[plan]; ok {
		return r
	}
	return planRules["free"]
}

// EntitlementService: semua keputusan Free/Pro di backend, app hanya menampilkan.
type EntitlementService struct {
	repos  *repository.Repos
	mu     sync.RWMutex
	limits map[string]map[string]model.PlanLimit // plan → metric → limit
	loaded time.Time
}

func NewEntitlementService(repos *repository.Repos) *EntitlementService {
	return &EntitlementService{repos: repos}
}

func (s *EntitlementService) limitsFor(ctx context.Context, plan string) (map[string]model.PlanLimit, error) {
	s.mu.RLock()
	fresh := time.Since(s.loaded) < 5*time.Minute && s.limits != nil
	s.mu.RUnlock()
	if !fresh {
		all, err := s.repos.Billing.PlanLimits(ctx)
		if err != nil {
			return nil, err
		}
		m := map[string]map[string]model.PlanLimit{}
		for _, l := range all {
			if m[l.Plan] == nil {
				m[l.Plan] = map[string]model.PlanLimit{}
			}
			m[l.Plan][l.Metric] = l
		}
		s.mu.Lock()
		s.limits, s.loaded = m, time.Now()
		s.mu.Unlock()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.limits[plan]; ok {
		return l, nil
	}
	return s.limits["free"], nil
}

// Period mengembalikan kunci periode dan waktu reset berikutnya di zona waktu user.
func Period(u model.User, period string, now time.Time) (string, time.Time) {
	loc := u.Location()
	t := now.In(loc)
	if period == "day" {
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		return start.Format("2006-01-02"), start.AddDate(0, 0, 1)
	}
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	return start.Format("2006-01"), start.AddDate(0, 1, 0)
}

// Check memastikan user masih punya sisa kuota n unit untuk metric; tidak mengonsumsi.
func (s *EntitlementService) Check(ctx context.Context, u model.User, metric string, n int) error {
	limits, err := s.limitsFor(ctx, u.Plan)
	if err != nil {
		return err
	}
	l, ok := limits[metric]
	if !ok || l.Limit < 0 {
		return nil
	}
	key, reset := Period(u, l.Period, time.Now())
	used, err := s.repos.Billing.Usage(ctx, u.ID, metric, key)
	if err != nil {
		return err
	}
	if used+n > l.Limit {
		return &model.QuotaError{Metric: metric, Limit: l.Limit, Used: used, ResetsAt: reset}
	}
	return nil
}

// Consume menambah pemakaian; bila melebihi batas, pemakaian dikembalikan dan QuotaError dikembalikan.
func (s *EntitlementService) Consume(ctx context.Context, u model.User, metric string, n int) error {
	limits, err := s.limitsFor(ctx, u.Plan)
	if err != nil {
		return err
	}
	l, ok := limits[metric]
	if !ok {
		return nil
	}
	key, reset := Period(u, l.Period, time.Now())
	used, err := s.repos.Billing.Consume(ctx, u.ID, metric, key, n)
	if err != nil {
		return err
	}
	if l.Limit >= 0 && used > l.Limit {
		if _, rerr := s.repos.Billing.Consume(ctx, u.ID, metric, key, -n); rerr != nil {
			slog.WarnContext(ctx, "rollback kuota gagal", "metric", metric, "err", rerr)
		}
		return &model.QuotaError{Metric: metric, Limit: l.Limit, Used: used - n, ResetsAt: reset}
	}
	return nil
}

func (s *EntitlementService) Quotas(ctx context.Context, u model.User) ([]model.Quota, error) {
	limits, err := s.limitsFor(ctx, u.Plan)
	if err != nil {
		return nil, err
	}
	order := []string{MetricItems, MetricQuestions, MetricImages, MetricSTTMinutes, MetricFiles}
	out := make([]model.Quota, 0, len(order))
	for _, m := range order {
		l, ok := limits[m]
		if !ok {
			continue
		}
		key, reset := Period(u, l.Period, time.Now())
		used, err := s.repos.Billing.Usage(ctx, u.ID, m, key)
		if err != nil {
			return nil, err
		}
		out = append(out, model.Quota{Metric: m, Limit: l.Limit, Used: used, ResetsAt: reset})
	}
	return out, nil
}

type PlanInfo struct {
	Plan       string         `json:"plan"`
	PriceLabel string         `json:"price_label,omitempty"`
	Limits     map[string]int `json:"limits"`
	Rules      map[string]any `json:"rules"`
}

func (s *EntitlementService) Plans(ctx context.Context) ([]PlanInfo, error) {
	all, err := s.repos.Billing.PlanLimits(ctx)
	if err != nil {
		return nil, err
	}
	byPlan := map[string]map[string]int{}
	for _, l := range all {
		if byPlan[l.Plan] == nil {
			byPlan[l.Plan] = map[string]int{}
		}
		byPlan[l.Plan][l.Metric] = l.Limit
	}
	out := []PlanInfo{}
	for _, p := range []string{"free", "pro"} {
		r := RulesFor(p)
		out = append(out, PlanInfo{Plan: p, Limits: byPlan[p], Rules: map[string]any{
			"max_upload_mb": r.MaxUploadMB, "trash_days": int(r.TrashRetention.Hours() / 24),
			"chat_history_days": int(r.ChatHistory.Hours() / 24), "stt_max_minutes_per_item": r.STTMaxMinutes,
			"ai_suggested_reminders": r.AISuggestReminds, "briefing_chapters": r.BriefingChapters,
		}})
	}
	return out, nil
}

// MaxUploadBytes untuk validasi di handler.
func MaxUploadBytes(plan string) int64 { return RulesFor(plan).MaxUploadMB << 20 }

func (s *EntitlementService) String() string {
	return fmt.Sprintf("entitlement(%d plans)", len(s.limits))
}
