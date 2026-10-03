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
	"strings"
	"time"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/telegram"
)

type NoteCreator interface {
	Create(ctx context.Context, userID int64, in model.CreateNoteInput) (model.NoteResult, error)
}

type Searcher interface {
	Search(ctx context.Context, userID int64, q string, f model.NoteFilter, limit int) ([]model.SearchHit, string, error)
}

type Auditor interface {
	Log(ctx context.Context, e repository.AuditEntry)
}

type TelegramAPI interface {
	GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]telegram.Update, error)
	SendMessage(ctx context.Context, chatID int64, text, parseMode string) error
}

type Bot struct {
	tg     TelegramAPI
	notes  NoteCreator
	search Searcher
	audit  Auditor
	// users memetakan ID Telegram ke user_id Vaulty. Hanya akun di sini yang dilayani.
	users map[int64]int64
}

func New(tg TelegramAPI, notes NoteCreator, search Searcher, audit Auditor, users map[int64]int64) *Bot {
	return &Bot{tg: tg, notes: notes, search: search, audit: audit, users: users}
}

const (
	searchLimit = 5
	// Batas pesan Telegram 4096 karakter; sisakan ruang untuk tag HTML.
	maxReply    = 3800
	maxPreview  = 200
	maxHeadline = 60
	maxTags     = 4
)

const helpText = `<b>Vaulty</b> — simpan &amp; cari catatan.

/create &lt;teks&gt; — simpan catatan (URL saja otomatis jadi link)
/create-cmd &lt;deskripsi&gt; | &lt;command&gt; — simpan command, ditampilkan apa adanya
/cari &lt;kata kunci&gt; — cari catatan (atau kirim teks biasa tanpa /)
/help — bantuan ini`

// Run melakukan long polling sampai ctx dibatalkan.
func (b *Bot) Run(ctx context.Context) error {
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
			if u.Message == nil {
				continue
			}
			text, mode, ok := b.Handle(ctx, u.Message)
			if !ok {
				continue
			}
			if err := b.tg.SendMessage(ctx, u.Message.Chat.ID, text, mode); err != nil {
				slog.WarnContext(ctx, "telegram sendMessage gagal", "update_id", u.UpdateID, "err", err)
			}
		}
	}
}

// Handle memproses satu pesan dan mengembalikan balasan (HTML). ok=false berarti tidak dibalas.
func (b *Bot) Handle(ctx context.Context, m *telegram.Message) (reply, parseMode string, ok bool) {
	// Hanya chat pribadi: catatan tidak boleh muncul di grup.
	if m.From == nil || m.From.IsBot || m.Chat.Type != "private" {
		return "", "", false
	}
	cmd, arg := parseCommand(m.Text)
	userID, linked := b.users[m.From.ID]
	if !linked {
		// Akun lain hanya diberi tahu ID Telegram-nya sendiri, tanpa akses data apa pun.
		return fmt.Sprintf("Akun Telegram ini belum terhubung ke Vaulty.\nID Telegram kamu: <code>%d</code>", m.From.ID), "HTML", true
	}

	switch cmd {
	case "start", "help":
		return helpText, "HTML", true
	case "create":
		return b.create(ctx, userID, model.CreateNoteInput{Body: arg}), "HTML", true
	case "create-cmd":
		desc, command, found := splitCommand(arg)
		if !found {
			return "Format: <code>/create-cmd deskripsi | command</code>", "HTML", true
		}
		return b.create(ctx, userID, model.CreateNoteInput{Type: model.NoteTypeCommand, Title: desc, Body: command}), "HTML", true
	case "cari", "search", "":
		return b.find(ctx, userID, arg), "HTML", true
	default:
		return "Perintah tidak dikenal. Kirim /help.", "HTML", true
	}
}

func (b *Bot) create(ctx context.Context, userID int64, in model.CreateNoteInput) string {
	if strings.TrimSpace(in.Body) == "" && strings.TrimSpace(in.Title) == "" {
		return "Isi catatan kosong. Contoh: <code>/create beli domain vaulty.id</code>"
	}
	res, err := b.notes.Create(ctx, userID, in)
	if err != nil {
		return userError(ctx, "simpan note via telegram", err)
	}
	b.audit.Log(ctx, repository.AuditEntry{
		UserID: userID, Action: "note.create", Entity: "note", EntityID: res.Note.ID,
		Metadata: map[string]any{"type": res.Note.Type, "via": "telegram"},
	})
	out := fmt.Sprintf("✅ Tersimpan sebagai <b>%s</b> (#%d). Tag otomatis menyusul.", html.EscapeString(res.Note.Type), res.Note.ID)
	for _, w := range res.Warnings {
		out += "\n⚠️ " + html.EscapeString(w)
	}
	return out
}

func (b *Bot) find(ctx context.Context, userID int64, q string) string {
	if strings.TrimSpace(q) == "" {
		return helpText
	}
	hits, _, err := b.search.Search(ctx, userID, q, model.NoteFilter{}, searchLimit)
	if err != nil {
		return userError(ctx, "search via telegram", err)
	}
	if len(hits) == 0 {
		return "Tidak ada catatan yang cocok."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔎 <b>%d catatan</b> untuk <i>%s</i>", len(hits), html.EscapeString(preview(q, maxHeadline)))
	for _, h := range hits {
		item := "\n\n" + formatHit(h.Note)
		if sb.Len()+len(item) > maxReply {
			sb.WriteString("\n\n<i>… hasil lain dipotong.</i>")
			break
		}
		sb.WriteString(item)
	}
	return sb.String()
}

var typeIcon = map[string]string{
	model.NoteTypeNote:     "📝",
	model.NoteTypeLink:     "🔗",
	model.NoteTypeCommand:  "⌨️",
	model.NoteTypeDocument: "📄",
}

// formatHit: baris judul (ikon + judul), isi, lalu tag di baris terpisah.
// Note tanpa judul memakai isinya sebagai judul supaya tidak tampil "note [note]".
func formatHit(note model.Note) string {
	icon, ok := typeIcon[note.Type]
	if !ok {
		icon = "📌"
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
