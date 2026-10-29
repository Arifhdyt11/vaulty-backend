package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
)

// BillingService: sumber langganan mobile = RevenueCat (ADR-011). users.plan di-sync dari subscriptions.
type BillingService struct {
	repos  *repository.Repos
	apiKey string
	http   *http.Client
	rcBase string
}

func NewBillingService(repos *repository.Repos, revenueCatAPIKey string) *BillingService {
	return &BillingService{repos: repos, apiKey: revenueCatAPIKey, http: &http.Client{Timeout: 15 * time.Second}, rcBase: "https://api.revenuecat.com/v1"}
}

// RevenueCatEvent: subset payload webhook RevenueCat.
type RevenueCatEvent struct {
	Event struct {
		Type                  string   `json:"type"`
		ID                    string   `json:"id"`
		AppUserID             string   `json:"app_user_id"`
		OriginalAppUserID     string   `json:"original_app_user_id"`
		ProductID             string   `json:"product_id"`
		EntitlementIDs        []string `json:"entitlement_ids"`
		ExpirationAtMs        *int64   `json:"expiration_at_ms"`
		Store                 string   `json:"store"`
		TransactionID         string   `json:"transaction_id"`
		OriginalTransactionID string   `json:"original_transaction_id"`
	} `json:"event"`
}

// HandleRevenueCatWebhook memetakan event → status langganan, lalu sinkron plan user. Idempoten per transaksi.
func (s *BillingService) HandleRevenueCatWebhook(ctx context.Context, raw []byte) error {
	var ev RevenueCatEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return model.Invalid("payload webhook tidak valid")
	}
	userID := parseAppUserID(ev.Event.AppUserID, ev.Event.OriginalAppUserID)
	if userID == 0 {
		slog.WarnContext(ctx, "webhook revenuecat tanpa app_user_id numerik", "type", ev.Event.Type)
		return nil
	}
	if _, err := s.repos.Users.ByID(ctx, userID); err != nil {
		return nil // user sudah dihapus: abaikan
	}
	var periodEnd *time.Time
	if ev.Event.ExpirationAtMs != nil {
		t := time.UnixMilli(*ev.Event.ExpirationAtMs)
		periodEnd = &t
	}
	status := ""
	switch ev.Event.Type {
	case "INITIAL_PURCHASE", "RENEWAL", "PRODUCT_CHANGE", "UNCANCELLATION", "NON_RENEWING_PURCHASE", "TRANSFER":
		status = "active"
	case "CANCELLATION":
		status = "active" // auto-renew mati, akses tetap sampai period_end
	case "BILLING_ISSUE":
		status = "grace"
	case "EXPIRATION":
		status = "expired"
	case "SUBSCRIBER_ALIAS", "TEST":
		return nil
	default:
		status = "active"
	}
	if periodEnd != nil && periodEnd.Before(time.Now()) && status != "grace" {
		status = "expired"
	}
	ref := ev.Event.OriginalTransactionID
	if ref == "" {
		ref = ev.Event.TransactionID
	}
	if ref == "" {
		ref = "rc-" + fmt.Sprint(userID)
	}
	sub := model.Subscription{UserID: userID, Plan: "pro", Provider: "revenuecat", ProviderRef: ref, ProductID: ev.Event.ProductID, Status: status, PeriodEnd: periodEnd, Raw: raw}
	if err := s.repos.Billing.UpsertSubscription(ctx, sub); err != nil {
		return fmt.Errorf("simpan langganan: %w", err)
	}
	_ = s.repos.Audit.Log(ctx, &userID, "billing.webhook", "subscription", nil, map[string]any{"type": ev.Event.Type, "status": status}, "")
	return s.SyncPlan(ctx, userID)
}

func parseAppUserID(ids ...string) int64 {
	for _, s := range ids {
		s = strings.TrimSpace(s)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// SyncPlan menghitung ulang users.plan dari subscriptions.
func (s *BillingService) SyncPlan(ctx context.Context, userID int64) error {
	return syncPlan(ctx, s.repos, userID)
}

// syncPlan terpisah supaya bisa dipakai dengan Repos transaksi (kupon, admin).
func syncPlan(ctx context.Context, repos *repository.Repos, userID int64) error {
	pro, err := repos.Billing.HasActivePro(ctx, userID)
	if err != nil {
		return err
	}
	plan := "free"
	if pro {
		plan = "pro"
	}
	return repos.Users.SetPlan(ctx, userID, plan)
}

// grantManualPro menambah langganan Pro manual selama days hari, memperpanjang
// sisa Pro manual yang masih berjalan.
func grantManualPro(ctx context.Context, repos *repository.Repos, userID int64, days int, ref string) (time.Time, error) {
	start := time.Now()
	if end, err := repos.Billing.ManualProEnd(ctx, userID); err != nil {
		return time.Time{}, err
	} else if end != nil && end.After(start) {
		start = *end
	}
	end := start.Add(time.Duration(days) * 24 * time.Hour)
	if err := repos.Billing.UpsertSubscription(ctx, model.Subscription{UserID: userID, Plan: "pro", Provider: "manual",
		ProviderRef: ref, ProductID: "manual", Status: "active", PeriodEnd: &end}); err != nil {
		return time.Time{}, fmt.Errorf("simpan pro manual: %w", err)
	}
	return end, syncPlan(ctx, repos, userID)
}

// SyncFromRevenueCat memaksa cek ke REST RevenueCat (dipanggil app setelah pembelian).
func (s *BillingService) SyncFromRevenueCat(ctx context.Context, u model.User) (string, error) {
	if s.apiKey == "" {
		if err := s.SyncPlan(ctx, u.ID); err != nil {
			return "", err
		}
		fresh, _ := s.repos.Users.ByID(ctx, u.ID)
		return fresh.Plan, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.rcBase+"/subscribers/"+fmt.Sprint(u.ID), nil)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	res, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("revenuecat: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return "", fmt.Errorf("revenuecat: status %d: %s", res.StatusCode, string(b))
	}
	var out struct {
		Subscriber struct {
			Entitlements map[string]struct {
				ExpiresDate       *time.Time `json:"expires_date"`
				ProductIdentifier string     `json:"product_identifier"`
			} `json:"entitlements"`
		} `json:"subscriber"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("revenuecat decode: %w", err)
	}
	if e, ok := out.Subscriber.Entitlements["pro"]; ok {
		status := "active"
		if e.ExpiresDate != nil && e.ExpiresDate.Before(time.Now()) {
			status = "expired"
		}
		_ = s.repos.Billing.UpsertSubscription(ctx, model.Subscription{UserID: u.ID, Plan: "pro", Provider: "revenuecat", ProviderRef: "rc-sync-" + fmt.Sprint(u.ID),
			ProductID: e.ProductIdentifier, Status: status, PeriodEnd: e.ExpiresDate})
	}
	if err := s.SyncPlan(ctx, u.ID); err != nil {
		return "", err
	}
	fresh, err := s.repos.Users.ByID(ctx, u.ID)
	if err != nil {
		return "", err
	}
	return fresh.Plan, nil
}

// DowngradeExpired: pekerjaan terjadwal untuk user pro yang langganannya habis tanpa webhook.
func (s *BillingService) DowngradeExpired(ctx context.Context) (int, error) {
	ids, err := s.repos.Billing.ExpiredProUsers(ctx, 200)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.repos.Users.SetPlan(ctx, id, "free"); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
