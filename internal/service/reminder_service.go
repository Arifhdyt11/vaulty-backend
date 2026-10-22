package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/ai"
	"vaulty/pkg/push"
)

type ReminderService struct {
	repos *repository.Repos
	chat  ai.Chat
	model string
	aiOn  bool
	items *ItemService // diisi belakangan (circular): untuk kartu item di payload
}

func NewReminderService(repos *repository.Repos, chat ai.Chat, modelName string, aiOn bool) *ReminderService {
	return &ReminderService{repos: repos, chat: chat, model: modelName, aiOn: aiOn}
}

func (s *ReminderService) SetItems(items *ItemService) { s.items = items }

type ReminderInput struct {
	ItemID      *int64
	Text        string
	RemindAt    time.Time
	Repeat      string
	LeadMinutes []int32
	Origin      string // user|item|ai
	Suggested   bool
}

type ParsedReminder struct {
	RemindAt    time.Time `json:"remind_at"`
	Repeat      string    `json:"repeat"`
	LeadMinutes []int32   `json:"lead_minutes"`
	Confidence  float64   `json:"confidence"`
	Text        string    `json:"text"`
}

// CreateFromText: kalimat natural → LLM → reminder (ADR-010).
func (s *ReminderService) CreateFromText(ctx context.Context, u model.User, text string, itemID *int64) (model.Reminder, *ParsedReminder, error) {
	if !s.aiOn {
		return model.Reminder{}, nil, fmt.Errorf("%w: kirim remind_at terstruktur", model.ErrAIDisabled)
	}
	now := time.Now().In(u.Location())
	resp, err := s.chat.Complete(ctx, ai.ChatRequest{Model: s.model, System: reminderParsePrompt, JSON: true, MaxTokens: 300,
		Messages: []ai.Message{{Role: "user", Content: fmt.Sprintf("WAKTU SEKARANG: %s (%s, %s)\nKALIMAT: %s", now.Format(time.RFC3339), u.Timezone, now.Weekday(), ai.Truncate(text, 500))}}})
	if err != nil {
		return model.Reminder{}, nil, fmt.Errorf("parse reminder: %w", err)
	}
	var out struct {
		Text        string  `json:"text"`
		RemindAt    string  `json:"remind_at"`
		Repeat      string  `json:"repeat"`
		LeadMinutes []int32 `json:"lead_minutes"`
		Confidence  float64 `json:"confidence"`
	}
	if err := ai.ParseJSONLoose(resp.Content, &out); err != nil || out.RemindAt == "" {
		return model.Reminder{}, nil, model.Invalid("waktu pengingat tidak bisa dipahami, coba sebutkan tanggal/jam")
	}
	at, err := time.Parse(time.RFC3339, out.RemindAt)
	if err != nil {
		return model.Reminder{}, nil, model.Invalid("waktu pengingat tidak valid")
	}
	if strings.TrimSpace(out.Text) == "" {
		out.Text = text
	}
	p := &ParsedReminder{RemindAt: at, Repeat: out.Repeat, LeadMinutes: out.LeadMinutes, Confidence: out.Confidence, Text: out.Text}
	r, err := s.CreateStructured(ctx, u, ReminderInput{ItemID: itemID, Text: out.Text, RemindAt: at, Repeat: out.Repeat, LeadMinutes: out.LeadMinutes, Origin: "user"})
	return r, p, err
}

