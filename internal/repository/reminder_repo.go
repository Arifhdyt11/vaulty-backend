package repository

import (
	"context"
	"time"

	"vaulty/internal/model"
)

type ReminderRepo struct{ db DB }

const reminderCols = `id, user_id, item_id, text, origin, remind_at, repeat, lead_minutes, notify_at, notify_kind, status, created_at`

func scanReminder(row interface{ Scan(...any) error }) (model.Reminder, error) {
	var m model.Reminder
	err := row.Scan(&m.ID, &m.UserID, &m.ItemID, &m.Text, &m.Origin, &m.RemindAt, &m.Repeat, &m.LeadMinutes, &m.NotifyAt, &m.NotifyKind, &m.Status, &m.CreatedAt)
	if m.LeadMinutes == nil {
		m.LeadMinutes = []int32{}
	}
	// 'sending' adalah klaim internal worker; di API tampil sebagai pending.
	if m.Status == "sending" {
		m.Status = "pending"
	}
	return m, mapErr(err)
}

type NewReminder struct {
	UserID      int64
	ItemID      *int64
	Text        string
	Origin      string
	RemindAt    time.Time
	Repeat      string
	LeadMinutes []int32
	NotifyAt    time.Time
	NotifyKind  string
	Status      string
}

func (r *ReminderRepo) Create(ctx context.Context, n NewReminder) (model.Reminder, error) {
	if n.LeadMinutes == nil {
		n.LeadMinutes = []int32{}
	}
	return scanReminder(r.db.QueryRow(ctx, `INSERT INTO reminders (user_id, item_id, text, origin, remind_at, repeat, lead_minutes, notify_at, notify_kind, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+reminderCols,
		n.UserID, n.ItemID, n.Text, n.Origin, n.RemindAt, n.Repeat, n.LeadMinutes, n.NotifyAt, n.NotifyKind, n.Status))
}

func (r *ReminderRepo) ByID(ctx context.Context, userID, id int64) (model.Reminder, error) {
	return scanReminder(r.db.QueryRow(ctx, `SELECT `+reminderCols+` FROM reminders WHERE id = $1 AND user_id = $2`, id, userID))
}

func (r *ReminderRepo) List(ctx context.Context, userID int64, status string, itemID *int64, limit int) ([]model.Reminder, error) {
	var cond string
	switch status {
	case "upcoming":
		cond = "status IN ('pending','sending','sent')"
	case "suggested":
		cond = "status = 'suggested'"
	case "done":
		cond = "status IN ('done','cancelled','failed')"
	default:
		cond = "TRUE"
	}
	order := "notify_at ASC"
	if status == "done" {
		order = "updated_at DESC"
	}
	rows, err := r.db.Query(ctx, `SELECT `+reminderCols+` FROM reminders WHERE user_id = $1 AND `+cond+` AND ($2::bigint IS NULL OR item_id = $2) ORDER BY `+order+` LIMIT $3`, userID, itemID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Reminder
	for rows.Next() {
		m, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type ReminderPatch struct {
	Text        *string
	RemindAt    *time.Time
	Repeat      *string
	LeadMinutes []int32
	NotifyAt    *time.Time
	NotifyKind  *string
	Status      *string
}

func (r *ReminderRepo) Update(ctx context.Context, userID, id int64, p ReminderPatch) (model.Reminder, error) {
	return scanReminder(r.db.QueryRow(ctx, `UPDATE reminders SET text = COALESCE($3, text), remind_at = COALESCE($4, remind_at),
		repeat = COALESCE($5, repeat), lead_minutes = COALESCE($6, lead_minutes), notify_at = COALESCE($7, notify_at),
		notify_kind = COALESCE($8, notify_kind), status = COALESCE($9, status), updated_at = now()
		WHERE id = $1 AND user_id = $2 RETURNING `+reminderCols, id, userID, p.Text, p.RemindAt, p.Repeat, p.LeadMinutes, p.NotifyAt, p.NotifyKind, p.Status))
}

// ClaimDue mengambil reminder jatuh tempo dengan FOR UPDATE SKIP LOCKED supaya banyak worker aman.
func (r *ReminderRepo) ClaimDue(ctx context.Context, limit int) ([]model.Reminder, error) {
	rows, err := r.db.Query(ctx, `UPDATE reminders SET status = 'sending', claimed_at = now(), attempts = attempts + 1, updated_at = now()
		WHERE id IN (SELECT id FROM reminders WHERE status = 'pending' AND notify_at <= now() ORDER BY notify_at FOR UPDATE SKIP LOCKED LIMIT $1)
		RETURNING `+reminderCols, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Reminder
	for rows.Next() {
		var m model.Reminder
		if err := rows.Scan(&m.ID, &m.UserID, &m.ItemID, &m.Text, &m.Origin, &m.RemindAt, &m.Repeat, &m.LeadMinutes, &m.NotifyAt, &m.NotifyKind, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ReminderRepo) FinishSend(ctx context.Context, id int64, status string, remindAt, notifyAt time.Time, kind string) error {
	_, err := r.db.Exec(ctx, `UPDATE reminders SET status = $2, remind_at = $3, notify_at = $4, notify_kind = $5, last_sent_at = now(), updated_at = now() WHERE id = $1`,
		id, status, remindAt, notifyAt, kind)
	return err
}

func (r *ReminderRepo) FailSend(ctx context.Context, id int64, maxAttempts int) error {
	_, err := r.db.Exec(ctx, `UPDATE reminders SET status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
		notify_at = CASE WHEN attempts >= $2 THEN notify_at ELSE now() + interval '1 minute' END, updated_at = now() WHERE id = $1`, id, maxAttempts)
	return err
}

// ReleaseStale mengembalikan klaim 'sending' yang macet (worker mati) ke pending.
func (r *ReminderRepo) ReleaseStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	t, err := r.db.Exec(ctx, `UPDATE reminders SET status = 'pending', updated_at = now() WHERE status = 'sending' AND claimed_at < now() - $1::interval`, olderThan.String())
	return t.RowsAffected(), err
}
