package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/telegram"
)

type fakeNotes struct {
	got     []model.CreateNoteInput
	userID  int64
	err     error
	notes   map[int64]model.Note // untuk Get/Delete, kunci = id note milik vaultID
	deleted []int64
}

func (f *fakeNotes) Get(_ context.Context, userID, id int64) (model.Note, error) {
	n, ok := f.notes[id]
	if !ok || userID != vaultID {
		return model.Note{}, model.ErrNotFound
	}
	return n, nil
}

func (f *fakeNotes) Delete(_ context.Context, userID, id int64) error {
	if _, ok := f.notes[id]; !ok || userID != vaultID {
		return model.ErrNotFound
	}
	delete(f.notes, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeNotes) Create(_ context.Context, userID int64, in model.CreateNoteInput) (model.NoteResult, error) {
	f.got, f.userID = append(f.got, in), userID
	if f.err != nil {
		return model.NoteResult{}, f.err
	}
	typ := in.Type
	if typ == "" {
		typ = model.NoteTypeNote
	}
	return model.NoteResult{Note: model.Note{ID: 9, Type: typ}}, nil
}

type fakeSearch struct {
	hits   []model.SearchHit
	userID int64
	q      string
}

func (f *fakeSearch) Search(_ context.Context, userID int64, q string, _ model.NoteFilter, _ int) ([]model.SearchHit, string, error) {
	f.userID, f.q = userID, q
	return f.hits, "hybrid", nil
}

type fakeAudit struct{ entries []repository.AuditEntry }

func (f *fakeAudit) Log(_ context.Context, e repository.AuditEntry) { f.entries = append(f.entries, e) }

const (
	tgArif  = int64(111)
	vaultID = int64(7)
)

func newBot() (*Bot, *fakeNotes, *fakeSearch, *fakeAudit) {
	n, s, a := &fakeNotes{}, &fakeSearch{}, &fakeAudit{}
	return New(nil, n, s, a, map[int64]int64{tgArif: vaultID}), n, s, a
}

func msg(from int64, chatType, text string) *telegram.Message {
	return &telegram.Message{From: &telegram.User{ID: from}, Chat: telegram.Chat{ID: from, Type: chatType}, Text: text}
}

func TestHandleAkunTidakTerhubungTidakMenyentuhData(t *testing.T) {
	b, notes, search, _ := newBot()
	for _, text := range []string{"/create rahasia", "nginx", "/cari nginx"} {
		r, ok := b.Handle(context.Background(), msg(999, "private", text))
		reply := r.Text
		if !ok || !strings.Contains(reply, "belum terhubung") || !strings.Contains(reply, "999") {
			t.Errorf("Handle(%q) dari akun asing = %q", text, reply)
		}
	}
	if len(notes.got) != 0 || search.q != "" {
		t.Errorf("akun asing tidak boleh membuat/mencari note: notes=%v q=%q", notes.got, search.q)
	}
}

func TestHandleBukanChatPribadiDiabaikan(t *testing.T) {
	b, notes, _, _ := newBot()
	if _, ok := b.Handle(context.Background(), msg(tgArif, "group", "/create x")); ok {
		t.Error("pesan grup harus diabaikan")
	}
	bot := msg(tgArif, "private", "/create x")
	bot.From.IsBot = true
	if _, ok := b.Handle(context.Background(), bot); ok {
		t.Error("pesan dari bot harus diabaikan")
	}
	if len(notes.got) != 0 {
		t.Errorf("tidak boleh ada note dibuat: %v", notes.got)
	}
}

func TestHandleCreate(t *testing.T) {
	b, notes, _, audit := newBot()
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "/create@arpsips_bot beli domain vaulty.id"))
	reply := r.Text
	if len(notes.got) != 1 || notes.got[0].Body != "beli domain vaulty.id" || notes.userID != vaultID {
		t.Fatalf("Create dipanggil dengan %v user %d", notes.got, notes.userID)
	}
	if !strings.Contains(reply, "Tersimpan") {
		t.Errorf("balasan = %q", reply)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "note.create" || audit.entries[0].Metadata["via"] != "telegram" {
		t.Errorf("audit = %+v", audit.entries)
	}
}

func TestHandleCreateCmd(t *testing.T) {
	cases := []struct {
		text, wantTitle, wantBody string
	}{
		{"/create-cmd cek proses | ps aux | grep nginx", "cek proses", "ps aux | grep nginx"},
		{"/create-cmd restart nginx\ndocker compose up -d --force-recreate", "restart nginx", "docker compose up -d --force-recreate"},
	}
	for _, c := range cases {
		b, notes, _, _ := newBot()
		b.Handle(context.Background(), msg(tgArif, "private", c.text))
		if len(notes.got) != 1 {
			t.Fatalf("Handle(%q): Create tidak dipanggil", c.text)
		}
		got := notes.got[0]
		if got.Type != model.NoteTypeCommand || got.Title != c.wantTitle || got.Body != c.wantBody {
			t.Errorf("Handle(%q) = %+v; want title %q body %q", c.text, got, c.wantTitle, c.wantBody)
		}
	}
}

