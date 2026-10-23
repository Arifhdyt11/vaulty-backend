package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/ai"
	"vaulty/pkg/media"
	"vaulty/pkg/storage"
)

// Siaran Vee (ADR-016): tiap hari (zona waktu user) beberapa simpanan dibacakan sebagai bab audio.
// Pemilihan bab sinkron saat GET pertama hari itu (hanya query DB); naskah (LLM) dan suara (TTS +
// ffmpeg) dibuat worker lewat task briefing:build, bab demi bab, supaya bab awal bisa diputar
// sebelum bab lain selesai.

const (
	dateLayout = "2006-01-02"
	// Item baru = dibuat dalam 24 jam terakhir dan belum pernah jadi bab.
	briefingFreshWindow = 24 * time.Hour
	// Item lama boleh tampil lagi bila tidak tampil di siaran 14 hari terakhir.
	briefingRepeatDays = 14
	// Siaran lebih tua dari ini dihapus housekeeping beserta audionya.
	briefingRetentionDays  = 30
	briefingCandidateLimit = 2000
	briefingQuoteMax       = 90
	briefingContentMax     = 1500
)

// BriefingQueue didefinisikan di sisi pemakai; implementasinya worker.Client.
type BriefingQueue interface {
	EnqueueBriefing(ctx context.Context, briefingID, userID int64, queue string) error
}

type BriefingService struct {
	repos     *repository.Repos
	items     *ItemService
	store     storage.Storage
	queue     BriefingQueue
	chat      ai.Chat
	chatModel string
	tts       ai.Speaker
	media     *media.Tool
	aiOn      bool
	now       func() time.Time
}

type BriefingDeps struct {
	Repos     *repository.Repos
	Items     *ItemService
	Store     storage.Storage
	Queue     BriefingQueue
	Chat      ai.Chat
	ChatModel string
	TTS       ai.Speaker
	Media     *media.Tool
	AIEnabled bool
}

func NewBriefingService(d BriefingDeps) *BriefingService {
	if d.Media == nil {
		d.Media = media.New(media.Options{})
	}
	return &BriefingService{repos: d.Repos, items: d.Items, store: d.Store, queue: d.Queue, chat: d.Chat, chatModel: d.ChatModel,
		tts: d.TTS, media: d.Media, aiOn: d.AIEnabled, now: time.Now}
}

var errBriefingExists = errors.New("siaran hari ini sudah ada")

// Today mengembalikan siaran hari ini; bila belum ada, memilih bab dan menjadwalkan pembuatannya.
func (s *BriefingService) Today(ctx context.Context, u model.User) (model.Briefing, error) {
	now := s.now().In(u.Location())
	date := now.Format(dateLayout)
	b, err := s.repos.Briefing.ByDate(ctx, u.ID, date)
	switch {
	case errors.Is(err, model.ErrNotFound):
		b, err = s.create(ctx, u, now, false)
	case err == nil && b.Status == "empty":
		// Simpanan baru mungkin sudah siap sejak siaran kosong dibuat: coba susun lagi.
		var chapters []repository.NewChapter
		if chapters, err = s.plan(ctx, u, now); err == nil && len(chapters) > 0 {
			b, err = s.insert(ctx, u, now, chapters, true)
		}
	}
	if err != nil {
		return model.Briefing{}, err
	}
	s.ensureBuilding(ctx, u, b)
	return s.present(ctx, u, b)
}

// Rebuild membuang siaran hari ini (beserta audionya) lalu menyusun ulang.
func (s *BriefingService) Rebuild(ctx context.Context, u model.User) (model.Briefing, error) {
	now := s.now().In(u.Location())
	b, err := s.create(ctx, u, now, true)
	if err != nil {
		return model.Briefing{}, err
	}
	s.ensureBuilding(ctx, u, b)
	return s.present(ctx, u, b)
}

func (s *BriefingService) create(ctx context.Context, u model.User, now time.Time, replace bool) (model.Briefing, error) {
	chapters, err := s.plan(ctx, u, now)
	if err != nil {
		return model.Briefing{}, err
	}
	return s.insert(ctx, u, now, chapters, replace)
}

