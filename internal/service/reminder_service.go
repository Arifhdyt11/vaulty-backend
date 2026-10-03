package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
)

// ReminderRepo dipenuhi *repository.ReminderRepository.
type ReminderRepo interface {
	Create(ctx context.Context, n repository.NewReminder) (model.Reminder, error)
	FindByID(ctx context.Context, userID, id int64) (model.Reminder, error)
	ListUpcoming(ctx context.Context, userID int64, limit int) ([]model.Reminder, error)
	ClaimDue(ctx context.Context, limit int) ([]model.Reminder, error)
	FinishSend(ctx context.Context, id int64, status string, remindAt, notifyAt time.Time, kind string) error
	FailSend(ctx context.Context, id int64, maxAttempts int) error
	Reschedule(ctx context.Context, userID, id int64, notifyAt time.Time, kind string) (model.Reminder, error)
	SetStatus(ctx context.Context, userID, id int64, status string, from ...string) (model.Reminder, error)
}

// NoteOwner memastikan note milik user (dipenuhi *repository.NoteRepository).
type NoteOwner interface {
	FindByID(ctx context.Context, userID, id int64) (model.Note, error)
}

// ReminderNotifier mengirim satu notifikasi reminder ke user (Telegram; Web Push menyusul).
type ReminderNotifier interface {
	NotifyReminder(ctx context.Context, r model.Reminder) error
}

// ReminderService: reminder sekali, berulang, dan bertahap (ADR-023).
// Tabel reminders adalah sumber kebenaran; worker memindai notify_at secara periodik.
type ReminderService struct {
	repo  ReminderRepo
	notes NoteOwner
	now   func() time.Time
}

func NewReminderService(repo ReminderRepo, notes NoteOwner) *ReminderService {
	return &ReminderService{repo: repo, notes: notes, now: time.Now}
}

const (
	maxReminderText = 500
	maxLeads        = 5
	maxLeadMinutes  = 365 * 24 * 60
	maxReminderAge  = 10 * 365 * 24 * time.Hour
	deliverBatch    = 20
	maxSendAttempts = 5
)

// Validasi pola ulang yang diterima.
var repeats = []string{model.RepeatNone, model.RepeatDaily, model.RepeatWeekly, model.RepeatMonthly, model.RepeatYearly}

func (s *ReminderService) Create(ctx context.Context, userID int64, in model.CreateReminderInput) (model.Reminder, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" || utf8.RuneCountInString(text) > maxReminderText {
		return model.Reminder{}, model.ErrEmptyReminder
	}
	if !slices.Contains(repeats, in.Repeat) {
		return model.Reminder{}, model.ErrInvalidRepeat
	}
	now := s.now()
	if !in.RemindAt.After(now) {
		return model.Reminder{}, model.ErrReminderPast
	}
	if in.RemindAt.Sub(now) > maxReminderAge {
		return model.Reminder{}, model.ErrReminderTooFar
	}
	leads, err := normalizeLeads(in.LeadMinutes)
	if err != nil {
		return model.Reminder{}, err
	}
	if in.NoteID != nil {
		// ADR-007: note milik user lain diperlakukan sama dengan note yang tidak ada.
		if _, err := s.notes.FindByID(ctx, userID, *in.NoteID); err != nil {
			return model.Reminder{}, fmt.Errorf("note reminder: %w", err)
		}
	}
	notifyAt, kind, _ := NextNotify(in.RemindAt, leads, now) // RemindAt > now, jadi selalu ada
	return s.repo.Create(ctx, repository.NewReminder{
		UserID: userID, NoteID: in.NoteID, Text: text, RemindAt: in.RemindAt.UTC(),
		Repeat: in.Repeat, LeadMinutes: leads, NotifyAt: notifyAt.UTC(), NotifyKind: kind,
	})
}

func (s *ReminderService) Get(ctx context.Context, userID, id int64) (model.Reminder, error) {
	return s.repo.FindByID(ctx, userID, id)
}

func (s *ReminderService) ListUpcoming(ctx context.Context, userID int64, limit int) ([]model.Reminder, error) {
	return s.repo.ListUpcoming(ctx, userID, limit)
}

// Cancel menghentikan reminder (termasuk seluruh pengulangannya).
func (s *ReminderService) Cancel(ctx context.Context, userID, id int64) (model.Reminder, error) {
	return s.repo.SetStatus(ctx, userID, id, model.ReminderCancelled,
		model.ReminderPending, model.ReminderSending, model.ReminderSent, model.ReminderFailed)
}

// Done menandai reminder sekali sebagai selesai. Reminder berulang tetap berjalan ke jadwal berikutnya.
func (s *ReminderService) Done(ctx context.Context, userID, id int64) (model.Reminder, error) {
	r, err := s.repo.FindByID(ctx, userID, id)
	if err != nil || r.Repeat != model.RepeatNone {
		return r, err
	}
	return s.repo.SetStatus(ctx, userID, id, model.ReminderDone, model.ReminderPending, model.ReminderSent, model.ReminderFailed)
}

