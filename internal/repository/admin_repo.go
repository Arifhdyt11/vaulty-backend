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
