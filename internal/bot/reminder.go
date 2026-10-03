package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/aiagent"
	"vaulty-api/pkg/telegram"
)

// Reminders dipenuhi *service.ReminderService.
type Reminders interface {
	Create(ctx context.Context, userID int64, in model.CreateReminderInput) (model.Reminder, error)
	ListUpcoming(ctx context.Context, userID int64, limit int) ([]model.Reminder, error)
	Cancel(ctx context.Context, userID, id int64) (model.Reminder, error)
	Done(ctx context.Context, userID, id int64) (model.Reminder, error)
	Snooze(ctx context.Context, userID, id int64, d time.Duration) (model.Reminder, error)
}

// draft: reminder hasil membaca kalimat, menunggu konfirmasi user (in-memory per ID Telegram).
type draft struct {
	in    model.CreateReminderInput
	until time.Time
}

const (
	modeRemind = "remind"
	maxListed  = 10
)

// Pengingat bertahap yang ditawarkan di konfirmasi, hanya jika acaranya lebih jauh dari lead itu.
var leadOptions = []int{30 * 1440, 7 * 1440, 1440, 60}

var repeatCycle = []string{model.RepeatNone, model.RepeatDaily, model.RepeatWeekly, model.RepeatMonthly, model.RepeatYearly}

var repeatLabel = map[string]string{
	model.RepeatNone: "Sekali", model.RepeatDaily: "Tiap hari", model.RepeatWeekly: "Tiap minggu",
	model.RepeatMonthly: "Tiap bulan", model.RepeatYearly: "Tiap tahun",
}

// reminderTrigger: teks biasa yang diawali "ingatkan ..." langsung dianggap membuat reminder.
var reminderTrigger = regexp.MustCompile(`(?i)^\s*(ingatkan|ingetin|ingatin|remind)\b\s*\S`)

var reminderMenu = Reply{
	Text: "⏰ <b>Reminder</b>",
	Keyboard: telegram.Keyboard{
		{{Text: "➕ Buat reminder", CallbackData: "mode:" + modeRemind}, {Text: "📋 Daftar reminder", CallbackData: "rlist"}},
	},
}

const remindPrompt = "⏰ Kirim reminder-nya, mis.\n<i>besok jam 9 follow up client X</i>\n<i>tiap senin jam 8 cek backup</i>\n<i>1 agustus 2027 perpanjang server, ingatkan H-30 dan H-7</i>"

// startReminder membaca kalimat lalu menampilkan konfirmasi. Reminder baru tersimpan setelah ✅ Simpan,
// karena model gratis kadang salah membaca tanggal.
func (b *Bot) startReminder(ctx context.Context, tgID int64, text string) Reply {
	now := b.now()
	d, err := b.parser.ParseReminder(ctx, text, now)
	if errors.Is(err, aiagent.ErrNoReminderTime) {
		b.setPending(tgID, modeRemind)
		return Reply{Text: "🤔 Kapan diingatkan? Kirim ulang lengkap dengan waktunya, mis. <i>besok jam 9 follow up client X</i>.", Keyboard: cancelKeyboard}
	}
	if err != nil {
		return Reply{Text: userError(ctx, "baca reminder via telegram", err)}
	}
	if d.Text == "" {
		d.Text = "Reminder"
	}
	if !d.At.After(now) {
		return Reply{Text: fmt.Sprintf("⚠️ Waktunya sudah lewat (%s). Kirim ulang dengan waktu lain.", formatWhen(d.At))}
	}
	// Default hanya saat acara; pengingat awal (H-1, H-7, ...) dicentang user atau disebut di kalimat.
	dr := &draft{in: model.CreateReminderInput{Text: d.Text, RemindAt: d.At, Repeat: d.Repeat, LeadMinutes: d.LeadMinutes}, until: now.Add(pendingTTL)}
	b.mu.Lock()
	b.drafts[tgID] = dr
	b.mu.Unlock()
	return draftReply(dr.in, now)
}