// plan memilih item dan menyiapkan label/judul/kutipan bab (tanpa AI).
func (s *BriefingService) plan(ctx context.Context, u model.User, now time.Time) ([]repository.NewChapter, error) {
	cands, err := s.repos.Briefing.Candidates(ctx, u.ID, briefingCandidateLimit)
	if err != nil {
		return nil, fmt.Errorf("baca kandidat siaran: %w", err)
	}
	shown, err := s.repos.Briefing.ShownHistory(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("baca riwayat siaran: %w", err)
	}
	date := now.Format(dateLayout)
	// Siaran hari ini sendiri tidak dihitung sebagai riwayat (rebuild menyusun ulang dari awal).
	for id, d := range shown {
		if d >= date {
			delete(shown, id)
		}
	}
	ids := pickBriefingItems(cands, shown, now, date, RulesFor(u.Plan).BriefingChapters)
	if len(ids) == 0 {
		return nil, nil
	}
	items, err := s.repos.Items.ByIDs(ctx, u.ID, ids)
	if err != nil {
		return nil, fmt.Errorf("baca item siaran: %w", err)
	}
	out := []repository.NewChapter{}
	for _, id := range ids {
		it, ok := items[id]
		if !ok {
			continue
		}
		out = append(out, repository.NewChapter{Seq: len(out) + 1, ItemID: id, Label: briefingLabel(it), Title: briefingTitle(it), Quote: briefingQuote(it)})
	}
	return out, nil
}

