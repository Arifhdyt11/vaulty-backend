package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository/queries"
)

type ReminderRepository struct{ q *queries.Queries }

func NewReminderRepository(q *queries.Queries) *ReminderRepository { return &ReminderRepository{q: q} }

type NewReminder struct {
	UserID      int64
	NoteID      *int64
	Text        string
	RemindAt    time.Time
	Repeat      string
	LeadMinutes []int
	NotifyAt    time.Time
	NotifyKind  string
}

func (r *ReminderRepository) Create(ctx context.Context, n NewReminder) (model.Reminder, error) {
	var noteID pgtype.Int8
	if n.NoteID != nil {
		noteID = pgtype.Int8{Int64: *n.NoteID, Valid: true}
	}
	row, err := r.q.CreateReminder(ctx, queries.CreateReminderParams{
		UserID:      n.UserID,
		NoteID:      noteID,
		Text:        n.Text,
		RemindAt:    ts(n.RemindAt),
		Repeat:      n.Repeat,
		LeadMinutes: int32s(n.LeadMinutes),
		NotifyAt:    ts(n.NotifyAt),
		NotifyKind:  n.NotifyKind,
	})
	return toReminder(row), err
}

func (r *ReminderRepository) FindByID(ctx context.Context, userID, id int64) (model.Reminder, error) {
	row, err := r.q.GetReminder(ctx, queries.GetReminderParams{ID: id, UserID: userID})
	return toReminder(row), notFound(err)
}

// ListUpcoming: reminder aktif user, notifikasi terdekat dulu.
func (r *ReminderRepository) ListUpcoming(ctx context.Context, userID int64, limit int) ([]model.Reminder, error) {
	rows, err := r.q.ListUpcomingReminders(ctx, queries.ListUpcomingRemindersParams{UserID: userID, RowLimit: int32(limit)})
	return toReminders(rows), err
}

// ClaimDue (worker, lintas user): tandai reminder jatuh tempo sebagai "sending" lalu kembalikan.
func (r *ReminderRepository) ClaimDue(ctx context.Context, limit int) ([]model.Reminder, error) {
	rows, err := r.q.ClaimDueReminders(ctx, int32(limit))
	return toReminders(rows), err
}

// FinishSend (worker) menyimpan jadwal berikutnya setelah notifikasi terkirim.
func (r *ReminderRepository) FinishSend(ctx context.Context, id int64, status string, remindAt, notifyAt time.Time, kind string) error {
	return r.q.FinishReminderSend(ctx, queries.FinishReminderSendParams{
		ID: id, Status: status, RemindAt: ts(remindAt), NotifyAt: ts(notifyAt), NotifyKind: kind,
	})
}

// FailSend (worker): kirim ulang 1 menit lagi, atau failed setelah maxAttempts.
func (r *ReminderRepository) FailSend(ctx context.Context, id int64, maxAttempts int) error {
	return r.q.FailReminderSend(ctx, queries.FailReminderSendParams{ID: id, MaxAttempts: int32(maxAttempts)})
}

func (r *ReminderRepository) Reschedule(ctx context.Context, userID, id int64, notifyAt time.Time, kind string) (model.Reminder, error) {
	row, err := r.q.RescheduleReminder(ctx, queries.RescheduleReminderParams{
		ID: id, UserID: userID, NotifyAt: ts(notifyAt), NotifyKind: kind,
	})
	return toReminder(row), notFound(err)
}

// SetStatus mengubah status hanya jika status sekarang termasuk from; selain itu ErrNotFound.
func (r *ReminderRepository) SetStatus(ctx context.Context, userID, id int64, status string, from ...string) (model.Reminder, error) {
	row, err := r.q.SetReminderStatus(ctx, queries.SetReminderStatusParams{
		ID: id, UserID: userID, Status: status, FromStatus: from,
	})
	return toReminder(row), notFound(err)
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func int32s(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func toReminders(rows []queries.Reminder) []model.Reminder {
	out := make([]model.Reminder, 0, len(rows))
	for _, row := range rows {
		out = append(out, toReminder(row))
	}
	return out
}

func toReminder(r queries.Reminder) model.Reminder {
	m := model.Reminder{
		ID: r.ID, UserID: r.UserID, Text: r.Text,
		RemindAt: r.RemindAt.Time, Repeat: r.Repeat, NotifyAt: r.NotifyAt.Time,
		NotifyKind: r.NotifyKind, Status: r.Status, CreatedAt: r.CreatedAt.Time,
		LeadMinutes: make([]int, len(r.LeadMinutes)),
	}
	for i, v := range r.LeadMinutes {
		m.LeadMinutes[i] = int(v)
	}
	if r.NoteID.Valid {
		id := r.NoteID.Int64
		m.NoteID = &id
	}
	return m
}
