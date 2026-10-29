package repository

import (
	"context"
	"time"

	"vaulty/internal/model"
)

// AdminRepo: agregat lintas user untuk dashboard admin. Satu-satunya repo yang
// sengaja tidak memfilter user_id (ADR-007); hanya dipanggil handler /admin yang
// sudah memastikan role = admin.
type AdminRepo struct{ db DB }

func (r *AdminRepo) Overview(ctx context.Context, days int) (model.AdminOverview, error) {
	var o model.AdminOverview
	err := r.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM users WHERE deleted_at IS NULL),
		  (SELECT count(*) FROM users WHERE deleted_at IS NULL AND plan = 'pro'),
		  (SELECT count(*) FROM users WHERE deleted_at IS NULL AND created_at >= now() - interval '30 days'),
		  (SELECT count(*) FROM items WHERE deleted_at IS NULL),
		  (SELECT count(*) FROM items WHERE deleted_at IS NULL AND status = 'ready'),
		  (SELECT count(*) FROM items WHERE deleted_at IS NULL AND status IN ('queued','processing')),
		  (SELECT count(*) FROM items WHERE deleted_at IS NULL AND status IN ('failed','partial')),
		  (SELECT count(*) FROM items WHERE deleted_at IS NULL AND created_at >= now() - interval '30 days'),
		  (SELECT count(*) FROM reminders WHERE status = 'pending'),
		  (SELECT count(*) FROM conversations WHERE deleted_at IS NULL)`).
		Scan(&o.Users, &o.ProUsers, &o.NewUsers30d, &o.Items, &o.ItemsReady, &o.ItemsProcessing, &o.ItemsFailed,
			&o.Items30d, &o.RemindersPending, &o.Conversations)
	if err != nil {
		return o, mapErr(err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT d::date, coalesce(i.n, 0), coalesce(u.n, 0)
		FROM generate_series((now() - ($1::int - 1) * interval '1 day')::date, now()::date, interval '1 day') d
		LEFT JOIN (SELECT created_at::date dd, count(*) n FROM items WHERE deleted_at IS NULL AND created_at >= now() - $1::int * interval '1 day' GROUP BY 1) i ON i.dd = d::date
		LEFT JOIN (SELECT created_at::date dd, count(*) n FROM users WHERE deleted_at IS NULL AND created_at >= now() - $1::int * interval '1 day' GROUP BY 1) u ON u.dd = d::date
		ORDER BY d`, days)
	if err != nil {
		return o, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p model.AdminDayPoint
		var d time.Time
		if err := rows.Scan(&d, &p.Items, &p.Users); err != nil {
			return o, err
		}
		p.Date = d.Format("2006-01-02")
		o.Daily = append(o.Daily, p)
	}
	if o.Daily == nil {
		o.Daily = []model.AdminDayPoint{}
	}
	rows2, err := r.db.Query(ctx, `
		SELECT u.id, u.email, u.name, u.plan, u.role, u.created_at,
		  (SELECT count(*) FROM items i WHERE i.user_id = u.id AND i.deleted_at IS NULL)
		FROM users u WHERE u.deleted_at IS NULL ORDER BY u.created_at DESC LIMIT 8`)
	if err != nil {
		return o, mapErr(err)
	}
	defer rows2.Close()
	o.RecentUsers = []model.AdminUser{}
	for rows2.Next() {
		var u model.AdminUser
		if err := rows2.Scan(&u.ID, &u.Email, &u.Name, &u.Plan, &u.Role, &u.CreatedAt, &u.Items); err != nil {
			return o, err
		}
		o.RecentUsers = append(o.RecentUsers, u)
	}
	return o, nil
}

// ---- konsol admin (ADR-021) ----

const adminUserCols = `u.id, u.email, u.name, u.avatar_url, u.timezone, u.plan, u.role, u.created_at, u.suspended_at,
	u.email_verified_at IS NOT NULL, u.password_hash IS NOT NULL, u.google_sub IS NOT NULL,
	(SELECT count(*) FROM items i WHERE i.user_id = u.id AND i.deleted_at IS NULL),
	GREATEST((SELECT max(coalesce(s.rotated_at, s.created_at)) FROM sessions s WHERE s.user_id = u.id),
	         (SELECT max(i.created_at) FROM items i WHERE i.user_id = u.id),
	         (SELECT max(m.created_at) FROM messages m WHERE m.user_id = u.id))`