func TestHandleInputTidakValid(t *testing.T) {
	b, notes, _, _ := newBot()
	for _, text := range []string{"/create", "/create-cmd tanpa pemisah", "/create-cmd | ls"} {
		r, ok := b.Handle(context.Background(), msg(tgArif, "private", text))
		if !ok || strings.Contains(r.Text, "Tersimpan") {
			t.Errorf("Handle(%q) = %q; harus ditolak", text, r.Text)
		}
	}
	if len(notes.got) != 0 {
		t.Errorf("input tidak valid tidak boleh disimpan: %v", notes.got)
	}
}

func TestHandleErrorInternalTidakBocor(t *testing.T) {
	b, notes, _, _ := newBot()
	notes.err = errors.New("pq: connection refused 10.0.0.5")
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "/create x"))
	reply := r.Text
	if strings.Contains(reply, "10.0.0.5") || !strings.Contains(reply, "kesalahan di server") {
		t.Errorf("balasan = %q; detail internal tidak boleh tampil", reply)
	}
	notes.err = model.ErrEmptyNote
	if r, _ := b.Handle(context.Background(), msg(tgArif, "private", "/create x")); !strings.Contains(r.Text, model.ErrEmptyNote.Error()) {
		t.Errorf("error domain harus tampil, dapat %q", reply)
	}
}

func TestHandleSearchCommandVerbatimDanDiEscape(t *testing.T) {
	b, _, search, _ := newBot()
	title := "restart <nginx>"
	search.hits = []model.SearchHit{{Note: model.Note{
		Type: model.NoteTypeCommand, Title: &title,
		Body: "docker compose up -d && curl localhost/health", AutoTags: []string{"nginx"},
	}}}
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "reload web server"))
	reply := r.Text
	if search.q != "reload web server" || search.userID != vaultID {
		t.Fatalf("Search dipanggil dengan q=%q user=%d", search.q, search.userID)
	}
	if !strings.Contains(reply, "<pre>docker compose up -d &amp;&amp; curl localhost/health</pre>") ||
		!strings.Contains(reply, "<b>1.</b> ⌨️ <b>restart &lt;nginx&gt;</b>") || !strings.Contains(reply, "🏷 nginx") {
		t.Errorf("balasan = %q", reply)
	}
}

func TestHandleSearchUrutMenurutWaktuDibuat(t *testing.T) {
	b, _, search, _ := newBot()
	at := func(day int) time.Time { return time.Date(2026, 10, day, 3, 0, 0, 0, time.UTC) }
	// Urutan relevansi dari search: 1, 3, 2.
	search.hits = []model.SearchHit{
		{Note: model.Note{Type: model.NoteTypeNote, Body: "satu", CreatedAt: at(1)}},
		{Note: model.Note{Type: model.NoteTypeNote, Body: "tiga", CreatedAt: at(3)}},
		{Note: model.Note{Type: model.NoteTypeNote, Body: "dua", CreatedAt: at(2)}},
	}
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "topup"))
	reply := r.Text
	i1, i2, i3 := strings.Index(reply, "satu"), strings.Index(reply, "dua"), strings.Index(reply, "tiga")
	if !(i1 < i2 && i2 < i3) {
		t.Errorf("hasil harus urut waktu dibuat (satu, dua, tiga):\n%s", reply)
	}
}

func TestFormatHitNoteTanpaJudul(t *testing.T) {
	got := formatHit(0, model.Note{
		Type: model.NoteTypeNote, Body: "topup ml tanggal 16 okt",
		CreatedAt: time.Date(2026, 10, 16, 14, 5, 0, 0, time.UTC),
		AutoTags:  []string{"ml", "topup", "oktober-16", "game", "kelima"},
	})
	want := "📝 topup ml tanggal 16 okt\n<i>🕒 16 Okt 2026, 21:05 WIB</i>\n<i>🏷 ml · topup · oktober-16 · game</i>"
	if got != want {
		t.Errorf("formatHit = %q\nwant %q", got, want)
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct{ in, cmd, arg string }{
		{"/create halo dunia", "create", "halo dunia"},
		{"/Create@arpsips_bot halo", "create", "halo"},
		{"/help", "help", ""},
		{"cara restart nginx", "", "cara restart nginx"},
		{"/create-cmd judul\nls -la", "create-cmd", "judul\nls -la"},
		{"/create_cmd a | b", "create_cmd", "a | b"},
	}
	for _, c := range cases {
		if cmd, arg := parseCommand(c.in); cmd != c.cmd || arg != c.arg {
			t.Errorf("parseCommand(%q) = %q, %q; want %q, %q", c.in, cmd, arg, c.cmd, c.arg)
		}
	}
}

func cb(from int64, data string) *telegram.CallbackQuery {
	return &telegram.CallbackQuery{ID: "cb", From: &telegram.User{ID: from},
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: from, Type: "private"}}, Data: data}
}

