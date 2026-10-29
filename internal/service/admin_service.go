package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
)

// AdminService: konsol admin web (ADR-021). Satu-satunya service yang membaca lintas
// user; setiap handler-nya sudah memastikan role admin. Semua perubahan dicatat di
// audit_logs dengan action "admin.*" dan user_id = admin pelaku.
type AdminService struct {
	repos       *repository.Repos
	auth        *AuthService
	ent         *EntitlementService
	settings    *SettingsService
	maxUploadMB int64
}

func NewAdminService(repos *repository.Repos, auth *AuthService, ent *EntitlementService, settings *SettingsService, maxUploadMB int64) *AdminService {
	return &AdminService{repos: repos, auth: auth, ent: ent, settings: settings, maxUploadMB: maxUploadMB}
}

var metrics = []string{MetricItems, MetricQuestions, MetricImages, MetricSTTMinutes, MetricFiles}

func validMetric(m string) bool {
	for _, x := range metrics {
		if x == m {
			return true
		}
	}
	return false
}

func (s *AdminService) audit(ctx context.Context, admin model.User, action, entity string, id *int64, meta map[string]any) {
	_ = s.repos.Audit.Log(ctx, &admin.ID, "admin."+action, entity, id, meta, "")
}

func (s *AdminService) Overview(ctx context.Context, days int) (model.AdminOverview, error) {
	return s.repos.Admin.Overview(ctx, days)
}

// ---- pengguna ----

func (s *AdminService) Users(ctx context.Context, f model.AdminUserFilter) ([]model.AdminUserRow, int, error) {
	f.Q = strings.TrimSpace(f.Q)
	return s.repos.Admin.Users(ctx, f)
}

func (s *AdminService) User(ctx context.Context, id int64) (model.AdminUserDetail, error) {
	var d model.AdminUserDetail
	row, err := s.repos.Admin.User(ctx, id)
	if err != nil {
		return d, err
	}
	d.User = row
	u, err := s.repos.Users.ByID(ctx, id)
	if err != nil {
		return d, err
	}
	if d.Quotas, err = s.ent.Quotas(ctx, u); err != nil {
		return d, fmt.Errorf("kuota user: %w", err)
	}
	if d.Stats, err = s.repos.Admin.UserStats(ctx, id); err != nil {
		return d, err
	}
	if d.Subscriptions, err = s.repos.Admin.UserSubscriptions(ctx, id); err != nil {
		return d, err
	}
	if d.Redemptions, err = s.repos.Admin.UserRedemptions(ctx, id); err != nil {
		return d, err
	}
	if d.Activity, err = s.repos.Admin.Audit(ctx, id, false, 25); err != nil {
		return d, err
	}
	return d, nil
}

type AdminUserPatch struct {
	Role          *string
	Suspended     *bool
	EmailVerified *bool
}

func (s *AdminService) UpdateUser(ctx context.Context, admin model.User, id int64, p AdminUserPatch) error {
	if id == admin.ID {
		return model.Invalid("tidak bisa mengubah role atau status akunmu sendiri")
	}
	if p.Role != nil {
		if *p.Role != "user" && *p.Role != "admin" {
			return model.Invalid("role harus user atau admin")
		}
		if err := s.repos.Admin.SetRole(ctx, id, *p.Role); err != nil {
			return err
		}
		s.audit(ctx, admin, "user.role", "user", &id, map[string]any{"role": *p.Role})
	}
	if p.Suspended != nil {
		if err := s.repos.Admin.SetSuspended(ctx, id, *p.Suspended); err != nil {
			return err
		}
		act := "user.unsuspend"
		if *p.Suspended {
			act = "user.suspend"
		}
		s.audit(ctx, admin, act, "user", &id, nil)
	}
	if p.EmailVerified != nil {
		if err := s.repos.Users.SetVerified(ctx, id, *p.EmailVerified); err != nil {
			return err
		}
		s.audit(ctx, admin, "user.email_verified", "user", &id, map[string]any{"verified": *p.EmailVerified})
	}
	return nil
}

// SetPassword: admin mengganti password user (mis. user tidak bisa menerima email). Semua sesinya keluar.
func (s *AdminService) SetPassword(ctx context.Context, admin model.User, id int64, pass string) error {
	if id == admin.ID {
		return model.Invalid("ganti password akunmu sendiri lewat Pengaturan")
	}
	if err := s.auth.AdminSetPassword(ctx, id, pass); err != nil {
		return err
	}
	s.audit(ctx, admin, "user.password_set", "user", &id, nil)
	return nil
}