func (s *BriefingService) insert(ctx context.Context, u model.User, now time.Time, chapters []repository.NewChapter, replace bool) (model.Briefing, error) {
	date := now.Format(dateLayout)
	status := "building"
	if len(chapters) == 0 {
		status = "empty"
	}
	var oldKeys []string
	err := s.repos.Tx(ctx, func(tx *repository.Repos) error {
		if replace {
			keys, err := tx.Briefing.DeleteByDate(ctx, u.ID, date)
			if err != nil {
				return err
			}
			oldKeys = keys
		}
		id, created, err := tx.Briefing.Insert(ctx, u.ID, date, status, briefingGreeting(now, u.Name))
		if err != nil {
			return err
		}
		if !created {
			return errBriefingExists
		}
		for _, c := range chapters {
			if err := tx.Briefing.InsertChapter(ctx, u.ID, id, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errBriefingExists) {
		return model.Briefing{}, err
	}
	for _, k := range oldKeys {
		if err := s.store.Delete(ctx, k); err != nil {
			slog.WarnContext(ctx, "hapus audio siaran lama gagal", "key", k, "err", err)
		}
	}
	return s.repos.Briefing.ByDate(ctx, u.ID, date)
}

// ensureBuilding menjadwalkan (ulang) build selama siaran masih dibuat. TaskID deterministik membuat
// panggilan berulang (polling tiap 3 detik) aman: task yang masih antre/berjalan tidak digandakan.
func (s *BriefingService) ensureBuilding(ctx context.Context, u model.User, b model.Briefing) {
	if b.Status != "building" || s.queue == nil {
		return
	}
	if err := s.queue.EnqueueBriefing(ctx, b.ID, u.ID, RulesFor(u.Plan).QueuePriority); err != nil {
		slog.WarnContext(ctx, "jadwalkan siaran gagal", "briefing_id", b.ID, "err", err)
	}
}

// present mengisi kartu item, URL audio bertanda tangan, dan total durasi. Bab yang itemnya
// sudah dihapus disaring keluar.
func (s *BriefingService) present(ctx context.Context, u model.User, b model.Briefing) (model.Briefing, error) {
	ids := make([]int64, 0, len(b.Chapters))
	for _, c := range b.Chapters {
		ids = append(ids, c.ItemID)
	}
	items, err := s.repos.Items.ByIDs(ctx, u.ID, ids)
	if err != nil {
		return model.Briefing{}, err
	}
	out := make([]model.BriefingChapter, 0, len(b.Chapters))
	total := 0
	for _, c := range b.Chapters {
		it, ok := items[c.ItemID]
		if !ok {
			continue
		}
		c.Index = len(out) + 1
		c.Item = s.items.Card(ctx, it)
		if c.Status == "ready" && c.AudioKey != nil && *c.AudioKey != "" {
			if url, err := s.store.URL(ctx, *c.AudioKey, signedURLTTL); err == nil {
				c.AudioURL = &url
			}
			total += c.DurationSec
		}
		out = append(out, c)
	}
	b.Chapters = out
	b.TotalDurationSec = total
	return b, nil
}

// ---------- worker ----------

// Build membuat naskah dan suara untuk bab yang masih pending, berurutan. Idempoten: bab yang sudah
// ready/failed dilewati dan naskah yang sudah tersimpan tidak dibuat ulang.
func (s *BriefingService) Build(ctx context.Context, userID, briefingID int64) error {
	b, err := s.repos.Briefing.ByID(ctx, userID, briefingID)
	if errors.Is(err, model.ErrNotFound) {
		return nil // siaran disusun ulang atau akun dihapus
	}
	if err != nil {
		return err
	}
	if b.Status != "building" {
		return nil
	}
	u, err := s.repos.Users.ByID(ctx, userID)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(b.Chapters))
	for _, c := range b.Chapters {
		ids = append(ids, c.ItemID)
	}
	items, err := s.repos.Items.ByIDs(ctx, userID, ids)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "vaulty-briefing-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	loc := u.Location()
	for i, ch := range b.Chapters {
		if ch.Status != "pending" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		it, ok := items[ch.ItemID]
		if !ok {
			_ = s.repos.Briefing.SetChapterFailed(ctx, userID, ch.ID, "item sudah dihapus")
			continue
		}
		start := time.Now()
		script, viaLLM := ch.Script, false
		if script == "" {
			opening := ""
			if i == 0 {
				opening = briefingOpening(b.Greeting)
			}
			script, viaLLM = s.writeScript(ctx, it, opening, relativeSaved(it.CreatedAt, s.now(), loc))
			if err := s.repos.Briefing.SetChapterScript(ctx, userID, ch.ID, script); err != nil {
				return fmt.Errorf("simpan naskah bab: %w", err)
			}
		}
		dur, mime, err := s.voice(ctx, userID, b, ch, script, dir)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.WarnContext(ctx, "suara bab siaran gagal", "briefing_id", b.ID, "seq", ch.Seq, "err", err)
			_ = s.repos.Briefing.SetChapterFailed(ctx, userID, ch.ID, ai.Truncate(err.Error(), 300))
			continue
		}
		slog.InfoContext(ctx, "bab siaran siap", "briefing_id", b.ID, "seq", ch.Seq, "item_id", it.ID, "llm", viaLLM,
			"kata", len(strings.Fields(script)), "detik", dur, "tts_mime", mime, "ms", time.Since(start).Milliseconds())
	}
	return s.finalize(ctx, userID, briefingID)
}

func (s *BriefingService) finalize(ctx context.Context, userID, briefingID int64) error {
	b, err := s.repos.Briefing.ByID(ctx, userID, briefingID)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ready, pending := 0, 0
	for _, c := range b.Chapters {
		switch c.Status {
		case "ready":
			ready++
		case "pending":
			pending++
		}
	}
	if pending > 0 {
		return fmt.Errorf("siaran %d: %d bab masih pending", briefingID, pending)
	}
	status := "ready"
	if ready == 0 {
		status = "failed"
	}
	return s.repos.Briefing.SetStatus(ctx, userID, briefingID, status)
}

// writeScript membuat naskah lewat LLM (3 percobaan, Hermes kadang menjawab kosong saat sibuk).
// Bila LLM tidak tersedia, naskah sederhana disusun dari ringkasan supaya siaran tetap jalan.
func (s *BriefingService) writeScript(ctx context.Context, it model.Item, opening, saved string) (string, bool) {
	if s.aiOn && s.chat != nil {
		msg := briefingScriptInput(it, opening, saved)
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return fallbackScript(it, opening, saved), false
				case <-time.After(time.Duration(attempt*2) * time.Second):
				}
			}
			resp, err := s.chat.Complete(ctx, ai.ChatRequest{Model: s.chatModel, System: briefingScriptPrompt, MaxTokens: 600, Temperature: 0.7,
				Messages: []ai.Message{{Role: "user", Content: msg}}})
			if err != nil {
				slog.WarnContext(ctx, "naskah siaran: llm gagal", "item_id", it.ID, "attempt", attempt+1, "err", err)
				continue
			}
			if sc := withOpening(cleanScript(resp.Content), opening); len(strings.Fields(sc)) >= 20 {
				return sc, true
			}
		}
	}
	return fallbackScript(it, opening, saved), false
}