func TestMenuLaluKirimIsi(t *testing.T) {
	b, notes, _, _ := newBot()
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "Vault"))
	if len(r.Keyboard) == 0 {
		t.Fatalf("ketik vault harus memunculkan tombol menu, dapat %+v", r)
	}

	b.HandleCallback(context.Background(), cb(tgArif, "mode:cmd"))
	b.Handle(context.Background(), msg(tgArif, "private", "cek proses | ps aux | grep nginx"))
	b.HandleCallback(context.Background(), cb(tgArif, "mode:note"))
	b.Handle(context.Background(), msg(tgArif, "private", "beli domain"))
	if len(notes.got) != 2 || notes.got[0].Type != model.NoteTypeCommand || notes.got[0].Body != "ps aux | grep nginx" ||
		notes.got[1].Body != "beli domain" {
		t.Fatalf("notes = %+v", notes.got)
	}

	// Mode hanya untuk satu pesan: pesan berikutnya kembali jadi pencarian.
	b.Handle(context.Background(), msg(tgArif, "private", "domain"))
	if len(notes.got) != 2 {
		t.Errorf("tanpa memilih menu harus cari, bukan simpan: %+v", notes.got)
	}
}

func TestModeKedaluwarsaDanBatal(t *testing.T) {
	b, notes, search, _ := newBot()
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	b.HandleCallback(context.Background(), cb(tgArif, "mode:note"))
	now = now.Add(pendingTTL + time.Minute)
	b.Handle(context.Background(), msg(tgArif, "private", "nginx"))
	if len(notes.got) != 0 || search.q != "nginx" {
		t.Errorf("mode kedaluwarsa harus jadi pencarian: notes=%v q=%q", notes.got, search.q)
	}

	b.HandleCallback(context.Background(), cb(tgArif, "mode:note"))
	b.HandleCallback(context.Background(), cb(tgArif, "mode:off"))
	b.Handle(context.Background(), msg(tgArif, "private", "redis"))
	if len(notes.got) != 0 {
		t.Errorf("setelah batal tidak boleh simpan: %v", notes.got)
	}
}

func TestHapusDenganKonfirmasi(t *testing.T) {
	b, notes, search, audit := newBot()
	notes.notes = map[int64]model.Note{9: {ID: 9, Type: model.NoteTypeNote, Body: "topup ml"}}
	search.hits = []model.SearchHit{{Note: notes.notes[9]}}
	r, _ := b.Handle(context.Background(), msg(tgArif, "private", "topup"))
	if len(r.Keyboard) != 1 || r.Keyboard[0][0].CallbackData != "del:9" {
		t.Fatalf("hasil cari harus punya tombol hapus, dapat %+v", r.Keyboard)
	}

	res := b.HandleCallback(context.Background(), cb(tgArif, "del:9"))
	if res.Send == nil || !strings.Contains(res.Send.Text, "topup ml") || len(notes.deleted) != 0 {
		t.Fatalf("tombol hapus harus minta konfirmasi dulu: %+v deleted=%v", res, notes.deleted)
	}
	b.HandleCallback(context.Background(), cb(tgArif, "delok:9"))
	if len(notes.deleted) != 1 || notes.deleted[0] != 9 {
		t.Fatalf("deleted = %v", notes.deleted)
	}
	if last := audit.entries[len(audit.entries)-1]; last.Action != "note.delete" || last.Metadata["via"] != "telegram" {
		t.Errorf("audit = %+v", last)
	}
	if res := b.HandleCallback(context.Background(), cb(tgArif, "delok:9")); !strings.Contains(res.Toast, "tidak ada") {
		t.Errorf("hapus ulang = %+v", res)
	}
}

func TestCallbackAkunAsingTidakMenghapus(t *testing.T) {
	b, notes, _, _ := newBot()
	notes.notes = map[int64]model.Note{9: {ID: 9}}
	b.HandleCallback(context.Background(), cb(999, "delok:9"))
	if len(notes.deleted) != 0 {
		t.Errorf("akun asing tidak boleh menghapus: %v", notes.deleted)
	}
}
