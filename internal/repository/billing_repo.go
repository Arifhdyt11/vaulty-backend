package repository

import (
	"context"
	"encoding/json"
	"time"

	"vaulty/internal/model"
)

type BillingRepo struct{ db DB }

func (r *BillingRepo) PlanLimits(ctx context.Context) ([]model.PlanLimit, error) {
	rows, err := r.db.Query(ctx, `SELECT plan, metric, period, "limit" FROM plan_limits ORDER BY plan, metric`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PlanLimit
	for rows.Next() {
		var p model.PlanLimit
		if err := rows.Scan(&p.Plan, &p.Metric, &p.Period, &p.Limit); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Usage mengembalikan pemakaian dan kuota tambahan (bonus) untuk satu periode.
func (r *BillingRepo) Usage(ctx context.Context, userID int64, metric, period string) (used, bonus int, err error) {
	err = r.db.QueryRow(ctx, `SELECT coalesce(max(used), 0), coalesce(max(bonus), 0) FROM usage_counters WHERE user_id = $1 AND metric = $2 AND period = $3`,
		userID, metric, period).Scan(&used, &bonus)
	return used, bonus, err
}

// Consume menambah pemakaian dan mengembalikan nilai baru beserta bonus (atomik).
func (r *BillingRepo) Consume(ctx context.Context, userID int64, metric, period string, n int) (used, bonus int, err error) {
	err = r.db.QueryRow(ctx, `INSERT INTO usage_counters (user_id, metric, period, used) VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, metric, period) DO UPDATE SET used = usage_counters.used + EXCLUDED.used RETURNING used, bonus`, userID, metric, period, n).Scan(&used, &bonus)
	return used, bonus, err
}

// ResetUsage mengosongkan pemakaian satu periode; bonus tetap.
func (r *BillingRepo) ResetUsage(ctx context.Context, userID int64, metric, period string) error {
	_, err := r.db.Exec(ctx, `UPDATE usage_counters SET used = 0 WHERE user_id = $1 AND metric = $2 AND period = $3`, userID, metric, period)
	return err
}

// AddBonus menambah kuota tambahan satu periode.
func (r *BillingRepo) AddBonus(ctx context.Context, userID int64, metric, period string, n int) error {
	_, err := r.db.Exec(ctx, `INSERT INTO usage_counters (user_id, metric, period, bonus) VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, metric, period) DO UPDATE SET bonus = usage_counters.bonus + EXCLUDED.bonus`, userID, metric, period, n)
	return err
}

// RevokeManualPro mengakhiri langganan Pro manual (dari admin/kupon); langganan toko tidak disentuh.
func (r *BillingRepo) RevokeManualPro(ctx context.Context, userID int64) (int64, error) {
	t, err := r.db.Exec(ctx, `UPDATE subscriptions SET status = 'cancelled', period_end = now(), updated_at = now()
		WHERE user_id = $1 AND provider = 'manual' AND status IN ('active','grace')`, userID)
	return t.RowsAffected(), err
}

// ManualProEnd: akhir langganan Pro manual yang masih aktif (untuk memperpanjang).
func (r *BillingRepo) ManualProEnd(ctx context.Context, userID int64) (*time.Time, error) {
	var t *time.Time
	err := r.db.QueryRow(ctx, `SELECT max(period_end) FROM subscriptions WHERE user_id = $1 AND provider = 'manual'
		AND status = 'active' AND period_end > now()`, userID).Scan(&t)
	return t, err
}

func (r *BillingRepo) UpsertSubscription(ctx context.Context, s model.Subscription) error {
	raw := s.Raw
	if raw == nil {
		raw = json.RawMessage(`{}`)
	}
	_, err := r.db.Exec(ctx, `INSERT INTO subscriptions (user_id, plan, provider, provider_ref, product_id, status, period_end, raw)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (provider, provider_ref) WHERE provider_ref <> '' DO UPDATE SET plan = EXCLUDED.plan, product_id = EXCLUDED.product_id,
		  status = EXCLUDED.status, period_end = EXCLUDED.period_end, raw = EXCLUDED.raw, user_id = EXCLUDED.user_id, updated_at = now()`,
		s.UserID, s.Plan, s.Provider, s.ProviderRef, s.ProductID, s.Status, s.PeriodEnd, raw)
	return err
}

// HasActivePro: ada langganan pro yang aktif/grace dan belum lewat period_end.
func (r *BillingRepo) HasActivePro(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM subscriptions WHERE user_id = $1 AND plan = 'pro' AND status IN ('active','grace')
		AND (period_end IS NULL OR period_end > now()))`, userID).Scan(&ok)
	return ok, err
}

// ExpiredProUsers: user dengan plan pro yang langganannya sudah tidak aktif (untuk downgrade terjadwal).
func (r *BillingRepo) ExpiredProUsers(ctx context.Context, limit int) ([]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id FROM users u WHERE u.plan = 'pro' AND u.deleted_at IS NULL AND NOT EXISTS (
		SELECT 1 FROM subscriptions s WHERE s.user_id = u.id AND s.plan = 'pro' AND s.status IN ('active','grace') AND (s.period_end IS NULL OR s.period_end > now())
	) LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- export jobs ----

func (r *BillingRepo) CreateExport(ctx context.Context, userID int64) (model.ExportJob, error) {
	var j model.ExportJob
	err := r.db.QueryRow(ctx, `INSERT INTO export_jobs (user_id) VALUES ($1) RETURNING id, status, created_at`, userID).Scan(&j.ID, &j.Status, &j.CreatedAt)
	return j, err
}

func (r *BillingRepo) Export(ctx context.Context, userID, id int64) (model.ExportJob, *string, error) {
	var j model.ExportJob
	var key *string
	err := r.db.QueryRow(ctx, `SELECT id, status, file_key, created_at FROM export_jobs WHERE id = $1 AND user_id = $2`, id, userID).Scan(&j.ID, &j.Status, &key, &j.CreatedAt)
	return j, key, mapErr(err)
}

func (r *BillingRepo) FinishExport(ctx context.Context, id int64, status string, key *string, errMsg *string) error {
	_, err := r.db.Exec(ctx, `UPDATE export_jobs SET status = $2, file_key = $3, error = $4, updated_at = now() WHERE id = $1`, id, status, key, errMsg)
	return err
}
