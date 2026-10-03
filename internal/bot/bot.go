// Package bot adalah bot Telegram Vaulty untuk quick-capture dan pencarian (ADR-021).
// Bot memanggil service yang sama dengan HTTP handler, sehingga validasi, scope user_id
// (ADR-007), audit, dan indexing di worker ikut berlaku.
package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/telegram"
)

type Notes interface {
	Create(ctx context.Context, userID int64, in model.CreateNoteInput) (model.NoteResult, error)
	Get(ctx context.Context, userID, id int64) (model.Note, error)
	Delete(ctx context.Context, userID, id int64) error
	List(ctx context.Context, userID int64, f model.NoteFilter, cursor int64, limit int) ([]model.Note, *int64, error)
}

type Searcher interface {
	Search(ctx context.Context, userID int64, q string, f model.NoteFilter, limit int) ([]model.SearchHit, string, error)
}

type Auditor interface {
	Log(ctx context.Context, e repository.AuditEntry)
}

type TelegramAPI interface {
	GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]telegram.Update, error)
	SendMessage(ctx context.Context, chatID int64, text, parseMode string, kb telegram.Keyboard) error
	EditMessageText(ctx context.Context, chatID, messageID int64, text, parseMode string, kb telegram.Keyboard) error
	AnswerCallbackQuery(ctx context.Context, id, text string) error
	SetMyCommands(ctx context.Context, cmds []telegram.Command) error
}

// Reply adalah pesan balasan berformat HTML, opsional dengan tombol inline.
type Reply struct {
	Text     string
	Keyboard telegram.Keyboard
}

// CallbackResult adalah hasil menekan tombol: Toast tampil sebentar di layar,
// Edit mengganti pesan yang tombolnya ditekan, Send mengirim pesan baru.
type CallbackResult struct {
	Toast string
	Edit  *Reply
	Send  *Reply
}

// Mode input setelah user memilih dari menu: pesan teks berikutnya disimpan sesuai mode.
const (
	modeNote    = "note"
	modeCommand = "cmd"
)

type pending struct {
	mode  string
	until time.Time
}

type Bot struct {
	tg     TelegramAPI
	notes  Notes
	search Searcher
	audit  Auditor
	// users memetakan ID Telegram ke user_id Vaulty. Hanya akun di sini yang dilayani.
	users map[int64]int64

	mu      sync.Mutex
	pending map[int64]pending // per ID Telegram
	now     func() time.Time
}

func New(tg TelegramAPI, notes Notes, search Searcher, audit Auditor, users map[int64]int64) *Bot {
	return &Bot{tg: tg, notes: notes, search: search, audit: audit, users: users,
		pending: map[int64]pending{}, now: time.Now}
}

const (
	searchLimit = 5
	// Batas pesan Telegram 4096 karakter; sisakan ruang untuk tag HTML.
	maxReply   = 3800
	maxPreview = 200
	maxTags    = 4
	// Daftar /semua: 10 catatan per halaman, judul dipotong supaya satu baris.
	pageSize     = 10
	maxListTitle = 70
	// Mode dari menu kedaluwarsa supaya pencarian beberapa jam kemudian tidak ikut tersimpan.
	pendingTTL = 10 * time.Minute
)

const helpText = `<b>Vaulty</b> — simpan &amp; cari catatan.

Ketik <b>vault</b> (atau tombol Menu) lalu pilih mau simpan apa.
Teks biasa tanpa memilih menu = <b>cari</b>.

/create &lt;teks&gt; — simpan catatan (URL saja otomatis jadi link)
/create_cmd &lt;deskripsi&gt; | &lt;command&gt; — simpan command, ditampilkan apa adanya
/cari &lt;kata kunci&gt; — cari catatan
/semua — tampilkan semua catatan
/help — bantuan ini`

// menuCommands mengisi tombol Menu bawaan Telegram di samping kolom ketik.
var menuCommands = []telegram.Command{
	{Command: "menu", Description: "Pilih: simpan catatan / command / cari"},
	{Command: "create", Description: "Simpan catatan: /create <teks>"},
	{Command: "create_cmd", Description: "Simpan command: /create_cmd <deskripsi> | <command>"},
	{Command: "cari", Description: "Cari catatan: /cari <kata kunci>"},
	{Command: "semua", Description: "Tampilkan semua catatan"},
	{Command: "help", Description: "Bantuan"},
}

