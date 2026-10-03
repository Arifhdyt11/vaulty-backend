package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/telegram"
)

type fakeNotes struct {
	got    []model.CreateNoteInput
	userID int64
	err    error
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
		reply, _, ok := b.Handle(context.Background(), msg(999, "private", text))
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
	if _, _, ok := b.Handle(context.Background(), msg(tgArif, "group", "/create x")); ok {
		t.Error("pesan grup harus diabaikan")
	}
	bot := msg(tgArif, "private", "/create x")
	bot.From.IsBot = true
	if _, _, ok := b.Handle(context.Background(), bot); ok {
		t.Error("pesan dari bot harus diabaikan")
	}
	if len(notes.got) != 0 {
		t.Errorf("tidak boleh ada note dibuat: %v", notes.got)
	}
}

func TestHandleCreate(t *testing.T) {
	b, notes, _, audit := newBot()
	reply, _, _ := b.Handle(context.Background(), msg(tgArif, "private", "/create@arpsips_bot beli domain vaulty.id"))
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
		reply, _, ok := b.Handle(context.Background(), msg(tgArif, "private", text))
		if !ok || strings.Contains(reply, "Tersimpan") {
			t.Errorf("Handle(%q) = %q; harus ditolak", text, reply)
		}
	}
	if len(notes.got) != 0 {
		t.Errorf("input tidak valid tidak boleh disimpan: %v", notes.got)
	}
}

func TestHandleErrorInternalTidakBocor(t *testing.T) {
	b, notes, _, _ := newBot()
	notes.err = errors.New("pq: connection refused 10.0.0.5")
	reply, _, _ := b.Handle(context.Background(), msg(tgArif, "private", "/create x"))
	if strings.Contains(reply, "10.0.0.5") || !strings.Contains(reply, "kesalahan di server") {
		t.Errorf("balasan = %q; detail internal tidak boleh tampil", reply)
	}
	notes.err = model.ErrEmptyNote
	if reply, _, _ := b.Handle(context.Background(), msg(tgArif, "private", "/create x")); !strings.Contains(reply, model.ErrEmptyNote.Error()) {
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
	reply, mode, _ := b.Handle(context.Background(), msg(tgArif, "private", "reload web server"))
	if search.q != "reload web server" || search.userID != vaultID {
		t.Fatalf("Search dipanggil dengan q=%q user=%d", search.q, search.userID)
	}
	if mode != "HTML" ||
		!strings.Contains(reply, "<pre>docker compose up -d &amp;&amp; curl localhost/health</pre>") ||
		!strings.Contains(reply, "⌨️ <b>restart &lt;nginx&gt;</b>") || !strings.Contains(reply, "🏷 nginx") {
		t.Errorf("balasan = %q", reply)
	}
}

func TestFormatHitNoteTanpaJudul(t *testing.T) {
	got := formatHit(model.Note{
		Type: model.NoteTypeNote, Body: "topup ml tanggal 16 okt",
		AutoTags: []string{"ml", "topup", "oktober-16", "game", "kelima"},
	})
	want := "📝 topup ml tanggal 16 okt\n<i>🏷 ml · topup · oktober-16 · game</i>"
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
	}
	for _, c := range cases {
		if cmd, arg := parseCommand(c.in); cmd != c.cmd || arg != c.arg {
			t.Errorf("parseCommand(%q) = %q, %q; want %q, %q", c.in, cmd, arg, c.cmd, c.arg)
		}
	}
}