func draftReply(in model.CreateReminderInput, now time.Time) Reply {
	var sb strings.Builder
	fmt.Fprintf(&sb, "⏰ <b>%s</b>\n🗓 %s\n🔁 %s", html.EscapeString(in.Text), formatWhen(in.RemindAt), repeatLabel[in.Repeat])
	if len(in.LeadMinutes) > 0 {
		sb.WriteString("\n🔔 Diingatkan juga: " + leadsLabel(in.LeadMinutes))
	}
	sb.WriteString("\n\n<i>Waktu salah? Kirim ulang kalimatnya.</i>")

	var toggles []telegram.Button
	for _, m := range leadOptions {
		if in.RemindAt.Sub(now) > time.Duration(m)*time.Minute {
			label := leadLabel(m)
			if slices.Contains(in.LeadMinutes, m) {
				label = "✅ " + label
			}
			toggles = append(toggles, telegram.Button{Text: label, CallbackData: fmt.Sprintf("rlead:%d", m)})
		}
	}
	kb := telegram.Keyboard{}
	if len(toggles) > 0 {
		kb = append(kb, toggles)
	}
	kb = append(kb,
		[]telegram.Button{{Text: "🔁 " + repeatLabel[in.Repeat], CallbackData: "rrep"}},
		[]telegram.Button{{Text: "✅ Simpan", CallbackData: "rsave"}, {Text: "✖️ Batal", CallbackData: "rdno"}},
	)
	return Reply{Text: sb.String(), Keyboard: kb}
}

// handleReminderCallback memproses tombol reminder. ok=false jika action bukan milik reminder.
func (b *Bot) handleReminderCallback(ctx context.Context, tgID, userID int64, action, arg string) (CallbackResult, bool) {
	switch action {
	case "rmenu":
		return CallbackResult{Edit: &reminderMenu}, true
	case "rlist":
		r := b.listReminders(ctx, userID)
		return CallbackResult{Send: &r}, true
	case "rlead", "rrep", "rsave", "rdno":
		return b.draftAction(ctx, tgID, userID, action, arg), true
	case "rcan":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return CallbackResult{}, true
		}
		return CallbackResult{Send: &Reply{
			Text: "Batalkan reminder ini? Semua pengulangannya ikut berhenti.",
			Keyboard: telegram.Keyboard{{
				{Text: "🗑 Ya, batalkan", CallbackData: fmt.Sprintf("rcanok:%d", id)},
				{Text: "✖️ Tidak", CallbackData: "rcno"},
			}},
		}}, true
	case "rcno":
		return CallbackResult{Edit: &Reply{Text: "Tidak jadi dibatalkan."}}, true
	case "rcanok":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return CallbackResult{}, true
		}
		r, err := b.reminders.Cancel(ctx, userID, id)
		if err != nil {
			return CallbackResult{Toast: reminderToast(ctx, err)}, true
		}
		b.auditReminder(ctx, userID, "reminder.cancel", id)
		return CallbackResult{Toast: "Dibatalkan", Edit: &Reply{Text: "🗑 Dibatalkan: " + html.EscapeString(r.Text)}}, true
	case "rdone":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return CallbackResult{}, true
		}
		r, err := b.reminders.Done(ctx, userID, id)
		if err != nil {
			return CallbackResult{Toast: reminderToast(ctx, err)}, true
		}
		text := "✅ <b>" + html.EscapeString(r.Text) + "</b> — selesai"
		if r.Repeat != model.RepeatNone {
			text += fmt.Sprintf("\n<i>🔁 Berikutnya: %s</i>", formatWhen(r.RemindAt))
		}
		return CallbackResult{Toast: "Selesai", Edit: &Reply{Text: text}}, true
	case "rsnz":
		ids, mins, _ := strings.Cut(arg, ":")
		id, err1 := strconv.ParseInt(ids, 10, 64)
		m, err2 := strconv.Atoi(mins)
		if err1 != nil || err2 != nil {
			return CallbackResult{}, true
		}
		r, err := b.reminders.Snooze(ctx, userID, id, time.Duration(m)*time.Minute)
		if err != nil {
			return CallbackResult{Toast: reminderToast(ctx, err)}, true
		}
		return CallbackResult{Toast: "Ditunda", Edit: &Reply{
			Text: fmt.Sprintf("⏰ <b>%s</b>\n<i>Ditunda sampai %s</i>", html.EscapeString(r.Text), formatWhen(r.NotifyAt)),
		}}, true
	}
	return CallbackResult{}, false
}