// voice: TTS → AAC mono 64 kbps → storage. Mengembalikan durasi (detik) dan MIME asli TTS
// (audio/wav = model utama Gemini, audio/mp3 = cadangan) untuk log.
func (s *BriefingService) voice(ctx context.Context, userID int64, b model.Briefing, ch model.BriefingChapter, script, dir string) (int, string, error) {
	if s.tts == nil {
		return 0, "", ai.ErrDisabled
	}
	if !s.media.CanTranscode() {
		return 0, "", fmt.Errorf("ffmpeg/ffprobe belum terpasang di server")
	}
	data, mime, err := s.tts.Speak(ctx, script, "")
	if err != nil {
		return 0, "", fmt.Errorf("suara: %w", err)
	}
	in := filepath.Join(dir, fmt.Sprintf("tts-%d%s", ch.Seq, audioExt(mime)))
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return 0, "", err
	}
	out := filepath.Join(dir, fmt.Sprintf("bab-%d.m4a", ch.Seq))
	if err := s.media.ToAAC(ctx, in, out); err != nil {
		return 0, "", err
	}
	secs, err := s.media.Duration(ctx, out)
	if err != nil {
		return 0, "", fmt.Errorf("baca durasi: %w", err)
	}
	dur := max(1, int(math.Round(secs)))
	f, err := os.Open(out)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	// ID siaran ikut di nama file: siaran yang disusun ulang di hari yang sama tidak menimpa audio lama
	// yang mungkin masih ditulis task sebelumnya.
	key := fmt.Sprintf("u/%d/briefing/%s/%d-%d.m4a", userID, b.Date, b.ID, ch.Seq)
	if err := s.store.Put(ctx, key, f, st.Size(), "audio/mp4"); err != nil {
		return 0, "", fmt.Errorf("simpan audio: %w", err)
	}
	ok, err := s.repos.Briefing.SetChapterAudio(ctx, userID, ch.ID, key, dur)
	if err != nil {
		return 0, "", fmt.Errorf("simpan bab: %w", err)
	}
	if !ok {
		_ = s.store.Delete(ctx, key) // siaran sudah disusun ulang; audio ini yatim
	}
	return dur, mime, nil
}

// Prune menghapus siaran lebih tua dari 30 hari beserta audionya (housekeeping worker).
func (s *BriefingService) Prune(ctx context.Context) (int, error) {
	before := s.now().AddDate(0, 0, -briefingRetentionDays).Format(dateLayout)
	keys, err := s.repos.Briefing.Prune(ctx, before)
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		if err := s.store.Delete(ctx, k); err != nil {
			slog.WarnContext(ctx, "hapus audio siaran gagal", "key", k, "err", err)
		}
	}
	return len(keys), nil
}

// ---------- logika murni (dites tanpa DB) ----------