var menuReply = Reply{
	Text: "Mau simpan apa? Pilih, lalu kirim isinya.\n<i>Teks biasa tanpa memilih = cari.</i>",
	Keyboard: telegram.Keyboard{
		{{Text: "📝 Catatan", CallbackData: "mode:" + modeNote}, {Text: "⌨️ Command", CallbackData: "mode:" + modeCommand}},
		{{Text: "🔎 Cari", CallbackData: "mode:find"}, {Text: "📚 Semua catatan", CallbackData: "list:0:1"}},
	},
}

var cancelKeyboard = telegram.Keyboard{{{Text: "✖️ Batal", CallbackData: "mode:off"}}}

// Run melakukan long polling sampai ctx dibatalkan.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.tg.SetMyCommands(ctx, menuCommands); err != nil {
		slog.WarnContext(ctx, "telegram setMyCommands gagal", "err", err)
	}
	var offset int64
	for {
		ups, err := b.tg.GetUpdates(ctx, offset, 50)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.WarnContext(ctx, "telegram getUpdates gagal", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			switch {
			case u.Message != nil:
				if r, ok := b.Handle(ctx, u.Message); ok {
					b.send(ctx, u.Message.Chat.ID, r)
				}
			case u.CallbackQuery != nil:
				b.runCallback(ctx, u.CallbackQuery)
			}
		}
	}
}

func (b *Bot) send(ctx context.Context, chatID int64, r Reply) {
	if err := b.tg.SendMessage(ctx, chatID, r.Text, "HTML", r.Keyboard); err != nil {
		slog.WarnContext(ctx, "telegram sendMessage gagal", "err", err)
	}
}

func (b *Bot) runCallback(ctx context.Context, cq *telegram.CallbackQuery) {
	res := b.HandleCallback(ctx, cq)
	// Selalu dijawab supaya tombol tidak terus loading.
	if err := b.tg.AnswerCallbackQuery(ctx, cq.ID, res.Toast); err != nil {
		slog.WarnContext(ctx, "telegram answerCallbackQuery gagal", "err", err)
	}
	if cq.Message == nil {
		return
	}
	if res.Edit != nil {
		if err := b.tg.EditMessageText(ctx, cq.Message.Chat.ID, cq.Message.MessageID, res.Edit.Text, "HTML", res.Edit.Keyboard); err != nil {
			slog.WarnContext(ctx, "telegram editMessageText gagal", "err", err)
		}
	}
	if res.Send != nil {
		b.send(ctx, cq.Message.Chat.ID, *res.Send)
	}
}

// Handle memproses satu pesan. ok=false berarti tidak dibalas.
func (b *Bot) Handle(ctx context.Context, m *telegram.Message) (Reply, bool) {
	// Hanya chat pribadi: catatan tidak boleh muncul di grup.
	if m.From == nil || m.From.IsBot || m.Chat.Type != "private" {
		return Reply{}, false
	}
	userID, linked := b.users[m.From.ID]
	if !linked {
		// Akun lain hanya diberi tahu ID Telegram-nya sendiri, tanpa akses data apa pun.
		return Reply{Text: fmt.Sprintf("Akun Telegram ini belum terhubung ke Vaulty.\nID Telegram kamu: <code>%d</code>", m.From.ID)}, true
	}
	cmd, arg := parseCommand(m.Text)
	if cmd == "" {
		if strings.EqualFold(arg, "vault") || strings.EqualFold(arg, "menu") {
			b.setPending(m.From.ID, "")
			return menuReply, true
		}
		if mode := b.takePending(m.From.ID); mode != "" {
			return b.handleMode(ctx, m.From.ID, userID, mode, arg), true
		}
		return b.find(ctx, userID, arg), true
	}
	// Perintah "/..." membatalkan mode dari menu.
	b.setPending(m.From.ID, "")

	switch cmd {
	case "start", "help":
		return Reply{Text: helpText}, true
	case "menu":
		return menuReply, true
	case "create":
		return b.create(ctx, userID, model.CreateNoteInput{Body: arg}), true
	case "create_cmd", "create-cmd":
		desc, command, found := splitCommand(arg)
		if !found {
			return Reply{Text: "Format: <code>/create_cmd deskripsi | command</code>"}, true
		}
		return b.create(ctx, userID, model.CreateNoteInput{Type: model.NoteTypeCommand, Title: desc, Body: command}), true
	case "cari", "search":
		return b.find(ctx, userID, arg), true
	case "semua", "list":
		return b.list(ctx, userID, 0, 1), true
	default:
		return Reply{Text: "Perintah tidak dikenal. Kirim /help."}, true
	}
}

