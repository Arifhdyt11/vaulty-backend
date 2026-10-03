package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/pkg/aiagent"
	"vaulty-api/pkg/telegram"
)

type fakeParser struct {
	draft aiagent.ReminderDraft
	err   error
	got   string
}

func (f *fakeParser) ParseReminder(_ context.Context, text string, _ time.Time) (aiagent.ReminderDraft, error) {
	f.got = text
	return f.draft, f.err
}

type fakeReminders struct {
	created   []model.CreateReminderInput
	cancelled []int64
	snoozed   time.Duration
	list      []model.Reminder
}

func (f *fakeReminders) Create(_ context.Context, userID int64, in model.CreateReminderInput) (model.Reminder, error) {
	if userID != vaultID {
		return model.Reminder{}, model.ErrNotFound
	}
	f.created = append(f.created, in)
	return model.Reminder{ID: 3, Text: in.Text, RemindAt: in.RemindAt, NotifyAt: in.RemindAt, Repeat: in.Repeat, LeadMinutes: in.LeadMinutes}, nil
}
func (f *fakeReminders) ListUpcoming(context.Context, int64, int) ([]model.Reminder, error) {
	return f.list, nil
}
func (f *fakeReminders) Cancel(_ context.Context, userID, id int64) (model.Reminder, error) {
	if userID != vaultID {
		return model.Reminder{}, model.ErrNotFound
	}
	f.cancelled = append(f.cancelled, id)
	return model.Reminder{ID: id, Text: "x"}, nil
}
func (f *fakeReminders) Done(_ context.Context, _, id int64) (model.Reminder, error) {
	return model.Reminder{ID: id, Text: "x", Status: model.ReminderDone}, nil
}
func (f *fakeReminders) Snooze(_ context.Context, _, id int64, d time.Duration) (model.Reminder, error) {
	f.snoozed = d
	return model.Reminder{ID: id, Text: "x", NotifyAt: time.Now().Add(d)}, nil
}

var botNow = time.Date(2026, 10, 4, 10, 0, 0, 0, wib)

func reminderBot() (*Bot, *fakeParser, *fakeReminders) {
	b, _, _, _ := newBot()
	b.now = func() time.Time { return botNow }
	return b, b.parser.(*fakeParser), b.reminders.(*fakeReminders)
}

func TestReminderKonfirmasiLaluSimpan(t *testing.T) {
	b, parser, rem := reminderBot()
	ctx := context.Background()
	parser.draft = aiagent.ReminderDraft{Text: "follow up client X", At: time.Date(2026, 10, 5, 9, 0, 0, 0, wib)}

	r, _ := b.Handle(ctx, msg(tgArif, "private", "ingatkan besok jam 9 follow up client X"))
	if parser.got != "ingatkan besok jam 9 follow up client X" {
		t.Fatalf("teks 'ingatkan ...' harus ke parser, dapat %q", parser.got)
	}
	if !strings.Contains(r.Text, "Sen, 5 Okt 2026 · 09:00 WIB") || len(rem.created) != 0 {
		t.Fatalf("harus konfirmasi dulu tanpa menyimpan: %q created=%v", r.Text, rem.created)
	}
	// Acara besok: tawaran H-1 tidak muncul (kurang dari sehari), 1 jam sebelum muncul.
	if strings.Contains(callbacks(r.Keyboard), "rlead:1440") || !strings.Contains(callbacks(r.Keyboard), "rlead:60") {
		t.Errorf("tombol lead = %s", callbacks(r.Keyboard))
	}

	b.HandleCallback(ctx, cb(tgArif, "rlead:60"))
	res := b.HandleCallback(ctx, cb(tgArif, "rrep"))
	if !strings.Contains(res.Edit.Text, "Tiap hari") || !strings.Contains(res.Edit.Text, "1 jam sebelum") {
		t.Errorf("draft setelah toggle = %q", res.Edit.Text)
	}
	res = b.HandleCallback(ctx, cb(tgArif, "rsave"))
	if len(rem.created) != 1 || rem.created[0].Repeat != model.RepeatDaily || rem.created[0].LeadMinutes[0] != 60 {
		t.Fatalf("created = %+v", rem.created)
	}
	if !strings.Contains(res.Edit.Text, "Reminder tersimpan") {
		t.Errorf("balasan simpan = %q", res.Edit.Text)
	}
	if res := b.HandleCallback(ctx, cb(tgArif, "rsave")); !strings.Contains(res.Toast, "kedaluwarsa") {
		t.Errorf("simpan dua kali harus ditolak: %+v", res)
	}
}

func TestReminderJauhDefaultBertahap(t *testing.T) {
	b, parser, _ := reminderBot()
	parser.draft = aiagent.ReminderDraft{Text: "perpanjang server", At: time.Date(2027, 8, 1, 9, 0, 0, 0, wib)}
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "/ingatkan 1 agustus 2027 perpanjang server"))
	if !strings.Contains(r.Text, "Diingatkan juga: H-30, H-7, H-1") || !strings.Contains(callbacks(r.Keyboard), "✅ H-30") {
		t.Errorf("draft = %q / %s", r.Text, callbacks(r.Keyboard))
	}
}