func scanAdminUser(row interface{ Scan(...any) error }, extra ...any) (model.AdminUserRow, error) {
	var u model.AdminUserRow
	dst := append([]any{&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Timezone, &u.Plan, &u.Role, &u.CreatedAt, &u.SuspendedAt, &u.EmailVerified, &u.HasPassword, &u.GoogleLinked, &u.Items, &u.LastActiveAt}, extra...)
	return u, row.Scan(dst...)
}

func (r *AdminRepo) Users(ctx context.Context, f model.AdminUserFilter) ([]model.AdminUserRow, int, error) {
	rows, err := r.db.Query(ctx, `SELECT `+adminUserCols+`, count(*) OVER ()
		FROM users u WHERE u.deleted_at IS NULL
		  AND ($1 = '' OR u.email ILIKE '%' || $1 || '%' OR u.name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR u.plan = $2)
		  AND ($3 = '' OR u.role = $3)
		  AND ($4 = '' OR ($4 = 'suspended' AND u.suspended_at IS NOT NULL) OR ($4 = 'active' AND u.suspended_at IS NULL))
		ORDER BY u.created_at DESC LIMIT $5 OFFSET $6`, f.Q, f.Plan, f.Role, f.Status, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, total := []model.AdminUserRow{}, 0
	for rows.Next() {
		u, err := scanAdminUser(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func (r *AdminRepo) User(ctx context.Context, id int64) (model.AdminUserRow, error) {
	u, err := scanAdminUser(r.db.QueryRow(ctx, `SELECT `+adminUserCols+` FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL`, id))
	return u, mapErr(err)
}

func (r *AdminRepo) UserStats(ctx context.Context, id int64) (model.AdminUserStats, error) {
	var s model.AdminUserStats
	err := r.db.QueryRow(ctx, `SELECT
		  (SELECT count(*) FROM items WHERE user_id = $1 AND deleted_at IS NULL),
		  (SELECT count(*) FROM items WHERE user_id = $1 AND deleted_at IS NULL AND status = 'ready'),
		  (SELECT count(*) FROM items WHERE user_id = $1 AND deleted_at IS NULL AND status IN ('failed','partial')),
		  (SELECT count(*) FROM reminders WHERE user_id = $1 AND status IN ('pending','suggested')),
		  (SELECT count(*) FROM conversations WHERE user_id = $1 AND deleted_at IS NULL),
		  (SELECT count(*) FROM messages WHERE user_id = $1 AND role = 'user' AND created_at >= now() - interval '30 days'),
		  (SELECT count(*) FROM devices WHERE user_id = $1),
		  (SELECT count(*) FROM sessions WHERE user_id = $1 AND refresh_expires_at > now())`, id).
		Scan(&s.Items, &s.ItemsReady, &s.ItemsFailed, &s.Reminders, &s.Conversations, &s.Questions30d, &s.Devices, &s.Sessions)
	return s, err
}

func (r *AdminRepo) SetRole(ctx context.Context, id int64, role string) error {
	t, err := r.db.Exec(ctx, `UPDATE users SET role = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id, role)
	if err == nil && t.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return err
}

// SetSuspended menonaktifkan/mengaktifkan akun; saat dinonaktifkan semua sesinya dihapus.
func (r *AdminRepo) SetSuspended(ctx context.Context, id int64, on bool) error {
	t, err := r.db.Exec(ctx, `UPDATE users SET suspended_at = CASE WHEN $2 THEN coalesce(suspended_at, now()) ELSE NULL END, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, id, on)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	if on {
		_, err = r.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	return err
}

const adminSubCols = `s.id, s.user_id, u.email, u.name, s.plan, s.provider, s.product_id, s.status, s.period_end, s.created_at, s.updated_at`

func scanAdminSub(row interface{ Scan(...any) error }, extra ...any) (model.AdminSubscription, error) {
	var s model.AdminSubscription
	dst := append([]any{&s.ID, &s.UserID, &s.Email, &s.Name, &s.Plan, &s.Provider, &s.ProductID, &s.Status, &s.PeriodEnd, &s.CreatedAt, &s.UpdatedAt}, extra...)
	return s, row.Scan(dst...)
}

func (r *AdminRepo) Subscriptions(ctx context.Context, f model.AdminSubscriptionFilter) ([]model.AdminSubscription, int, error) {
	rows, err := r.db.Query(ctx, `SELECT `+adminSubCols+`, count(*) OVER ()
		FROM subscriptions s JOIN users u ON u.id = s.user_id
		WHERE ($1 = '' OR u.email ILIKE '%' || $1 || '%' OR u.name ILIKE '%' || $1 || '%' OR s.product_id ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR s.status = $2)
		  AND ($3 = '' OR s.provider = $3)
		ORDER BY s.updated_at DESC LIMIT $4 OFFSET $5`, f.Q, f.Status, f.Provider, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, total := []model.AdminSubscription{}, 0
	for rows.Next() {
		s, err := scanAdminSub(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

func (r *AdminRepo) UserSubscriptions(ctx context.Context, userID int64) ([]model.AdminSubscription, error) {
	rows, err := r.db.Query(ctx, `SELECT `+adminSubCols+` FROM subscriptions s JOIN users u ON u.id = s.user_id
		WHERE s.user_id = $1 ORDER BY s.updated_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AdminSubscription{}
	for rows.Next() {
		s, err := scanAdminSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *AdminRepo) SubscriptionSummary(ctx context.Context) (model.AdminSubscriptionSummary, error) {
	var s model.AdminSubscriptionSummary
	err := r.db.QueryRow(ctx, `SELECT
		  count(*) FILTER (WHERE status = 'active'),
		  count(*) FILTER (WHERE status = 'grace'),
		  count(*) FILTER (WHERE status = 'expired'),
		  count(*) FILTER (WHERE status = 'cancelled'),
		  count(*) FILTER (WHERE provider = 'manual' AND status = 'active' AND (period_end IS NULL OR period_end > now())),
		  count(*) FILTER (WHERE provider <> 'manual' AND status IN ('active','grace') AND (period_end IS NULL OR period_end > now())),
		  (SELECT count(*) FROM users WHERE plan = 'pro' AND deleted_at IS NULL)
		FROM subscriptions`).Scan(&s.Active, &s.Grace, &s.Expired, &s.Cancelled, &s.Manual, &s.Store, &s.ProUsers)
	return s, err
}

func (r *AdminRepo) UserRedemptions(ctx context.Context, userID int64) ([]model.AdminRedemption, error) {
	rows, err := r.db.Query(ctx, `SELECT c.code, c.kind, c.metric, c.amount, cr.created_at FROM coupon_redemptions cr
		JOIN coupons c ON c.id = cr.coupon_id WHERE cr.user_id = $1 ORDER BY cr.created_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AdminRedemption{}
	for rows.Next() {
		var x model.AdminRedemption
		if err := rows.Scan(&x.Code, &x.Kind, &x.Metric, &x.Amount, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Audit: entri log. userID > 0 = aktivitas user itu (sebagai pelaku atau target);
// adminOnly = hanya aksi admin.
func (r *AdminRepo) Audit(ctx context.Context, userID int64, adminOnly bool, limit int) ([]model.AdminAuditEntry, error) {
	rows, err := r.db.Query(ctx, `SELECT a.id, a.user_id, coalesce(u.email, ''), a.action, a.entity, a.entity_id, a.metadata, a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id = a.user_id
		WHERE ($1 = 0 OR a.user_id = $1 OR (a.entity = 'user' AND a.entity_id = $1))
		  AND (NOT $2 OR a.action LIKE 'admin.%')
		ORDER BY a.id DESC LIMIT $3`, userID, adminOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AdminAuditEntry{}
	for rows.Next() {
		var e model.AdminAuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.Action, &e.Entity, &e.EntityID, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- kupon ----

const couponCols = `id, code, description, kind, metric, amount, max_redemptions, redeemed, starts_at, expires_at, active, created_at`

func scanCoupon(row interface{ Scan(...any) error }) (model.Coupon, error) {
	var c model.Coupon
	err := row.Scan(&c.ID, &c.Code, &c.Description, &c.Kind, &c.Metric, &c.Amount, &c.MaxRedemptions, &c.Redeemed, &c.StartsAt, &c.ExpiresAt, &c.Active, &c.CreatedAt)
	return c, mapErr(err)
}

func (r *AdminRepo) Coupons(ctx context.Context) ([]model.Coupon, error) {
	rows, err := r.db.Query(ctx, `SELECT `+couponCols+` FROM coupons ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Coupon{}
	for rows.Next() {
		c, err := scanCoupon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *AdminRepo) CreateCoupon(ctx context.Context, c model.Coupon, by int64) (model.Coupon, error) {
	return scanCoupon(r.db.QueryRow(ctx, `INSERT INTO coupons (code, description, kind, metric, amount, max_redemptions, starts_at, expires_at, active, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+couponCols,
		c.Code, c.Description, c.Kind, c.Metric, c.Amount, c.MaxRedemptions, c.StartsAt, c.ExpiresAt, c.Active, by))
}

func (r *AdminRepo) SetCouponActive(ctx context.Context, id int64, active bool) (model.Coupon, error) {
	return scanCoupon(r.db.QueryRow(ctx, `UPDATE coupons SET active = $2 WHERE id = $1 RETURNING `+couponCols, id, active))
}

// DeleteCoupon hanya untuk kupon yang belum pernah dipakai; sisanya dinonaktifkan saja.
func (r *AdminRepo) DeleteCoupon(ctx context.Context, id int64) error {
	t, err := r.db.Exec(ctx, `DELETE FROM coupons WHERE id = $1 AND redeemed = 0`, id)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		var exists bool
		_ = r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM coupons WHERE id = $1)`, id).Scan(&exists)
		if exists {
			return model.ErrConflict
		}
		return model.ErrNotFound
	}
	return nil
}

// CouponForRedeem mengunci kupon (dipanggil dalam transaksi).
func (r *AdminRepo) CouponForRedeem(ctx context.Context, code string) (model.Coupon, error) {
	return scanCoupon(r.db.QueryRow(ctx, `SELECT `+couponCols+` FROM coupons WHERE code = $1 FOR UPDATE`, code))
}

// Redeem mencatat pemakaian; ErrConflict bila user sudah memakai kupon ini.
func (r *AdminRepo) Redeem(ctx context.Context, couponID, userID int64) error {
	if _, err := r.db.Exec(ctx, `INSERT INTO coupon_redemptions (coupon_id, user_id) VALUES ($1, $2)`, couponID, userID); err != nil {
		return mapErr(err)
	}
	_, err := r.db.Exec(ctx, `UPDATE coupons SET redeemed = redeemed + 1 WHERE id = $1`, couponID)
	return err
}

// ---- laporan ----

// Report: agregat per hari (zona Asia/Jakarta) untuk `days` hari terakhir.
func (r *AdminRepo) Report(ctx context.Context, days int) (model.AdminReport, error) {
	rep := model.AdminReport{Days: days}
	totals := func(offsetDays int, t *model.AdminReportTotals) error {
		return r.db.QueryRow(ctx, `WITH w AS (SELECT now() - ($1::int + $2::int) * interval '1 day' AS a, now() - $2::int * interval '1 day' AS b)
			SELECT
			  (SELECT count(*) FROM users, w WHERE deleted_at IS NULL AND created_at >= w.a AND created_at < w.b),
			  (SELECT count(DISTINCT uid) FROM (
			     SELECT user_id uid FROM items, w WHERE created_at >= w.a AND created_at < w.b
			     UNION SELECT user_id FROM messages, w WHERE role = 'user' AND created_at >= w.a AND created_at < w.b) x),
			  (SELECT count(*) FROM items, w WHERE created_at >= w.a AND created_at < w.b),
			  (SELECT count(*) FROM messages, w WHERE role = 'user' AND created_at >= w.a AND created_at < w.b),
			  (SELECT count(*) FROM coupon_redemptions, w WHERE created_at >= w.a AND created_at < w.b),
			  (SELECT count(*) FROM reminders, w WHERE created_at >= w.a AND created_at < w.b)`, days, offsetDays).
			Scan(&t.NewUsers, &t.ActiveUsers, &t.Items, &t.Questions, &t.Redemptions, &t.Reminders)
	}
	if err := totals(0, &rep.Totals); err != nil {
		return rep, err
	}
	if err := totals(days, &rep.Previous); err != nil {
		return rep, err
	}

	rows, err := r.db.Query(ctx, `WITH d AS (
		  SELECT generate_series(((now() AT TIME ZONE 'Asia/Jakarta')::date - ($1::int - 1)), (now() AT TIME ZONE 'Asia/Jakarta')::date, interval '1 day')::date AS dy),
		u AS (SELECT (created_at AT TIME ZONE 'Asia/Jakarta')::date AS dy, count(*) n FROM users WHERE deleted_at IS NULL AND created_at >= now() - ($1::int + 1) * interval '1 day' GROUP BY 1),
		i AS (SELECT (created_at AT TIME ZONE 'Asia/Jakarta')::date AS dy, count(*) n FROM items WHERE created_at >= now() - ($1::int + 1) * interval '1 day' GROUP BY 1),
		q AS (SELECT (created_at AT TIME ZONE 'Asia/Jakarta')::date AS dy, count(*) n FROM messages WHERE role = 'user' AND created_at >= now() - ($1::int + 1) * interval '1 day' GROUP BY 1),
		a AS (SELECT dy, count(DISTINCT uid) n FROM (
		        SELECT (created_at AT TIME ZONE 'Asia/Jakarta')::date AS dy, user_id uid FROM items WHERE created_at >= now() - ($1::int + 1) * interval '1 day'
		        UNION ALL SELECT (created_at AT TIME ZONE 'Asia/Jakarta')::date, user_id FROM messages WHERE role = 'user' AND created_at >= now() - ($1::int + 1) * interval '1 day') x GROUP BY 1)
		SELECT d.dy, coalesce(u.n, 0), coalesce(a.n, 0), coalesce(i.n, 0), coalesce(q.n, 0)
		FROM d LEFT JOIN u USING (dy) LEFT JOIN a USING (dy) LEFT JOIN i USING (dy) LEFT JOIN q USING (dy) ORDER BY d.dy`, days)
	if err != nil {
		return rep, err
	}
	defer rows.Close()
	rep.Daily = []model.AdminReportDay{}
	for rows.Next() {
		var p model.AdminReportDay
		var d time.Time
		if err := rows.Scan(&d, &p.NewUsers, &p.ActiveUsers, &p.Items, &p.Questions); err != nil {
			return rep, err
		}
		p.Date = d.Format("2006-01-02")
		rep.Daily = append(rep.Daily, p)
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}

	counts := func(q string, args ...any) ([]model.AdminCount, error) {
		rows, err := r.db.Query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []model.AdminCount{}
		for rows.Next() {
			var c model.AdminCount
			if err := rows.Scan(&c.Key, &c.Count); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	if rep.ItemsByKind, err = counts(`SELECT kind, count(*) FROM items WHERE created_at >= now() - $1::int * interval '1 day' GROUP BY 1 ORDER BY 2 DESC`, days); err != nil {
		return rep, err
	}
	if rep.ItemsByStatus, err = counts(`SELECT status, count(*) FROM items WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return rep, err
	}
	if rep.Plans, err = counts(`SELECT k, n FROM (
		  SELECT 'free' k, count(*) FILTER (WHERE plan = 'free') n FROM users WHERE deleted_at IS NULL
		  UNION ALL SELECT 'pro', count(*) FILTER (WHERE plan = 'pro') FROM users WHERE deleted_at IS NULL
		  UNION ALL SELECT 'admin', count(*) FILTER (WHERE role = 'admin') FROM users WHERE deleted_at IS NULL
		  UNION ALL SELECT 'suspended', count(*) FILTER (WHERE suspended_at IS NOT NULL) FROM users WHERE deleted_at IS NULL) x`); err != nil {
		return rep, err
	}
	if rep.Subscriptions, err = counts(`SELECT status, count(*) FROM subscriptions GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return rep, err
	}

	rows3, err := r.db.Query(ctx, `SELECT id, email, name, plan, it, qs FROM (
		  SELECT u.id, u.email, u.name, u.plan,
		    (SELECT count(*) FROM items i WHERE i.user_id = u.id AND i.created_at >= now() - $1::int * interval '1 day') it,
		    (SELECT count(*) FROM messages m WHERE m.user_id = u.id AND m.role = 'user' AND m.created_at >= now() - $1::int * interval '1 day') qs
		  FROM users u WHERE u.deleted_at IS NULL) x
		WHERE it + qs > 0 ORDER BY it + qs DESC, id LIMIT 10`, days)
	if err != nil {
		return rep, err
	}
	defer rows3.Close()
	rep.TopUsers = []model.AdminTopUser{}
	for rows3.Next() {
		var t model.AdminTopUser
		if err := rows3.Scan(&t.ID, &t.Email, &t.Name, &t.Plan, &t.Items, &t.Questions); err != nil {
			return rep, err
		}
		rep.TopUsers = append(rep.TopUsers, t)
	}
	return rep, rows3.Err()
}

func (r *AdminRepo) PlanUserCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `SELECT plan, count(*) FROM users WHERE deleted_at IS NULL GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

func (r *AdminRepo) UpsertPlanLimits(ctx context.Context, plan string, ls []model.PlanLimit) error {
	for _, l := range ls {
		if _, err := r.db.Exec(ctx, `INSERT INTO plan_limits (plan, metric, period, "limit") VALUES ($1,$2,$3,$4)
			ON CONFLICT (plan, metric) DO UPDATE SET period = EXCLUDED.period, "limit" = EXCLUDED."limit"`, plan, l.Metric, l.Period, l.Limit); err != nil {
			return err
		}
	}
	return nil
}