// pickBriefingItems memilih maksimal n item: dulu item baru (≤24 jam, belum pernah jadi bab, terbaru
// dulu), lalu item lama ber-ringkasan yang tidak tampil 14 hari terakhir dengan urutan acak yang
// deterministik per tanggal (hash item_id+tanggal), supaya GET berulang di hari yang sama stabil.
func pickBriefingItems(cands []repository.BriefingCandidate, lastShown map[int64]string, now time.Time, date string, n int) []int64 {
	if n <= 0 {
		return nil
	}
	d, err := time.Parse(dateLayout, date)
	if err != nil {
		return nil
	}
	cutoff := d.AddDate(0, 0, -briefingRepeatDays).Format(dateLayout)
	var fresh, old []repository.BriefingCandidate
	for _, c := range cands {
		last, shown := lastShown[c.ID]
		switch {
		case !shown && now.Sub(c.CreatedAt) <= briefingFreshWindow:
			fresh = append(fresh, c)
		case c.HasSummary && (!shown || last <= cutoff):
			old = append(old, c)
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		if !fresh[i].CreatedAt.Equal(fresh[j].CreatedAt) {
			return fresh[i].CreatedAt.After(fresh[j].CreatedAt)
		}
		return fresh[i].ID > fresh[j].ID
	})
	sort.SliceStable(old, func(i, j int) bool {
		hi, hj := dailyHash(old[i].ID, date), dailyHash(old[j].ID, date)
		if hi != hj {
			return hi < hj
		}
		return old[i].ID < old[j].ID
	})
	out := make([]int64, 0, n)
	for _, list := range [][]repository.BriefingCandidate{fresh, old} {
		for _, c := range list {
			if len(out) >= n {
				return out
			}
			out = append(out, c.ID)
		}
	}
	return out
}

func dailyHash(id int64, date string) uint64 {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d|%s", id, date)
	// FNV saja bercampur buruk di byte terakhir (tanggal yang beda satu digit nyaris memberi urutan
	// sama); finalizer splitmix64 meratakannya.
	z := h.Sum64()
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

var platformLabels = map[string]string{
	"youtube": "YouTube", "tiktok": "TikTok", "instagram": "Instagram", "threads": "Threads", "web": "Web", "x": "X", "twitter": "X",
}

// briefingLabel: tag pertama (user, lalu auto) dalam huruf kalimat, atau label platform/jenis.
func briefingLabel(it model.Item) string {
	for _, list := range [][]string{it.Tags, it.AutoTags} {
		for _, t := range list {
			if l, ok := platformLabels[strings.ToLower(strings.TrimSpace(t))]; ok {
				return l // tag "youtube" → "YouTube", bukan "Youtube"
			}
			if l := sentenceCase(t); l != "" {
				return l
			}
		}
	}
	if it.Kind == "url" {
		if it.Platform != nil {
			if l, ok := platformLabels[*it.Platform]; ok {
				return l
			}
			if l := sentenceCase(*it.Platform); l != "" {
				return l
			}
		}
		return "Web"
	}
	switch it.Kind {
	case "image":
		return "Gambar"
	case "audio":
		return "Suara"
	case "file":
		return "Dokumen"
	}
	return "Catatan"
}

// sentenceCase: "desain-ui" → "Desain ui".
func sentenceCase(tag string) string {
	t := strings.Join(strings.Fields(strings.NewReplacer("-", " ", "_", " ", "#", " ").Replace(tag)), " ")
	if t == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(t)
	return string(unicode.ToUpper(r)) + strings.ToLower(t[size:])
}

func briefingTitle(it model.Item) string {
	if it.Title != nil && strings.TrimSpace(*it.Title) != "" {
		return strings.TrimSpace(*it.Title)
	}
	if it.FileName != nil && *it.FileName != "" {
		return *it.FileName
	}
	if b := strings.TrimSpace(it.Body); b != "" {
		return clipWords(firstLine(b), 80)
	}
	return "Tanpa judul"
}

// briefingQuote: poin penting pertama yang ≤ 90 karakter; bila tidak ada, potongan kalimat pertama ringkasan.
func briefingQuote(it model.Item) string {
	for _, kp := range it.KeyPoints {
		kp = trimSentence(kp)
		if kp != "" && utf8.RuneCountInString(kp) <= briefingQuoteMax {
			return kp
		}
	}
	src := ""
	switch {
	case it.Summary != nil && strings.TrimSpace(*it.Summary) != "":
		src = *it.Summary
	case len(it.KeyPoints) > 0:
		src = it.KeyPoints[0]
	case strings.TrimSpace(it.Body) != "":
		src = it.Body
	case it.ContentText != nil:
		src = *it.ContentText
	}
	return clipWords(trimSentence(firstSentence(src)), briefingQuoteMax)
}

func trimSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, ".;: ")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for i, r := range s {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(s) || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// clipWords memotong di batas kata supaya ≤ max rune (termasuk "…").
func clipWords(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max-1]
	cut := string(r)
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

// briefingGreeting: salam sesuai jam lokal user + nama depan.
func briefingGreeting(now time.Time, name string) string {
	h := now.Hour()
	part := "malam"
	switch {
	case h >= 4 && h < 11:
		part = "pagi"
	case h >= 11 && h < 15:
		part = "siang"
	case h >= 15 && h < 18:
		part = "sore"
	}
	g := "Selamat " + part
	if f := strings.Fields(name); len(f) > 0 {
		g += ", " + f[0]
	}
	return g
}

func briefingOpening(greeting string) string {
	if greeting == "" {
		greeting = "Halo"
	}
	return greeting + "! Ini siaran Vee hari ini."
}

var angka = []string{"nol", "satu", "dua", "tiga", "empat", "lima", "enam", "tujuh", "delapan", "sembilan", "sepuluh", "sebelas", "dua belas"}

func angkaKata(n int) string {
	if n >= 0 && n < len(angka) {
		return angka[n]
	}
	return fmt.Sprint(n)
}

// relativeSaved: kapan item disimpan relatif terhadap hari ini (tanggal lokal user), dalam kata.
func relativeSaved(created, now time.Time, loc *time.Location) string {
	c, n := created.In(loc), now.In(loc)
	cd := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC)
	nd := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	days := int(nd.Sub(cd).Hours() / 24)
	switch {
	case days <= 0:
		return "hari ini"
	case days == 1:
		return "kemarin"
	case days == 2:
		return "kemarin lusa"
	case days < 7:
		return angkaKata(days) + " hari lalu"
	case days < 14:
		return "seminggu lalu"
	case days < 28:
		return angkaKata(days/7) + " minggu lalu"
	case days < 60:
		return "sebulan lalu"
	case days < 365:
		return angkaKata(days/30) + " bulan lalu"
	case days < 730:
		return "setahun lalu"
	}
	return angkaKata(days/365) + " tahun lalu"
}

