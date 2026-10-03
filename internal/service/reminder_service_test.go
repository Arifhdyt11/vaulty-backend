package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
)

var wib = time.FixedZone("WIB", 7*3600)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, wib)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextOccurrence(t *testing.T) {
	cases := []struct{ from, repeat, want string }{
		{"2026-10-05 09:00", model.RepeatDaily, "2026-10-06 09:00"},
		{"2026-10-05 09:00", model.RepeatWeekly, "2026-10-12 09:00"},
		{"2026-01-31 09:00", model.RepeatMonthly, "2026-02-28 09:00"},
		{"2028-01-31 09:00", model.RepeatMonthly, "2028-02-29 09:00"},
		{"2026-12-15 08:30", model.RepeatMonthly, "2027-01-15 08:30"},
		{"2028-02-29 09:00", model.RepeatYearly, "2029-02-28 09:00"},
	}
	for _, c := range cases {
		if got := NextOccurrence(at(c.from), c.repeat); !got.Equal(at(c.want)) {
			t.Errorf("NextOccurrence(%s, %s) = %s; want %s", c.from, c.repeat, got.In(wib), c.want)
		}
	}
}

func TestNextNotifyBertahap(t *testing.T) {
	event := at("2027-08-01 09:00")
	leads := []int{30 * 1440, 7 * 1440, 1440}
	cases := []struct{ now, want, kind string }{
		{"2026-10-04 10:00", "2027-07-02 09:00", model.NotifyLead}, // H-30
		{"2027-07-02 09:00", "2027-07-25 09:00", model.NotifyLead}, // setelah H-30 → H-7
		{"2027-07-31 09:00", "2027-08-01 09:00", model.NotifyEvent},
	}
	for _, c := range cases {
		got, kind, ok := NextNotify(event, leads, at(c.now))
		if !ok || !got.Equal(at(c.want)) || kind != c.kind {
			t.Errorf("now %s: NextNotify = %s %s %v; want %s %s", c.now, got.In(wib), kind, ok, c.want, c.kind)
		}
	}
	if _, _, ok := NextNotify(event, leads, at("2027-08-01 09:00")); ok {
		t.Error("acara yang sudah lewat tidak punya notifikasi berikutnya")
	}
}

func TestAfterSend(t *testing.T) {
	now := at("2026-10-05 09:00")
	cases := []struct {
		name                       string
		r                          model.Reminder
		status, remindAt, notifyAt string
		kind                       string
	}{
		{"sekali selesai", model.Reminder{RemindAt: now, NotifyKind: model.NotifyEvent},
			model.ReminderSent, "2026-10-05 09:00", "2026-10-05 09:00", model.NotifyEvent},
		{"mingguan + H-1", model.Reminder{RemindAt: now, Repeat: model.RepeatWeekly, LeadMinutes: []int{1440}, NotifyKind: model.NotifyEvent},
			model.ReminderPending, "2026-10-12 09:00", "2026-10-11 09:00", model.NotifyLead},
		{"harian, worker telat 3 hari", model.Reminder{RemindAt: at("2026-10-02 09:00"), Repeat: model.RepeatDaily, NotifyKind: model.NotifyEvent},
			model.ReminderPending, "2026-10-06 09:00", "2026-10-06 09:00", model.NotifyEvent},
		{"lead → acara", model.Reminder{RemindAt: at("2026-10-06 09:00"), LeadMinutes: []int{1440}, NotifyKind: model.NotifyLead},
			model.ReminderPending, "2026-10-06 09:00", "2026-10-06 09:00", model.NotifyEvent},
		{"tunda setelah acara sekali", model.Reminder{RemindAt: at("2026-10-05 08:00"), NotifyKind: model.NotifySnooze},
			model.ReminderSent, "2026-10-05 08:00", "2026-10-05 08:00", model.NotifyEvent},
	}
	for _, c := range cases {
		status, remindAt, notifyAt, kind := afterSend(c.r, now)
		if status != c.status || !remindAt.Equal(at(c.remindAt)) || !notifyAt.Equal(at(c.notifyAt)) || kind != c.kind {
			t.Errorf("%s: afterSend = %s %s %s %s", c.name, status, remindAt.In(wib), notifyAt.In(wib), kind)
		}
	}
}

type fakeReminderRepo struct {
	created  []repository.NewReminder
	due      []model.Reminder
	finished map[int64]string
	failed   []int64
	byID     map[int64]model.Reminder
}

func (f *fakeReminderRepo) Create(_ context.Context, n repository.NewReminder) (model.Reminder, error) {
	f.created = append(f.created, n)
	return model.Reminder{ID: 1, Text: n.Text, NotifyAt: n.NotifyAt, NotifyKind: n.NotifyKind}, nil
}
func (f *fakeReminderRepo) FindByID(_ context.Context, _, id int64) (model.Reminder, error) {
	if r, ok := f.byID[id]; ok {
		return r, nil
	}
	return model.Reminder{}, model.ErrNotFound
}
func (f *fakeReminderRepo) ListUpcoming(context.Context, int64, int) ([]model.Reminder, error) {
	return nil, nil
}
func (f *fakeReminderRepo) ClaimDue(context.Context, int) ([]model.Reminder, error) {
	d := f.due
	f.due = nil
	return d, nil
}
func (f *fakeReminderRepo) FinishSend(_ context.Context, id int64, status string, _, _ time.Time, _ string) error {
	f.finished[id] = status
	return nil
}
func (f *fakeReminderRepo) FailSend(_ context.Context, id int64, _ int) error {
	f.failed = append(f.failed, id)
	return nil
}
func (f *fakeReminderRepo) Reschedule(context.Context, int64, int64, time.Time, string) (model.Reminder, error) {
	return model.Reminder{}, model.ErrNotFound
}
func (f *fakeReminderRepo) SetStatus(_ context.Context, _, id int64, status string, _ ...string) (model.Reminder, error) {
	return model.Reminder{ID: id, Status: status}, nil
}