func (b *Bot) draftAction(ctx context.Context, tgID, userID int64, action, arg string) CallbackResult {
	b.mu.Lock()
	dr, ok := b.drafts[tgID]
	if ok && b.now().After(dr.until) {
		delete(b.drafts, tgID)
		ok = false
	}
	if !ok {
		b.mu.Unlock()
		return CallbackResult{Toast: "Draft reminder sudah kedaluwarsa, kirim ulang.", Edit: &Reply{Text: "Draft reminder kedaluwarsa."}}
	}
	switch action {
	case "rlead":
		m, _ := strconv.Atoi(arg)
		if i := slices.Index(dr.in.LeadMinutes, m); i >= 0 {
			dr.in.LeadMinutes = slices.Delete(slices.Clone(dr.in.LeadMinutes), i, i+1)
		} else if m > 0 {
			dr.in.LeadMinutes = append(slices.Clone(dr.in.LeadMinutes), m)
			slices.Sort(dr.in.LeadMinutes)
			slices.Reverse(dr.in.LeadMinutes)
		}
	case "rrep":
		i := slices.Index(repeatCycle, dr.in.Repeat)
		dr.in.Repeat = repeatCycle[(i+1)%len(repeatCycle)]
	case "rdno":
		delete(b.drafts, tgID)
		b.mu.Unlock()
		return CallbackResult{Edit: &Reply{Text: "Reminder dibatalkan."}}
	case "rsave":
		delete(b.drafts, tgID)
		in := dr.in
		b.mu.Unlock()
		r, err := b.reminders.Create(ctx, userID, in)
		if err != nil {
			return CallbackResult{Edit: &Reply{Text: userError(ctx, "simpan reminder via telegram", err)}}
		}
		b.auditReminder(ctx, userID, "reminder.create", r.ID)
		return CallbackResult{Toast: "Tersimpan", Edit: &Reply{Text: "✅ Reminder tersimpan\n\n" + formatReminder(0, r)}}
	}
	in := dr.in
	b.mu.Unlock()
	r := draftReply(in, b.now())
	return CallbackResult{Edit: &r}
}

func (b *Bot) listReminders(ctx context.Context, userID int64) Reply {
	rs, err := b.reminders.ListUpcoming(ctx, userID, maxListed)
	if err != nil {
		return Reply{Text: userError(ctx, "list reminder via telegram", err)}
	}
	if len(rs) == 0 {
		return Reply{Text: "Belum ada reminder aktif.\nKetik misalnya: <i>ingatkan besok jam 9 follow up client X</i>"}
	}
	var sb strings.Builder
	sb.WriteString("⏰ <b>Reminder aktif</b>")
	var buttons []telegram.Button
	for i, r := range rs {
		sb.WriteString("\n\n" + formatReminder(i+1, r))
		buttons = append(buttons, telegram.Button{Text: fmt.Sprintf("🗑 %d", i+1), CallbackData: fmt.Sprintf("rcan:%d", r.ID)})
	}
	var kb telegram.Keyboard
	for len(buttons) > 0 {
		k := min(5, len(buttons))
		kb, buttons = append(kb, buttons[:k]), buttons[k:]
	}
	return Reply{Text: sb.String(), Keyboard: kb}
}

// formatReminder: judul, waktu acara, pola ulang, dan pengingat awal. n = 0 berarti tanpa nomor.
func formatReminder(n int, r model.Reminder) string {
	var sb strings.Builder
	if n > 0 {
		fmt.Fprintf(&sb, "<b>%d.</b> ", n)
	}
	fmt.Fprintf(&sb, "⏰ <b>%s</b>\n<i>🗓 %s", html.EscapeString(r.Text), formatWhen(r.RemindAt))
	if r.Repeat != model.RepeatNone {
		sb.WriteString(" · 🔁 " + strings.ToLower(repeatLabel[r.Repeat]))
	}
	if len(r.LeadMinutes) > 0 {
		sb.WriteString(" · 🔔 " + leadsLabel(r.LeadMinutes))
	}
	sb.WriteString("</i>")
	if !r.NotifyAt.Equal(r.RemindAt) && !r.NotifyAt.IsZero() {
		fmt.Fprintf(&sb, "\n<i>Notifikasi berikutnya: %s</i>", formatWhen(r.NotifyAt))
	}
	return sb.String()
}

func (b *Bot) auditReminder(ctx context.Context, userID int64, action string, id int64) {
	b.audit.Log(ctx, repository.AuditEntry{
		UserID: userID, Action: action, Entity: "reminder", EntityID: id,
		Metadata: map[string]any{"via": "telegram"},
	})
}