// kindPhrase: jenis simpanan dalam bahasa lisan, mis. "video TikTok".
func kindPhrase(it model.Item) string {
	switch it.Kind {
	case "text":
		return "catatan yang kamu tulis sendiri"
	case "image":
		return "gambar"
	case "audio":
		return "rekaman suara"
	case "file":
		return "dokumen"
	}
	p := ""
	if it.Platform != nil {
		p = *it.Platform
	}
	switch p {
	case "youtube":
		return "video YouTube"
	case "tiktok":
		return "video TikTok"
	case "instagram":
		return "post Instagram"
	case "threads":
		return "post Threads"
	case "x", "twitter":
		return "post X"
	}
	return "artikel dari web"
}

// briefingScriptInput menyusun bahan naskah untuk LLM (tanpa data user lain, tanpa URL).
func briefingScriptInput(it model.Item, opening, saved string) string {
	var sb strings.Builder
	if opening != "" {
		fmt.Fprintf(&sb, "PEMBUKA: %s\n", opening)
	}
	fmt.Fprintf(&sb, "DISIMPAN: %s\n", saved)
	fmt.Fprintf(&sb, "JENIS: %s\n", kindPhrase(it))
	fmt.Fprintf(&sb, "JUDUL: %s\n", briefingTitle(it))
	if n := strings.TrimSpace(it.UserNote); n != "" {
		fmt.Fprintf(&sb, "ALASAN USER: %s\n", ai.Truncate(n, 500))
	}
	if it.Summary != nil && strings.TrimSpace(*it.Summary) != "" {
		fmt.Fprintf(&sb, "RINGKASAN: %s\n", strings.TrimSpace(*it.Summary))
	}
	if len(it.KeyPoints) > 0 {
		sb.WriteString("POIN PENTING:\n")
		for i, kp := range it.KeyPoints {
			if i >= 6 {
				break
			}
			fmt.Fprintf(&sb, "- %s\n", strings.TrimSpace(kp))
		}
	}
	content := ""
	if it.ContentText != nil {
		content = strings.TrimSpace(*it.ContentText)
	}
	if content == "" {
		content = strings.TrimSpace(it.Body)
	}
	if content != "" {
		fmt.Fprintf(&sb, "AWAL ISI:\n%s\n", ai.Truncate(content, briefingContentMax))
	}
	return sb.String()
}