func newReminderSvc(now time.Time) (*ReminderService, *fakeReminderRepo) {
	repo := &fakeReminderRepo{finished: map[int64]string{}, byID: map[int64]model.Reminder{}}
	s := NewReminderService(repo, fakeNoteOwner{})
	s.now = func() time.Time { return now }
	return s, repo
}

func TestReminderCreateValidasi(t *testing.T) {
	now := at("2026-10-04 10:00")
	s, repo := newReminderSvc(now)
	ctx := context.Background()
	bad := []struct {
		in   model.CreateReminderInput
		want error
	}{
		{model.CreateReminderInput{Text: " ", RemindAt: now.Add(time.Hour)}, model.ErrEmptyReminder},
		{model.CreateReminderInput{Text: "x", RemindAt: now.Add(-time.Minute)}, model.ErrReminderPast},
		{model.CreateReminderInput{Text: "x", RemindAt: now.Add(time.Hour), Repeat: "hourly"}, model.ErrInvalidRepeat},
		{model.CreateReminderInput{Text: "x", RemindAt: now.Add(time.Hour), LeadMinutes: []int{0}}, model.ErrInvalidLead},
		{model.CreateReminderInput{Text: "x", RemindAt: now.AddDate(11, 0, 0)}, model.ErrReminderTooFar},
	}
	for _, c := range bad {
		if _, err := s.Create(ctx, 1, c.in); !errors.Is(err, c.want) {
			t.Errorf("Create(%+v) = %v; want %v", c.in, err, c.want)
		}
	}

	// Pengingat awal yang sudah lewat dilewati; notifikasi pertama = H-1.
	_, err := s.Create(ctx, 1, model.CreateReminderInput{Text: " perpanjang server ", RemindAt: at("2026-10-10 09:00"), LeadMinutes: []int{1440, 7 * 1440, 1440}})
	if err != nil {
		t.Fatal(err)
	}
	got := repo.created[0]
	if got.Text != "perpanjang server" || len(got.LeadMinutes) != 2 || got.LeadMinutes[0] != 7*1440 ||
		!got.NotifyAt.Equal(at("2026-10-09 09:00")) || got.NotifyKind != model.NotifyLead {
		t.Errorf("created = %+v", got)
	}
}

type fakeNoteOwner struct{}

// Hanya note 1 milik user 1.
func (fakeNoteOwner) FindByID(_ context.Context, userID, id int64) (model.Note, error) {
	if userID == 1 && id == 1 {
		return model.Note{ID: 1}, nil
	}
	return model.Note{}, model.ErrNotFound
}

func TestReminderNoteMilikUserLainDitolak(t *testing.T) {
	now := at("2026-10-04 10:00")
	s, _ := newReminderSvc(now)
	other := int64(2)
	if _, err := s.Create(context.Background(), 1, model.CreateReminderInput{Text: "x", RemindAt: now.Add(time.Hour), NoteID: &other}); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("note milik user lain = %v; want ErrNotFound", err)
	}
	own := int64(1)
	if _, err := s.Create(context.Background(), 1, model.CreateReminderInput{Text: "x", RemindAt: now.Add(time.Hour), NoteID: &own}); err != nil {
		t.Errorf("note sendiri = %v", err)
	}
}

type fakeNotifier struct {
	sent []int64
	fail map[int64]bool
}

func (f *fakeNotifier) NotifyReminder(_ context.Context, r model.Reminder) error {
	if f.fail[r.ID] {
		return errors.New("telegram down")
	}
	f.sent = append(f.sent, r.ID)
	return nil
}

func TestDeliverDue(t *testing.T) {
	now := at("2026-10-05 09:00")
	s, repo := newReminderSvc(now)
	repo.due = []model.Reminder{
		{ID: 1, RemindAt: now, NotifyKind: model.NotifyEvent},
		{ID: 2, RemindAt: now, Repeat: model.RepeatDaily, NotifyKind: model.NotifyEvent},
		{ID: 3, RemindAt: now, NotifyKind: model.NotifyEvent},
	}
	n := &fakeNotifier{fail: map[int64]bool{3: true}}
	sent, err := s.DeliverDue(context.Background(), n)
	if err != nil || sent != 2 {
		t.Fatalf("DeliverDue = %d, %v", sent, err)
	}
	if repo.finished[1] != model.ReminderSent || repo.finished[2] != model.ReminderPending {
		t.Errorf("finished = %v", repo.finished)
	}
	if len(repo.failed) != 1 || repo.failed[0] != 3 {
		t.Errorf("failed = %v", repo.failed)
	}
}

func TestReminderDoneBerulangTetapJalan(t *testing.T) {
	s, repo := newReminderSvc(at("2026-10-05 09:00"))
	repo.byID[5] = model.Reminder{ID: 5, Repeat: model.RepeatWeekly, Status: model.ReminderPending}
	r, err := s.Done(context.Background(), 1, 5)
	if err != nil || r.Status != model.ReminderPending {
		t.Errorf("Done berulang = %+v, %v; status harus tetap pending", r, err)
	}
	repo.byID[6] = model.Reminder{ID: 6, Status: model.ReminderSent}
	if r, _ := s.Done(context.Background(), 1, 6); r.Status != model.ReminderDone {
		t.Errorf("Done sekali = %+v", r)
	}
}
