package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
)

// PlanRulesConfig: bentuk JSON PlanRules yang boleh diubah admin (ADR-021).
// QueuePriority tidak ikut: itu urusan kapasitas worker, bukan produk.
type PlanRulesConfig struct {
	MaxUploadMB      int64 `json:"max_upload_mb" minimum:"1"`
	TrashDays        int   `json:"trash_days" minimum:"1" maximum:"365"`
	ChatHistoryDays  int   `json:"chat_history_days" minimum:"0" maximum:"3650" doc:"0 = selamanya"`
	STTMaxMinutes    int   `json:"stt_max_minutes_per_item" minimum:"0" maximum:"180" doc:"0 = transkrip suara/video mati"`
	AISuggestReminds bool  `json:"ai_suggested_reminders"`
	BriefingChapters int   `json:"briefing_chapters" minimum:"1" maximum:"10"`
}

func ConfigFromRules(r PlanRules) PlanRulesConfig {
	return PlanRulesConfig{MaxUploadMB: r.MaxUploadMB, TrashDays: int(r.TrashRetention.Hours() / 24), ChatHistoryDays: int(r.ChatHistory.Hours() / 24),
		STTMaxMinutes: r.STTMaxMinutes, AISuggestReminds: r.AISuggestReminds, BriefingChapters: r.BriefingChapters}
}

// apply menimpa field yang bisa diubah admin; QueuePriority tetap dari base.
func (c PlanRulesConfig) apply(base PlanRules) PlanRules {
	base.MaxUploadMB = c.MaxUploadMB
	base.TrashRetention = time.Duration(c.TrashDays) * 24 * time.Hour
	base.ChatHistory = time.Duration(c.ChatHistoryDays) * 24 * time.Hour
	base.STTMaxMinutes = c.STTMaxMinutes
	base.AISuggestReminds = c.AISuggestReminds
	base.BriefingChapters = c.BriefingChapters
	return base
}

// Settings: pengaturan platform efektif.
type Settings struct {
	ProEnabled bool                       `json:"pro_enabled" doc:"false = semua UI upgrade/Pro disembunyikan"`
	PlanRules  map[string]PlanRulesConfig `json:"plan_rules"`
	UpdatedAt  *time.Time                 `json:"-"`
}

const (
	settingProEnabled = "pro_enabled"
	settingPlanRules  = "plan_rules"
)

// SettingsService membaca app_settings dengan cache singkat. API dan worker sama-sama
// memanggil Run supaya RulesFor ikut berubah tanpa restart.
type SettingsService struct {
	repos  *repository.Repos
	mu     sync.RWMutex
	cur    Settings
	loaded time.Time
}

func NewSettingsService(repos *repository.Repos) *SettingsService {
	return &SettingsService{repos: repos, cur: defaults()}
}

func defaults() Settings {
	pr := map[string]PlanRulesConfig{}
	for p, r := range defaultPlanRules {
		pr[p] = ConfigFromRules(r)
	}
	return Settings{ProEnabled: true, PlanRules: pr}
}

// Reload membaca ulang dari DB dan memasang aturan plan efektif.
func (s *SettingsService) Reload(ctx context.Context) error {
	raw, err := s.repos.Settings.All(ctx)
	if err != nil {
		return err
	}
	st := defaults()
	if v, ok := raw[settingProEnabled]; ok {
		_ = json.Unmarshal(v, &st.ProEnabled)
	}
	if v, ok := raw[settingPlanRules]; ok {
		var over map[string]PlanRulesConfig
		if err := json.Unmarshal(v, &over); err == nil {
			for p, c := range over {
				if _, known := defaultPlanRules[p]; known {
					st.PlanRules[p] = c
				}
			}
		}
	}
	rules := map[string]PlanRules{}
	for p, base := range defaultPlanRules {
		rules[p] = st.PlanRules[p].apply(base)
	}
	SetPlanRules(rules)
	s.mu.Lock()
	s.cur, s.loaded = st, time.Now()
	s.mu.Unlock()
	return nil
}

// Get mengembalikan pengaturan; dibaca ulang bila cache lebih tua dari 30 detik.
func (s *SettingsService) Get(ctx context.Context) Settings {
	s.mu.RLock()
	fresh := time.Since(s.loaded) < 30*time.Second
	cur := s.cur
	s.mu.RUnlock()
	if !fresh {
		if err := s.Reload(ctx); err != nil {
			slog.WarnContext(ctx, "muat pengaturan gagal; pakai nilai terakhir", "err", err)
			return cur
		}
		s.mu.RLock()
		cur = s.cur
		s.mu.RUnlock()
	}
	return cur
}

func (s *SettingsService) ProEnabled(ctx context.Context) bool { return s.Get(ctx).ProEnabled }

func (s *SettingsService) SetProEnabled(ctx context.Context, by int64, on bool) error {
	if err := s.repos.Settings.Set(ctx, settingProEnabled, on, by); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// SetPlanRules menyimpan aturan satu plan. maxUploadMB = batas body server.
func (s *SettingsService) SetPlanRules(ctx context.Context, by int64, plan string, c PlanRulesConfig, maxUploadMB int64) error {
	if _, ok := defaultPlanRules[plan]; !ok {
		return model.Invalid("plan %q tidak dikenal", plan)
	}
	if c.MaxUploadMB < 1 || (maxUploadMB > 0 && c.MaxUploadMB > maxUploadMB) {
		return model.Invalid("ukuran upload harus 1–%d MB", maxUploadMB)
	}
	if c.TrashDays < 1 || c.BriefingChapters < 1 || c.ChatHistoryDays < 0 || c.STTMaxMinutes < 0 {
		return model.Invalid("nilai aturan tidak valid")
	}
	all := s.Get(ctx).PlanRules
	next := map[string]PlanRulesConfig{}
	for p, v := range all {
		next[p] = v
	}
	next[plan] = c
	if err := s.repos.Settings.Set(ctx, settingPlanRules, next, by); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// Run memuat ulang berkala sampai ctx selesai (aturan plan di proses worker ikut segar).
func (s *SettingsService) Run(ctx context.Context) {
	if err := s.Reload(ctx); err != nil {
		slog.WarnContext(ctx, "muat pengaturan awal gagal", "err", err)
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Reload(ctx); err != nil {
				slog.WarnContext(ctx, "muat ulang pengaturan gagal", "err", err)
			}
		}
	}
}