func (s *ReminderService) CreateStructured(ctx context.Context, u model.User, in ReminderInput) (model.Reminder, error) {
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || len(in.Text) > 500 {
		return model.Reminder{}, model.Invalid("teks pengingat 1-500 karakter")
	}
	if in.RemindAt.IsZero() {
		return model.Reminder{}, model.Invalid("remind_at wajib")
	}
	now := time.Now()
	if in.Repeat == "" && in.RemindAt.Before(now.Add(-time.Minute)) {
		return model.Reminder{}, model.Invalid("waktu pengingat sudah lewat")
	}
	switch in.Repeat {
	case "", "daily", "weekly", "monthly", "yearly":
	default:
		return model.Reminder{}, model.Invalid("repeat tidak valid")
	}
	leads, err := normalizeLeads(in.LeadMinutes)
	if err != nil {
		return model.Reminder{}, err
	}
	if in.ItemID != nil {
		if _, err := s.repos.Items.ByID(ctx, u.ID, *in.ItemID); err != nil {
			return model.Reminder{}, fmt.Errorf("%w: item", model.ErrNotFound)
		}
	}
	if in.Origin == "" {
		in.Origin = "user"
	}
	remindAt := in.RemindAt
	for in.Repeat != "" && remindAt.Before(now) {
		remindAt = NextOccurrence(remindAt, in.Repeat)
	}
	notifyAt, kind, _ := NextNotify(remindAt, leads, now)
	status := "pending"
	if in.Suggested {
		status = "suggested"
	}
	r, err := s.repos.Reminder.Create(ctx, repository.NewReminder{UserID: u.ID, ItemID: in.ItemID, Text: in.Text, Origin: in.Origin, RemindAt: remindAt,
		Repeat: in.Repeat, LeadMinutes: leads, NotifyAt: notifyAt, NotifyKind: kind, Status: status})
	if err != nil {
		return r, err
	}
	return s.decorate(ctx, r), nil
}

func (s *ReminderService) Get(ctx context.Context, u model.User, id int64) (model.Reminder, error) {
	r, err := s.repos.Reminder.ByID(ctx, u.ID, id)
	if err != nil {
		return r, err
	}
	return s.decorate(ctx, r), nil
}

func (s *ReminderService) List(ctx context.Context, u model.User, status string, itemID *int64) ([]model.Reminder, error) {
	rs, err := s.repos.Reminder.List(ctx, u.ID, status, itemID, 200)
	if err != nil {
		return nil, err
	}
	out := make([]model.Reminder, 0, len(rs))
	for _, r := range rs {
		out = append(out, s.decorate(ctx, r))
	}
	return out, nil
}

type ReminderPatchInput struct {
	Text        *string
	RemindAt    *time.Time
	Repeat      *string
	LeadMinutes []int32
}

func (s *ReminderService) Update(ctx context.Context, u model.User, id int64, in ReminderPatchInput) (model.Reminder, error) {
	cur, err := s.repos.Reminder.ByID(ctx, u.ID, id)
	if err != nil {
		return cur, err
	}
	p := repository.ReminderPatch{Text: in.Text, RemindAt: in.RemindAt, Repeat: in.Repeat}
	remindAt, repeat, leads := cur.RemindAt, cur.Repeat, cur.LeadMinutes
	if in.RemindAt != nil {
		remindAt = *in.RemindAt
	}
	if in.Repeat != nil {
		repeat = *in.Repeat
	}
	if in.LeadMinutes != nil {
		if leads, err = normalizeLeads(in.LeadMinutes); err != nil {
			return cur, err
		}
		p.LeadMinutes = leads
	}
	if in.RemindAt != nil || in.Repeat != nil || in.LeadMinutes != nil {
		notifyAt, kind, _ := NextNotify(remindAt, leads, time.Now())
		p.NotifyAt, p.NotifyKind = &notifyAt, &kind
		if cur.Status == "sent" || cur.Status == "done" {
			st := "pending"
			p.Status = &st
		}
	}
	_ = repeat
	r, err := s.repos.Reminder.Update(ctx, u.ID, id, p)
	if err != nil {
		return r, err
	}
	return s.decorate(ctx, r), nil
}

func (s *ReminderService) Confirm(ctx context.Context, u model.User, id int64) (model.Reminder, error) {
	cur, err := s.repos.Reminder.ByID(ctx, u.ID, id)
	if err != nil {
		return cur, err
	}
	if cur.Status != "suggested" {
		return cur, model.Invalid("reminder bukan usulan")
	}
	notifyAt, kind, _ := NextNotify(cur.RemindAt, cur.LeadMinutes, time.Now())
	st := "pending"
	r, err := s.repos.Reminder.Update(ctx, u.ID, id, repository.ReminderPatch{Status: &st, NotifyAt: &notifyAt, NotifyKind: &kind})
	return s.decorate(ctx, r), err
}