func TestReminderTanpaWaktu(t *testing.T) {
	b, parser, _ := reminderBot()
	parser.err = aiagent.ErrNoReminderTime
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "ingatkan follow up client"))
	if !strings.Contains(r.Text, "Kapan") {
		t.Fatalf("balasan = %q", r.Text)
	}
	// Pesan berikutnya dianggap kalimat reminder.
	parser.err = nil
	parser.draft = aiagent.ReminderDraft{Text: "follow up", At: botNow.Add(time.Hour)}
	b.Handle(context.Background(), msg(tgArif, "private", "besok jam 9"))
	if parser.got != "besok jam 9" {
		t.Errorf("pesan lanjutan harus ke parser, dapat %q", parser.got)
	}
}

func TestReminderWaktuLewatDitolak(t *testing.T) {
	b, parser, rem := reminderBot()
	parser.draft = aiagent.ReminderDraft{Text: "x", At: botNow.Add(-time.Hour)}
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "ingatkan kemarin x"))
	if !strings.Contains(r.Text, "sudah lewat") || len(b.drafts) != 0 || len(rem.created) != 0 {
		t.Errorf("balasan = %q", r.Text)
	}
}

func TestReminderListDanBatal(t *testing.T) {
	b, _, rem := reminderBot()
	rem.list = []model.Reminder{{ID: 8, Text: "cek backup", RemindAt: botNow.Add(time.Hour), NotifyAt: botNow.Add(time.Hour), Repeat: model.RepeatWeekly}}
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "/reminder"))
	if !strings.Contains(r.Text, "<b>1.</b> ⏰ <b>cek backup</b>") || !strings.Contains(r.Text, "🔁 tiap minggu") || r.Keyboard[0][0].CallbackData != "rcan:8" {
		t.Fatalf("list = %q %+v", r.Text, r.Keyboard)
	}
	res := b.HandleCallback(context.Background(), cb(tgArif, "rcan:8"))
	if res.Send == nil || len(rem.cancelled) != 0 {
		t.Fatalf("batal harus konfirmasi dulu: %+v", res)
	}
	b.HandleCallback(context.Background(), cb(tgArif, "rcanok:8"))
	if len(rem.cancelled) != 1 {
		t.Errorf("cancelled = %v", rem.cancelled)
	}
	b.HandleCallback(context.Background(), cb(999, "rcanok:8"))
	if len(rem.cancelled) != 1 {
		t.Error("akun asing tidak boleh membatalkan")
	}
}

func TestReminderSnooze(t *testing.T) {
	b, _, rem := reminderBot()
	res := b.HandleCallback(context.Background(), cb(tgArif, "rsnz:8:60"))
	if rem.snoozed != time.Hour || !strings.Contains(res.Edit.Text, "Ditunda sampai") {
		t.Errorf("snooze = %v %+v", rem.snoozed, res)
	}
}

type fakeSender struct {
	chat int64
	text string
	kb   telegram.Keyboard
}

func (f *fakeSender) SendMessage(_ context.Context, chatID int64, text, _ string, kb telegram.Keyboard) error {
	f.chat, f.text, f.kb = chatID, text, kb
	return nil
}

func TestNotifier(t *testing.T) {
	s := &fakeSender{}
	n := NewNotifier(s, map[int64]int64{tgArif: vaultID})
	n.now = func() time.Time { return botNow }
	noteID := int64(13)
	err := n.NotifyReminder(context.Background(), model.Reminder{ID: 4, UserID: vaultID, Text: "cek <backup>",
		RemindAt: botNow, NotifyKind: model.NotifyEvent, Repeat: model.RepeatWeekly, NoteID: &noteID})
	if err != nil || s.chat != tgArif || !strings.HasPrefix(s.text, "🔔 <b>cek &lt;backup&gt;</b>") ||
		!strings.Contains(callbacks(s.kb), "rdone:4") || !strings.Contains(callbacks(s.kb), "rsnz:4:1440") || !strings.Contains(callbacks(s.kb), "open:13") {
		t.Fatalf("notif = %v %d %q %s", err, s.chat, s.text, callbacks(s.kb))
	}

	n.NotifyReminder(context.Background(), model.Reminder{ID: 4, UserID: vaultID, Text: "perpanjang server",
		RemindAt: botNow.Add(7 * 24 * time.Hour), NotifyKind: model.NotifyLead})
	if !strings.Contains(s.text, "⏳ <b>perpanjang server</b> · 7 hari lagi") {
		t.Errorf("notif lead = %q", s.text)
	}

	if err := n.NotifyReminder(context.Background(), model.Reminder{UserID: 42}); err == nil {
		t.Error("user tanpa Telegram harus error")
	}
}

func callbacks(kb telegram.Keyboard) string {
	var out []string
	for _, row := range kb {
		for _, b := range row {
			out = append(out, b.Text+"="+b.CallbackData)
		}
	}
	return strings.Join(out, " ")
}