func (b *Bot) handleMode(ctx context.Context, tgID, userID int64, mode, text string) Reply {
	if mode == modeCommand {
		desc, command, found := splitCommand(text)
		if !found {
			b.setPending(tgID, modeCommand) // beri kesempatan mengirim ulang
			return Reply{Text: "Format: <code>deskripsi | command</code>\natau deskripsi di baris pertama, command di baris berikutnya.", Keyboard: cancelKeyboard}
		}
		return b.create(ctx, userID, model.CreateNoteInput{Type: model.NoteTypeCommand, Title: desc, Body: command})
	}
	return b.create(ctx, userID, model.CreateNoteInput{Body: text})
}

func (b *Bot) setPending(tgID int64, mode string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode == "" {
		delete(b.pending, tgID)
		return
	}
	b.pending[tgID] = pending{mode: mode, until: b.now().Add(pendingTTL)}
}

// takePending mengambil lalu menghapus mode yang masih berlaku.
func (b *Bot) takePending(tgID int64) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[tgID]
	delete(b.pending, tgID)
	if !ok || b.now().After(p.until) {
		return ""
	}
	return p.mode
}

// HandleCallback memproses tombol inline: pilihan menu dan hapus note (dengan konfirmasi).
func (b *Bot) HandleCallback(ctx context.Context, cq *telegram.CallbackQuery) CallbackResult {
	if cq.From == nil || cq.Message == nil || cq.Message.Chat.Type != "private" {
		return CallbackResult{}
	}
	userID, linked := b.users[cq.From.ID]
	if !linked {
		return CallbackResult{Toast: "Akun Telegram ini belum terhubung ke Vaulty."}
	}
	action, arg, _ := strings.Cut(cq.Data, ":")
	switch action {
	case "mode":
		switch arg {
		case modeNote:
			b.setPending(cq.From.ID, modeNote)
			return CallbackResult{Edit: &Reply{Text: "📝 Kirim isi catatannya.", Keyboard: cancelKeyboard}}
		case modeCommand:
			b.setPending(cq.From.ID, modeCommand)
			return CallbackResult{Edit: &Reply{Text: "⌨️ Kirim command-nya:\n<code>deskripsi | command</code>\natau deskripsi di baris pertama, command di baris berikutnya.", Keyboard: cancelKeyboard}}
		case "find":
			b.setPending(cq.From.ID, "")
			return CallbackResult{Edit: &Reply{Text: "🔎 Kirim kata kunci yang mau dicari."}}
		default:
			b.setPending(cq.From.ID, "")
			return CallbackResult{Edit: &Reply{Text: "Dibatalkan."}}
		}
	case "del", "delok":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return CallbackResult{}
		}
		if action == "del" {
			return b.confirmDelete(ctx, userID, id)
		}
		return b.delete(ctx, userID, id)
	case "list":
		// list:<cursor>:<halaman>
		c, p, _ := strings.Cut(arg, ":")
		cursor, err1 := strconv.ParseInt(c, 10, 64)
		page, err2 := strconv.Atoi(p)
		if err1 != nil || err2 != nil || page < 1 {
			return CallbackResult{}
		}
		r := b.list(ctx, userID, cursor, page)
		return CallbackResult{Edit: &r}
	case "open":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return CallbackResult{}
		}
		return b.open(ctx, userID, id)
	case "delno":
		return CallbackResult{Toast: "Batal", Edit: &Reply{Text: "Batal menghapus."}}
	}
	return CallbackResult{}
}