func (s *ReminderService) Done(ctx context.Context, u model.User, id int64) (model.Reminder, error) {
	cur, err := s.repos.Reminder.ByID(ctx, u.ID, id)
	if err != nil {
		return cur, err
	}
	st := "done"
	p := repository.ReminderPatch{Status: &st}
	if cur.Repeat != "" {
		// Berulang: selesai untuk kali ini, jadwalkan berikutnya.
		next := NextOccurrence(cur.RemindAt, cur.Repeat)
		notifyAt, kind, _ := NextNotify(next, cur.LeadMinutes, time.Now())
		pending := "pending"
		p = repository.ReminderPatch{Status: &pending, RemindAt: &next, NotifyAt: &notifyAt, NotifyKind: &kind}
	}
	r, err := s.repos.Reminder.Update(ctx, u.ID, id, p)
	return s.decorate(ctx, r), err
}

func (s *ReminderService) Cancel(ctx context.Context, u model.User, id int64) (model.Reminder, error) {
	st := "cancelled"
	r, err := s.repos.Reminder.Update(ctx, u.ID, id, repository.ReminderPatch{Status: &st})
	return s.decorate(ctx, r), err
}

func (s *ReminderService) Snooze(ctx context.Context, u model.User, id int64, d time.Duration) (model.Reminder, error) {
	if d < time.Minute || d > 30*24*time.Hour {
		return model.Reminder{}, model.Invalid("durasi tunda 1 menit - 30 hari")
	}
	if _, err := s.repos.Reminder.ByID(ctx, u.ID, id); err != nil {
		return model.Reminder{}, err
	}
	at := time.Now().Add(d)
	st, kind := "pending", "snooze"
	r, err := s.repos.Reminder.Update(ctx, u.ID, id, repository.ReminderPatch{Status: &st, NotifyAt: &at, NotifyKind: &kind})
	return s.decorate(ctx, r), err
}

// SnoozeUntilTomorrowMorning: 09:00 besok di zona waktu user.
func (s *ReminderService) SnoozeUntilTomorrow(ctx context.Context, u model.User, id int64) (model.Reminder, error) {
	now := time.Now().In(u.Location())
	t := time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, u.Location())
	return s.Snooze(ctx, u, id, time.Until(t))
}

func (s *ReminderService) decorate(ctx context.Context, r model.Reminder) model.Reminder {
	if r.ItemID != nil && s.items != nil {
		if it, err := s.repos.Items.ByID(ctx, r.UserID, *r.ItemID); err == nil {
			c := s.items.Card(ctx, it)
			r.Item = &c
		}
	}
	return r
}

// ---------- pengiriman (worker) ----------

const maxSendAttempts = 5

// DeliverDue: klaim reminder jatuh tempo, kirim push ke semua device user, jadwalkan berikutnya.
func (s *ReminderService) DeliverDue(ctx context.Context, sender push.Sender) (int, error) {
	due, err := s.repos.Reminder.ClaimDue(ctx, 50)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, r := range due {
		if err := s.send(ctx, sender, r); err != nil {
			slog.WarnContext(ctx, "kirim reminder gagal", "reminder_id", r.ID, "err", err)
			_ = s.repos.Reminder.FailSend(ctx, r.ID, maxSendAttempts)
			continue
		}
		status, remindAt, notifyAt, kind := afterSend(r, time.Now())
		if err := s.repos.Reminder.FinishSend(ctx, r.ID, status, remindAt, notifyAt, kind); err != nil {
			slog.WarnContext(ctx, "finish send gagal", "reminder_id", r.ID, "err", err)
		}
		sent++
	}
	return sent, nil
}

func (s *ReminderService) send(ctx context.Context, sender push.Sender, r model.Reminder) error {
	devices, err := s.repos.Devices.ListByUser(ctx, r.UserID)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil // tidak ada perangkat: anggap terkirim, jangan retry selamanya
	}
	u, err := s.repos.Users.ByID(ctx, r.UserID)
	if err != nil {
		return err
	}
	title, body := notificationText(r, u)
	data := map[string]string{"type": "reminder", "reminder_id": fmt.Sprint(r.ID), "notify_kind": r.NotifyKind}
	var image string
	if r.ItemID != nil && s.items != nil {
		data["item_id"] = fmt.Sprint(*r.ItemID)
		if it, err := s.repos.Items.ByID(ctx, r.UserID, *r.ItemID); err == nil {
			c := s.items.Card(ctx, it)
			if c.ThumbnailURL != nil {
				image = *c.ThumbnailURL
				data["thumbnail_url"] = image
			}
			if c.Title != nil && *c.Title != "" {
				body = body + " · " + ai.Truncate(*c.Title, 60)
			}
		}
	}
	var lastErr error
	ok := 0
	for _, d := range devices {
		err := sender.Send(ctx, d.PushToken, push.Notification{Title: title, Body: body, ImageURL: image}, data)
		switch {
		case err == nil:
			ok++
		case errors.Is(err, push.ErrInvalidToken):
			_ = s.repos.Devices.DeleteByToken(ctx, d.PushToken)
		case errors.Is(err, push.ErrDisabled):
			return nil // push belum dikonfigurasi: jangan dianggap gagal
		default:
			lastErr = err
		}
	}
	if ok == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