// SendEmail mengirim kode verifikasi ("verify") atau reset password ("reset") ke pengguna.
func (s *AdminService) SendEmail(ctx context.Context, admin model.User, id int64, kind string) error {
	if err := s.auth.AdminSendEmail(ctx, id, kind); err != nil {
		return err
	}
	s.audit(ctx, admin, "user.email_"+kind, "user", &id, nil)
	return nil
}

// GrantPro memberi Pro manual; tetap boleh walau Pro ditutup (mis. untuk penguji).
func (s *AdminService) GrantPro(ctx context.Context, admin model.User, id int64, days int) (time.Time, error) {
	if days < 1 || days > 3650 {
		return time.Time{}, model.Invalid("jumlah hari 1–3650")
	}
	if _, err := s.repos.Users.ByID(ctx, id); err != nil {
		return time.Time{}, err
	}
	ref := fmt.Sprintf("admin-%d-%d", id, time.Now().UnixNano())
	end, err := grantManualPro(ctx, s.repos, id, days, ref)
	if err != nil {
		return end, err
	}
	s.audit(ctx, admin, "user.pro_grant", "user", &id, map[string]any{"days": days, "until": end})
	return end, nil
}

// RevokePro mengakhiri Pro manual. Langganan toko (RevenueCat) tidak bisa dicabut dari sini.
func (s *AdminService) RevokePro(ctx context.Context, admin model.User, id int64) error {
	n, err := s.repos.Billing.RevokeManualPro(ctx, id)
	if err != nil {
		return err
	}
	if err := syncPlan(ctx, s.repos, id); err != nil {
		return err
	}
	s.audit(ctx, admin, "user.pro_revoke", "user", &id, map[string]any{"n": n})
	return nil
}

func (s *AdminService) ResetQuota(ctx context.Context, admin model.User, id int64, metric string) error {
	if metric != "" && !validMetric(metric) {
		return model.Invalid("metric %q tidak dikenal", metric)
	}
	u, err := s.repos.Users.ByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ent.ResetUsage(ctx, u, metric); err != nil {
		return err
	}
	s.audit(ctx, admin, "user.quota_reset", "user", &id, map[string]any{"metric": metric})
	return nil
}

func (s *AdminService) AddBonus(ctx context.Context, admin model.User, id int64, metric string, amount int) error {
	if !validMetric(metric) {
		return model.Invalid("metric %q tidak dikenal", metric)
	}
	if amount == 0 || amount > 100000 || amount < -100000 {
		return model.Invalid("jumlah tidak valid")
	}
	u, err := s.repos.Users.ByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ent.AddBonus(ctx, u, metric, amount); err != nil {
		return err
	}
	s.audit(ctx, admin, "user.quota_bonus", "user", &id, map[string]any{"metric": metric, "amount": amount})
	return nil
}

// ---- pembayaran ----

func (s *AdminService) Subscriptions(ctx context.Context, f model.AdminSubscriptionFilter) ([]model.AdminSubscription, int, model.AdminSubscriptionSummary, error) {
	f.Q = strings.TrimSpace(f.Q)
	list, total, err := s.repos.Admin.Subscriptions(ctx, f)
	if err != nil {
		return nil, 0, model.AdminSubscriptionSummary{}, err
	}
	sum, err := s.repos.Admin.SubscriptionSummary(ctx)
	return list, total, sum, err
}

// ---- kupon ----