func (b *Bot) confirmDelete(ctx context.Context, userID, id int64) CallbackResult {
	note, err := b.notes.Get(ctx, userID, id)
	if errors.Is(err, model.ErrNotFound) {
		return CallbackResult{Toast: "Catatan sudah tidak ada."}
	}
	if err != nil {
		return CallbackResult{Toast: plainError(ctx, "ambil note via telegram", err)}
	}
	return CallbackResult{Send: &Reply{
		Text: "Hapus catatan ini?\n\n" + formatHit(0, note),
		Keyboard: telegram.Keyboard{{
			{Text: "🗑 Ya, hapus", CallbackData: fmt.Sprintf("delok:%d", id)},
			{Text: "✖️ Batal", CallbackData: "delno"},
		}},
	}}
}

func (b *Bot) delete(ctx context.Context, userID, id int64) CallbackResult {
	err := b.notes.Delete(ctx, userID, id)
	if errors.Is(err, model.ErrNotFound) {
		return CallbackResult{Toast: "Catatan sudah tidak ada.", Edit: &Reply{Text: "Catatan sudah tidak ada."}}
	}
	if err != nil {
		return CallbackResult{Toast: plainError(ctx, "hapus note via telegram", err)}
	}
	b.audit.Log(ctx, repository.AuditEntry{
		UserID: userID, Action: "note.delete", Entity: "note", EntityID: id,
		Metadata: map[string]any{"via": "telegram"},
	})
	return CallbackResult{Toast: "Terhapus", Edit: &Reply{Text: "🗑 Catatan dihapus."}}
}