// cleanScript membuang sisa format tulisan (markdown, daftar, emoji) supaya enak dibacakan TTS.
func cleanScript(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		l = strings.TrimLeft(l, "#>-•*")
		l = strings.TrimSpace(l)
		// "1. " / "2) " di awal baris
		if len(l) > 2 && l[0] >= '0' && l[0] <= '9' && (l[1] == '.' || l[1] == ')') && l[2] == ' ' {
			l = strings.TrimSpace(l[2:])
		}
		if low := strings.ToLower(l); strings.HasPrefix(low, "naskah:") {
			l = strings.TrimSpace(l[len("naskah:"):])
		}
		if l != "" {
			parts = append(parts, l)
		}
	}
	out := strings.Join(parts, " ")
	out = strings.NewReplacer("**", "", "__", "", "*", "", "`", "", "#", "", "_", " ").Replace(out)
	out = strings.Map(func(r rune) rune {
		if isEmoji(r) {
			return -1
		}
		return r
	}, out)
	out = strings.Join(strings.Fields(out), " ")
	return strings.Trim(out, "\"“”' ")
}

func isEmoji(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || r == 0xFE0F || r == 0x200D || (r >= 0x1F1E6 && r <= 0x1F1FF)
}

// withOpening memastikan bab pertama diawali salam (model kadang melewatkannya).
func withOpening(script, opening string) string {
	if opening == "" || script == "" {
		return script
	}
	// Cukup dua kata pertama ("Selamat pagi") yang sama; model kadang mengubah tanda baca salam.
	head := func(s string) string {
		f := strings.Fields(strings.ToLower(s))
		for i := range f {
			f[i] = strings.Trim(f[i], ",.!?")
		}
		return strings.Join(f[:min(2, len(f))], " ")
	}
	if head(script) == head(opening) {
		return script
	}
	return opening + " " + script
}

// fallbackScript: naskah tanpa LLM dari judul, ringkasan, dan catatan user.
func fallbackScript(it model.Item, opening, saved string) string {
	var sb strings.Builder
	if opening != "" {
		sb.WriteString(opening + " ")
	}
	w := saved
	if r, size := utf8.DecodeRuneInString(w); size > 0 {
		w = string(unicode.ToUpper(r)) + w[size:]
	}
	fmt.Fprintf(&sb, "%s kamu menyimpan %s berjudul %s. ", w, kindPhrase(it), strings.TrimRight(briefingTitle(it), ". "))
	if n := strings.TrimSpace(it.UserNote); n != "" {
		fmt.Fprintf(&sb, "Kamu mencatat: %s. ", strings.TrimRight(ai.Truncate(n, 160), ". "))
	}
	if it.Summary != nil && strings.TrimSpace(*it.Summary) != "" {
		sb.WriteString(ai.Truncate(strings.TrimSpace(*it.Summary), 400) + " ")
	} else if q := briefingQuote(it); q != "" {
		sb.WriteString(q + ". ")
	}
	sb.WriteString("Coba buka lagi kalau sempat, siapa tahu berguna hari ini.")
	return cleanScript(sb.String())
}

func audioExt(mime string) string {
	switch strings.ToLower(mime) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/aac":
		return ".aac"
	case "audio/flac":
		return ".flac"
	}
	return ".bin"
}