func notificationText(r model.Reminder, u model.User) (string, string) {
	loc := u.Location()
	switch r.NotifyKind {
	case "lead":
		d := time.Until(r.RemindAt)
		var when string
		switch {
		case d >= 48*time.Hour:
			when = fmt.Sprintf("%d hari lagi", int(d.Hours()/24+0.5))
		case d >= 2*time.Hour:
			when = fmt.Sprintf("%d jam lagi", int(d.Hours()+0.5))
		default:
			when = fmt.Sprintf("%d menit lagi", int(d.Minutes()+0.5))
		}
		return "⏳ " + when, r.Text + " (" + r.RemindAt.In(loc).Format("02 Jan 15:04") + ")"
	case "snooze":
		return "⏰ Pengingat (ditunda)", r.Text
	}
	return "🔔 Pengingat", r.Text
}

// afterSend menentukan status & notifikasi berikutnya setelah satu pengiriman berhasil.
func afterSend(r model.Reminder, now time.Time) (status string, remindAt, notifyAt time.Time, kind string) {
	remindAt = r.RemindAt
	if r.NotifyKind == "lead" || r.NotifyKind == "snooze" && now.Before(r.RemindAt) {
		// Masih ada notifikasi berikutnya sebelum/pada acara.
		if next, k, ok := NextNotify(r.RemindAt, r.LeadMinutes, now); ok {
			return "pending", remindAt, next, k
		}
	}
	// Notifikasi acara terkirim.
	if r.Repeat != "" {
		next := NextOccurrence(r.RemindAt, r.Repeat)
		for !next.After(now) {
			next = NextOccurrence(next, r.Repeat)
		}
		n, k, _ := NextNotify(next, r.LeadMinutes, now)
		return "pending", next, n, k
	}
	return "sent", remindAt, r.RemindAt, "event"
}

// NextNotify: waktu notifikasi berikutnya setelah now: lead terdekat yang masih di depan, atau acara itu sendiri.
func NextNotify(event time.Time, leads []int32, now time.Time) (time.Time, string, bool) {
	sorted := append([]int32(nil), leads...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] }) // lead terbesar (paling awal) dulu
	for _, m := range sorted {
		t := event.Add(-time.Duration(m) * time.Minute)
		if t.After(now) {
			return t, "lead", true
		}
	}
	if event.After(now) {
		return event, "event", true
	}
	return event, "event", false
}

func NextOccurrence(t time.Time, repeat string) time.Time {
	switch repeat {
	case "daily":
		return t.AddDate(0, 0, 1)
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return addMonthsClamped(t, 1)
	case "yearly":
		return addMonthsClamped(t, 12)
	}
	return t
}

// addMonthsClamped: 31 Jan + 1 bulan = 28/29 Feb, bukan 2/3 Mar.
func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func normalizeLeads(in []int32) ([]int32, error) {
	seen := map[int32]bool{}
	out := []int32{}
	for _, m := range in {
		if m <= 0 || m > 60*24*366 {
			return nil, model.Invalid("lead_minutes harus 1..527040")
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) > 5 {
		return nil, model.Invalid("maksimal 5 pengingat bertahap")
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out, nil
}

// ReleaseStale mengembalikan klaim 'sending' yang macet (worker mati) ke pending.
func (s *ReminderService) ReleaseStale(ctx context.Context) (int64, error) {
	return s.repos.Reminder.ReleaseStale(ctx, 10*time.Minute)
}