// Snooze menunda notifikasi selama d dari sekarang.
func (s *ReminderService) Snooze(ctx context.Context, userID, id int64, d time.Duration) (model.Reminder, error) {
	if d < time.Minute || d > 7*24*time.Hour {
		return model.Reminder{}, fmt.Errorf("tunda harus 1 menit sampai 7 hari: %w", model.ErrReminderInvalid)
	}
	r, err := s.repo.Reschedule(ctx, userID, id, s.now().Add(d).UTC(), model.NotifySnooze)
	if errors.Is(err, model.ErrNotFound) {
		// Reminder ada tapi status tidak bisa ditunda (selesai/dibatalkan) dibedakan dari tidak ada.
		if _, gerr := s.repo.FindByID(ctx, userID, id); gerr == nil {
			return model.Reminder{}, model.ErrReminderInvalid
		}
	}
	return r, err
}

// DeliverDue (worker) mengirim semua reminder yang jatuh tempo, lalu menjadwalkan notifikasi
// berikutnya. Pengiriman at-least-once: jika worker mati setelah kirim tapi sebelum update,
// notifikasi bisa terkirim dua kali (lebih baik daripada tidak terkirim).
func (s *ReminderService) DeliverDue(ctx context.Context, n ReminderNotifier) (int, error) {
	sent := 0
	for {
		due, err := s.repo.ClaimDue(ctx, deliverBatch)
		if err != nil {
			return sent, fmt.Errorf("ambil reminder jatuh tempo: %w", err)
		}
		for _, r := range due {
			if err := n.NotifyReminder(ctx, r); err != nil {
				slog.WarnContext(ctx, "kirim reminder gagal", "reminder_id", r.ID, "err", err)
				if ferr := s.repo.FailSend(ctx, r.ID, maxSendAttempts); ferr != nil {
					slog.ErrorContext(ctx, "tandai reminder gagal", "reminder_id", r.ID, "err", ferr)
				}
				continue
			}
			sent++
			status, remindAt, notifyAt, kind := afterSend(r, s.now())
			if err := s.repo.FinishSend(ctx, r.ID, status, remindAt, notifyAt, kind); err != nil {
				slog.ErrorContext(ctx, "jadwalkan reminder berikutnya gagal", "reminder_id", r.ID, "err", err)
			}
		}
		if len(due) < deliverBatch {
			return sent, nil
		}
	}
}

// afterSend menentukan jadwal setelah notifikasi r terkirim pada waktu now.
func afterSend(r model.Reminder, now time.Time) (status string, remindAt, notifyAt time.Time, kind string) {
	event := r.RemindAt
	if r.NotifyKind == model.NotifyEvent {
		if r.Repeat == model.RepeatNone {
			return model.ReminderSent, event, event, model.NotifyEvent
		}
		for !event.After(now) {
			event = NextOccurrence(event, r.Repeat)
		}
	}
	at, k, ok := NextNotify(event, r.LeadMinutes, now)
	if !ok { // acara sekali sudah lewat (mis. tunda setelah acara): selesai
		return model.ReminderSent, event, event, model.NotifyEvent
	}
	return model.ReminderPending, event, at, k
}

// NextNotify mengembalikan notifikasi berikutnya setelah now untuk acara event: pengingat
// bertahap paling awal yang belum lewat, atau acara itu sendiri. ok=false jika acara sudah lewat.
func NextNotify(event time.Time, leadMinutes []int, now time.Time) (time.Time, string, bool) {
	best, kind := time.Time{}, ""
	for _, m := range leadMinutes {
		t := event.Add(-time.Duration(m) * time.Minute)
		if t.After(now) && (best.IsZero() || t.Before(best)) {
			best, kind = t, model.NotifyLead
		}
	}
	if !best.IsZero() {
		return best, kind, true
	}
	if event.After(now) {
		return event, model.NotifyEvent, true
	}
	return time.Time{}, "", false
}

// NextOccurrence: kejadian berikutnya dari t. Bulanan/tahunan memakai tanggal terakhir bulan
// jika tanggalnya tidak ada (31 Jan → 28/29 Feb), supaya tidak meloncat ke bulan berikutnya.
func NextOccurrence(t time.Time, repeat string) time.Time {
	switch repeat {
	case model.RepeatDaily:
		return t.AddDate(0, 0, 1)
	case model.RepeatWeekly:
		return t.AddDate(0, 0, 7)
	case model.RepeatMonthly:
		return addMonthsClamped(t, 1)
	case model.RepeatYearly:
		return addMonthsClamped(t, 12)
	}
	return t
}

func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(d, last)-1)
}

// normalizeLeads: unik, positif, maksimal 365 hari, urut dari yang paling jauh.
func normalizeLeads(in []int) ([]int, error) {
	out := []int{}
	for _, m := range in {
		if m < 1 || m > maxLeadMinutes {
			return nil, model.ErrInvalidLead
		}
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	if len(out) > maxLeads {
		return nil, model.ErrInvalidLead
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out, nil
}