// list menampilkan satu halaman catatan (terbaru dulu), satu baris per catatan.
// Tombol nomor membuka isi lengkap; navigasi memakai cursor keyset dari NoteService.List.
func (b *Bot) list(ctx context.Context, userID, cursor int64, page int) Reply {
	notes, next, err := b.notes.List(ctx, userID, model.NoteFilter{}, cursor, pageSize)
	if err != nil {
		return Reply{Text: userError(ctx, "list note via telegram", err)}
	}
	if len(notes) == 0 {
		if page == 1 {
			return Reply{Text: "Belum ada catatan. Ketik <b>vault</b> untuk mulai menyimpan."}
		}
		return Reply{Text: "Tidak ada catatan lagi.", Keyboard: telegram.Keyboard{{{Text: "⏮ Ke awal", CallbackData: "list:0:1"}}}}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📚 <b>Semua catatan</b> · halaman %d\n<i>Terbaru di atas. Tekan nomor untuk lihat isi.</i>", page)
	var nums []telegram.Button
	for i, n := range notes {
		no := (page-1)*pageSize + i + 1
		fmt.Fprintf(&sb, "\n\n<b>%d.</b> %s %s\n<i>🕒 %s</i>", no, icon(n.Type), html.EscapeString(preview(headline(n), maxListTitle)), formatTime(n.CreatedAt))
		nums = append(nums, telegram.Button{Text: strconv.Itoa(no), CallbackData: fmt.Sprintf("open:%d", n.ID)})
	}
	var kb telegram.Keyboard
	for len(nums) > 0 { // maksimal 5 tombol per baris
		k := min(5, len(nums))
		kb, nums = append(kb, nums[:k]), nums[k:]
	}
	var nav []telegram.Button
	if page > 1 {
		nav = append(nav, telegram.Button{Text: "⏮ Ke awal", CallbackData: "list:0:1"})
	}
	if next != nil {
		nav = append(nav, telegram.Button{Text: "Berikutnya ➡️", CallbackData: fmt.Sprintf("list:%d:%d", *next, page+1)})
	}
	if len(nav) > 0 {
		kb = append(kb, nav)
	}
	return Reply{Text: sb.String(), Keyboard: kb}
}

func (b *Bot) open(ctx context.Context, userID, id int64) CallbackResult {
	note, err := b.notes.Get(ctx, userID, id)
	if errors.Is(err, model.ErrNotFound) {
		return CallbackResult{Toast: "Catatan sudah tidak ada."}
	}
	if err != nil {
		return CallbackResult{Toast: plainError(ctx, "ambil note via telegram", err)}
	}
	return CallbackResult{Send: &Reply{Text: formatHit(0, note), Keyboard: telegram.Keyboard{{deleteButton("Hapus", id)}}}}
}

func deleteButton(label string, id int64) telegram.Button {
	return telegram.Button{Text: "🗑 " + label, CallbackData: fmt.Sprintf("del:%d", id)}
}

func (b *Bot) create(ctx context.Context, userID int64, in model.CreateNoteInput) Reply {
	if strings.TrimSpace(in.Body) == "" && strings.TrimSpace(in.Title) == "" {
		return Reply{Text: "Isi catatan kosong. Contoh: <code>/create beli domain vaulty.id</code>"}
	}
	res, err := b.notes.Create(ctx, userID, in)
	if err != nil {
		return Reply{Text: userError(ctx, "simpan note via telegram", err)}
	}
	b.audit.Log(ctx, repository.AuditEntry{
		UserID: userID, Action: "note.create", Entity: "note", EntityID: res.Note.ID,
		Metadata: map[string]any{"type": res.Note.Type, "via": "telegram"},
	})
	out := fmt.Sprintf("✅ Tersimpan sebagai <b>%s</b>. Tag otomatis menyusul.", html.EscapeString(res.Note.Type))
	for _, w := range res.Warnings {
		out += "\n⚠️ " + html.EscapeString(w)
	}
	return Reply{Text: out, Keyboard: telegram.Keyboard{{deleteButton("Hapus", res.Note.ID)}}}
}

func (b *Bot) find(ctx context.Context, userID int64, q string) Reply {
	if strings.TrimSpace(q) == "" {
		return Reply{Text: helpText}
	}
	hits, _, err := b.search.Search(ctx, userID, q, model.NoteFilter{}, searchLimit)
	if err != nil {
		return Reply{Text: userError(ctx, "search via telegram", err)}
	}
	if len(hits) == 0 {
		return Reply{Text: "Tidak ada catatan yang cocok."}
	}
	// Search mengurutkan menurut relevansi; di chat lebih mudah dibaca menurut urutan dibuat.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Note.CreatedAt.Before(hits[j].Note.CreatedAt) })
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔎 <b>%d catatan ditemukan</b>", len(hits))
	var buttons []telegram.Button
	for i, h := range hits {
		item := "\n\n" + formatHit(i+1, h.Note)
		if sb.Len()+len(item) > maxReply {
			sb.WriteString("\n\n<i>… hasil lain dipotong.</i>")
			break
		}
		sb.WriteString(item)
		buttons = append(buttons, deleteButton(strconv.Itoa(i+1), h.Note.ID))
	}
	return Reply{Text: sb.String(), Keyboard: telegram.Keyboard{buttons}}
}

var typeIcon = map[string]string{
	model.NoteTypeNote:     "📝",
	model.NoteTypeLink:     "🔗",
	model.NoteTypeCommand:  "⌨️",
	model.NoteTypeDocument: "📄",
}

// formatHit: baris judul (nomor + ikon + judul), isi, waktu dibuat, lalu tag.
// Note tanpa judul memakai isinya sebagai judul supaya tidak tampil "note [note]".
// n = 0 berarti tanpa nomor.
func icon(noteType string) string {
	if i, ok := typeIcon[noteType]; ok {
		return i
	}
	return "📌"
}

// headline: judul, atau URL, atau isi note bila tanpa judul.
func headline(n model.Note) string {
	if n.Title != nil && strings.TrimSpace(*n.Title) != "" {
		return *n.Title
	}
	if n.URL != nil && strings.TrimSpace(*n.URL) != "" {
		return *n.URL
	}
	return n.Body
}

func formatHit(n int, note model.Note) string {
	icon := icon(note.Type)
	if n > 0 {
		icon = fmt.Sprintf("<b>%d.</b> %s", n, icon)
	}
	var title string
	if note.Title != nil {
		title = strings.TrimSpace(*note.Title)
	}
	url := ""
	if note.URL != nil {
		url = strings.TrimSpace(*note.URL)
	}

	var sb strings.Builder
	switch {
	case note.Type == model.NoteTypeCommand:
		if title == "" {
			title = "command"
		}
		// ADR-010: command ditampilkan verbatim, tidak diringkas.
		body := note.Body
		if len(body) > maxReply/2 {
			body = body[:maxReply/2] + "\n…"
		}
		fmt.Fprintf(&sb, "%s <b>%s</b>\n<pre>%s</pre>", icon, html.EscapeString(title), html.EscapeString(body))
	case title != "":
		fmt.Fprintf(&sb, "%s <b>%s</b>", icon, html.EscapeString(title))
		if url != "" {
			sb.WriteString("\n" + html.EscapeString(url))
		} else if body := preview(note.Body, maxPreview); body != "" {
			sb.WriteString("\n" + html.EscapeString(body))
		}
	case url != "":
		fmt.Fprintf(&sb, "%s %s", icon, html.EscapeString(url))
	default:
		fmt.Fprintf(&sb, "%s %s", icon, html.EscapeString(preview(note.Body, maxPreview)))
	}

	if !note.CreatedAt.IsZero() {
		sb.WriteString("\n<i>🕒 " + formatTime(note.CreatedAt) + "</i>")
	}
	tags := append(append([]string{}, note.Tags...), note.AutoTags...)
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	if len(tags) > 0 {
		// Bukan #hashtag: Telegram memotong hashtag di tanda "-" (#docker-compose → #docker).
		sb.WriteString("\n<i>🏷 " + html.EscapeString(strings.Join(tags, " · ")) + "</i>")
	}
	return sb.String()
}

// wib dipakai tetap (UTC+7) karena image alpine tidak membawa tzdata.
var wib = time.FixedZone("WIB", 7*3600)

var bulan = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// formatTime: "16 Okt 2026, 21:05 WIB".
func formatTime(t time.Time) string {
	t = t.In(wib)
	return fmt.Sprintf("%d %s %d, %02d:%02d WIB", t.Day(), bulan[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

var spaces = regexp.MustCompile(`\s+`)

func preview(s string, max int) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// parseCommand memisahkan "/create@nama_bot isi" menjadi ("create", "isi").
// Teks tanpa "/" dianggap pencarian: cmd "".
func parseCommand(text string) (cmd, arg string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", text
	}
	head, rest, _ := strings.Cut(text, " ")
	if i := strings.IndexAny(head, "\n"); i >= 0 {
		head, rest = head[:i], head[i+1:]+" "+rest
	}
	head, _, _ = strings.Cut(strings.TrimPrefix(head, "/"), "@")
	return strings.ToLower(head), strings.TrimSpace(rest)
}

// splitCommand memisahkan "deskripsi | command" di "|" pertama, atau di baris baru pertama.
// Command boleh berisi "|" (pipe shell), jadi hanya pemisah pertama yang dipakai.
func splitCommand(arg string) (desc, command string, ok bool) {
	i := strings.IndexAny(arg, "|\n")
	if i < 0 {
		return "", "", false
	}
	desc, command = strings.TrimSpace(arg[:i]), strings.TrimSpace(arg[i+1:])
	return desc, command, desc != "" && command != ""
}

// userError: error domain ditampilkan apa adanya, error internal hanya ke log.
func userError(ctx context.Context, op string, err error) string {
	var domain bool
	for _, e := range []error{model.ErrEmptyNote, model.ErrBlockedType, model.ErrNotFound} {
		if errors.Is(err, e) {
			domain = true
		}
	}
	if domain {
		return "❌ " + html.EscapeString(err.Error())
	}
	slog.ErrorContext(ctx, op, "err", err)
	return "❌ Terjadi kesalahan di server. Coba lagi nanti."
}

// plainError seperti userError, untuk toast tombol yang tidak mendukung HTML.
func plainError(ctx context.Context, op string, err error) string {
	return html.UnescapeString(userError(ctx, op, err))
}