var couponCode = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{2,31}$`)

func NormalizeCoupon(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func (s *AdminService) Coupons(ctx context.Context) ([]model.Coupon, error) {
	return s.repos.Admin.Coupons(ctx)
}

func (s *AdminService) CreateCoupon(ctx context.Context, admin model.User, c model.Coupon) (model.Coupon, error) {
	c.Code = NormalizeCoupon(c.Code)
	if !couponCode.MatchString(c.Code) {
		return c, model.Invalid("kode 3–32 karakter: huruf, angka, - atau _")
	}
	switch c.Kind {
	case "pro_days":
		c.Metric = ""
		if c.Amount < 1 || c.Amount > 3650 {
			return c, model.Invalid("jumlah hari Pro 1–3650")
		}
	case "bonus":
		if !validMetric(c.Metric) {
			return c, model.Invalid("pilih kuota yang ditambah")
		}
		if c.Amount < 1 || c.Amount > 100000 {
			return c, model.Invalid("jumlah bonus tidak valid")
		}
	default:
		return c, model.Invalid("jenis kupon tidak dikenal")
	}
	if c.MaxRedemptions < 0 {
		return c, model.Invalid("batas pemakaian tidak valid")
	}
	if c.StartsAt != nil && c.ExpiresAt != nil && !c.ExpiresAt.After(*c.StartsAt) {
		return c, model.Invalid("tanggal berakhir harus setelah tanggal mulai")
	}
	out, err := s.repos.Admin.CreateCoupon(ctx, c, admin.ID)
	if errors.Is(err, model.ErrConflict) {
		return out, fmt.Errorf("%w: kode %s sudah dipakai", model.ErrConflict, c.Code)
	}
	if err != nil {
		return out, err
	}
	s.audit(ctx, admin, "coupon.create", "coupon", &out.ID, map[string]any{"code": out.Code, "kind": out.Kind, "amount": out.Amount})
	return out, nil
}

func (s *AdminService) SetCouponActive(ctx context.Context, admin model.User, id int64, active bool) (model.Coupon, error) {
	c, err := s.repos.Admin.SetCouponActive(ctx, id, active)
	if err != nil {
		return c, err
	}
	s.audit(ctx, admin, "coupon.active", "coupon", &id, map[string]any{"active": active})
	return c, nil
}

func (s *AdminService) DeleteCoupon(ctx context.Context, admin model.User, id int64) error {
	if err := s.repos.Admin.DeleteCoupon(ctx, id); err != nil {
		if errors.Is(err, model.ErrConflict) {
			return fmt.Errorf("%w: kupon sudah dipakai; nonaktifkan saja", model.ErrConflict)
		}
		return err
	}
	s.audit(ctx, admin, "coupon.delete", "coupon", &id, nil)
	return nil
}

// RedeemCoupon dipakai user biasa (POST /coupons/redeem).
type RedeemResult struct {
	Kind     string     `json:"kind" enum:"pro_days,bonus"`
	Metric   string     `json:"metric,omitempty"`
	Amount   int        `json:"amount"`
	ProUntil *time.Time `json:"pro_until,omitempty"`
	Message  string     `json:"message"`
	Plan     string     `json:"plan"`
}

func (s *AdminService) RedeemCoupon(ctx context.Context, u model.User, code string) (RedeemResult, error) {
	var res RedeemResult
	code = NormalizeCoupon(code)
	if code == "" {
		return res, model.Invalid("masukkan kode kupon")
	}
	proOn := s.settings == nil || s.settings.ProEnabled(ctx)
	err := s.repos.Tx(ctx, func(tx *repository.Repos) error {
		c, err := tx.Admin.CouponForRedeem(ctx, code)
		if errors.Is(err, model.ErrNotFound) {
			return model.Invalid("kode kupon tidak ditemukan")
		}
		if err != nil {
			return err
		}
		now := time.Now()
		switch {
		case !c.Active:
			return model.Invalid("kupon ini sudah tidak aktif")
		case c.StartsAt != nil && now.Before(*c.StartsAt):
			return model.Invalid("kupon ini belum bisa dipakai")
		case c.ExpiresAt != nil && now.After(*c.ExpiresAt):
			return model.Invalid("kupon ini sudah kedaluwarsa")
		case c.MaxRedemptions > 0 && c.Redeemed >= c.MaxRedemptions:
			return model.Invalid("kuota pemakaian kupon ini sudah habis")
		case c.Kind == "pro_days" && !proOn:
			return model.Invalid("kupon ini belum bisa dipakai")
		}
		if err := tx.Admin.Redeem(ctx, c.ID, u.ID); err != nil {
			if errors.Is(err, model.ErrConflict) {
				return model.Invalid("kamu sudah memakai kupon ini")
			}
			return err
		}
		res.Kind, res.Metric, res.Amount = c.Kind, c.Metric, c.Amount
		if c.Kind == "pro_days" {
			end, err := grantManualPro(ctx, tx, u.ID, c.Amount, fmt.Sprintf("coupon-%d-%d", c.ID, u.ID))
			if err != nil {
				return err
			}
			res.ProUntil = &end
			res.Message = fmt.Sprintf("Pro aktif sampai %s.", end.In(u.Location()).Format("2 Jan 2006"))
			return nil
		}
		key, err := s.ent.PeriodKey(ctx, u, c.Metric)
		if err != nil {
			return err
		}
		if err := tx.Billing.AddBonus(ctx, u.ID, c.Metric, key, c.Amount); err != nil {
			return err
		}
		res.Message = fmt.Sprintf("Kuota %s bertambah %d untuk periode ini.", MetricLabel(c.Metric), c.Amount)
		return nil
	})
	if err != nil {
		return res, err
	}
	_ = s.repos.Audit.Log(ctx, &u.ID, "coupon.redeem", "coupon", nil, map[string]any{"code": code}, "")
	fresh, err := s.repos.Users.ByID(ctx, u.ID)
	if err != nil {
		return res, err
	}
	res.Plan = fresh.Plan
	return res, nil
}

// MetricLabel: nama metric untuk pesan ke user.
func MetricLabel(m string) string {
	switch m {
	case MetricItems:
		return "simpanan"
	case MetricQuestions:
		return "pertanyaan"
	case MetricImages:
		return "gambar"
	case MetricSTTMinutes:
		return "menit transkrip"
	case MetricFiles:
		return "file"
	}
	return m
}

// ---- pengaturan & paket ----

func (s *AdminService) Settings(ctx context.Context) Settings { return s.settings.Get(ctx) }

func (s *AdminService) SetProEnabled(ctx context.Context, admin model.User, on bool) error {
	if err := s.settings.SetProEnabled(ctx, admin.ID, on); err != nil {
		return err
	}
	s.audit(ctx, admin, "settings.pro_enabled", "settings", nil, map[string]any{"value": on})
	return nil
}

type PlanLimitInput struct {
	Metric string `json:"metric"`
	Period string `json:"period" enum:"day,month"`
	Limit  int    `json:"limit" minimum:"-1" doc:"-1 = tanpa batas"`
}

// AdminPlan: limit kuota + aturan satu plan untuk halaman Paket & Limit.
type AdminPlan struct {
	Plan   string           `json:"plan"`
	Limits []PlanLimitInput `json:"limits"`
	Rules  PlanRulesConfig  `json:"rules"`
	Users  int              `json:"users"`
}

func (s *AdminService) Plans(ctx context.Context) ([]AdminPlan, error) {
	all, err := s.repos.Billing.PlanLimits(ctx)
	if err != nil {
		return nil, err
	}
	st := s.settings.Get(ctx)
	counts, err := s.repos.Admin.PlanUserCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := []AdminPlan{}
	for _, p := range []string{"free", "pro"} {
		ap := AdminPlan{Plan: p, Limits: []PlanLimitInput{}, Rules: st.PlanRules[p], Users: counts[p]}
		for _, m := range metrics {
			for _, l := range all {
				if l.Plan == p && l.Metric == m {
					ap.Limits = append(ap.Limits, PlanLimitInput{Metric: m, Period: l.Period, Limit: l.Limit})
				}
			}
		}
		out = append(out, ap)
	}
	return out, nil
}

func (s *AdminService) UpdatePlan(ctx context.Context, admin model.User, plan string, limits []PlanLimitInput, rules *PlanRulesConfig) error {
	if plan != "free" && plan != "pro" {
		return model.Invalid("plan %q tidak dikenal", plan)
	}
	for _, l := range limits {
		if !validMetric(l.Metric) {
			return model.Invalid("metric %q tidak dikenal", l.Metric)
		}
		if l.Period != "day" && l.Period != "month" {
			return model.Invalid("periode harus day atau month")
		}
		if l.Limit < -1 {
			return model.Invalid("limit minimal -1 (tanpa batas)")
		}
	}
	if len(limits) > 0 {
		if err := s.repos.Admin.UpsertPlanLimits(ctx, plan, limits2model(plan, limits)); err != nil {
			return err
		}
		s.ent.InvalidateLimits()
	}
	if rules != nil {
		if err := s.settings.SetPlanRules(ctx, admin.ID, plan, *rules, s.maxUploadMB); err != nil {
			return err
		}
	}
	s.audit(ctx, admin, "plan.update", "plan", nil, map[string]any{"plan": plan, "limits": limits, "rules": rules})
	return nil
}

func limits2model(plan string, in []PlanLimitInput) []model.PlanLimit {
	out := make([]model.PlanLimit, 0, len(in))
	for _, l := range in {
		out = append(out, model.PlanLimit{Plan: plan, Metric: l.Metric, Period: l.Period, Limit: l.Limit})
	}
	return out
}

// ---- laporan ----

func (s *AdminService) Report(ctx context.Context, days int) (model.AdminReport, error) {
	return s.repos.Admin.Report(ctx, days)
}

func (s *AdminService) Audit(ctx context.Context, limit int) ([]model.AdminAuditEntry, error) {
	return s.repos.Admin.Audit(ctx, 0, true, limit)
}