// reminderToast: pesan singkat untuk toast tombol (teks polos).
func reminderToast(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, model.ErrNotFound):
		return "Reminder sudah tidak ada."
	case errors.Is(err, model.ErrReminderInvalid):
		return "Reminder ini sudah selesai atau dibatalkan."
	}
	return plainError(ctx, "aksi reminder via telegram", err)
}

var hariPendek = [...]string{"Min", "Sen", "Sel", "Rab", "Kam", "Jum", "Sab"}

// formatWhen: "Sen, 5 Okt 2026 · 09:00 WIB".
func formatWhen(t time.Time) string {
	t = t.In(wib)
	return fmt.Sprintf("%s, %d %s %d · %02d:%02d WIB", hariPendek[t.Weekday()], t.Day(), bulan[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

func leadLabel(m int) string {
	switch {
	case m%1440 == 0:
		return fmt.Sprintf("H-%d", m/1440)
	case m%60 == 0:
		return fmt.Sprintf("%d jam sebelum", m/60)
	}
	return fmt.Sprintf("%d menit sebelum", m)
}

func leadsLabel(ms []int) string {
	labels := make([]string, len(ms))
	for i, m := range ms {
		labels[i] = leadLabel(m)
	}
	return strings.Join(labels, ", ")
}

// untilLabel: "7 hari lagi", "2 jam lagi", "15 menit lagi".
func untilLabel(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%d hari lagi", int((d+12*time.Hour)/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%d jam lagi", int((d+30*time.Minute)/time.Hour))
	}
	return fmt.Sprintf("%d menit lagi", max(1, int((d+30*time.Second)/time.Minute)))
}

// Sender cukup untuk mengirim notifikasi (dipakai worker, bukan proses bot).
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text, parseMode string, kb telegram.Keyboard) error
}

// Notifier mengirim notifikasi reminder ke Telegram (service.ReminderNotifier).
type Notifier struct {
	tg    Sender
	chats map[int64]int64 // user_id Vaulty -> chat ID Telegram (chat pribadi = ID Telegram)
	now   func() time.Time
}

// NewNotifier: telegramUsers = ID Telegram -> user_id (hasil bootstrap.TelegramUsers).
func NewNotifier(tg Sender, telegramUsers map[int64]int64) *Notifier {
	chats := make(map[int64]int64, len(telegramUsers))
	for tgID, userID := range telegramUsers {
		chats[userID] = tgID
	}
	return &Notifier{tg: tg, chats: chats, now: time.Now}
}

var errNoChannel = errors.New("user belum menghubungkan Telegram")

func (n *Notifier) NotifyReminder(ctx context.Context, r model.Reminder) error {
	chat, ok := n.chats[r.UserID]
	if !ok {
		return errNoChannel
	}
	text, kb := notificationReply(r, n.now())
	return n.tg.SendMessage(ctx, chat, text, "HTML", kb)
}

func notificationReply(r model.Reminder, now time.Time) (string, telegram.Keyboard) {
	if r.NotifyKind == model.NotifyLead {
		return fmt.Sprintf("⏳ <b>%s</b> · %s\n<i>🗓 %s</i>", html.EscapeString(r.Text), untilLabel(r.RemindAt.Sub(now)), formatWhen(r.RemindAt)),
			telegram.Keyboard{{{Text: "📋 Daftar reminder", CallbackData: "rlist"}}}
	}
	text := fmt.Sprintf("🔔 <b>%s</b>\n<i>🗓 %s", html.EscapeString(r.Text), formatWhen(r.RemindAt))
	if r.Repeat != model.RepeatNone {
		text += " · 🔁 " + strings.ToLower(repeatLabel[r.Repeat])
	}
	text += "</i>"
	kb := telegram.Keyboard{
		{{Text: "✅ Selesai", CallbackData: fmt.Sprintf("rdone:%d", r.ID)}},
		{
			{Text: "⏰ 10 mnt", CallbackData: fmt.Sprintf("rsnz:%d:10", r.ID)},
			{Text: "⏰ 1 jam", CallbackData: fmt.Sprintf("rsnz:%d:60", r.ID)},
			{Text: "⏰ Besok", CallbackData: fmt.Sprintf("rsnz:%d:1440", r.ID)},
		},
	}
	if r.NoteID != nil {
		kb = append(kb, []telegram.Button{{Text: "📄 Buka catatan", CallbackData: fmt.Sprintf("open:%d", *r.NoteID)}})
	}
	return text, kb
}
