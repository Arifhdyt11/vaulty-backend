package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/ai"
	"vaulty/pkg/extract"
	"vaulty/pkg/fetch"
	"vaulty/pkg/storage"
)

// IngestService menjalankan pipeline fetch → transcribe → understand → index untuk satu item
// (dipanggil worker). Setiap tahap dicatat di items.ingest dan hanya berjalan bila masih pending
// untuk content_version yang sama (ADR-006).
type IngestService struct {
	repos         *repository.Repos
	store         storage.Storage
	fetcher       *fetch.Fetcher
	chat          ai.Chat
	embedder      ai.Embedder
	stt           ai.Transcriber
	visionModel   string
	chatModel     string
	remind        *ReminderService
	ent           *EntitlementService
	aiOn          bool
	fetchCacheTTL time.Duration
}

type IngestDeps struct {
	Repos       *repository.Repos
	Store       storage.Storage
	Fetcher     *fetch.Fetcher
	Chat        ai.Chat
	Embedder    ai.Embedder
	STT         ai.Transcriber
	VisionModel string
	ChatModel   string
	Reminders   *ReminderService
	Entitlement *EntitlementService
	AIEnabled   bool
}

func NewIngestService(d IngestDeps) *IngestService {
	return &IngestService{repos: d.Repos, store: d.Store, fetcher: d.Fetcher, chat: d.Chat, embedder: d.Embedder, stt: d.STT,
		visionModel: d.VisionModel, chatModel: d.ChatModel, remind: d.Reminders, ent: d.Entitlement, aiOn: d.AIEnabled, fetchCacheTTL: 7 * 24 * time.Hour}
}

// Run menjalankan semua tahap yang masih pending. Idempoten: aman dipanggil ulang.
func (s *IngestService) Run(ctx context.Context, userID, itemID int64, version int32) error {
	it, deleted, err := s.repos.Items.ByIDAnyState(ctx, userID, itemID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil // item sudah hilang, tidak perlu retry
		}
		return err
	}
	if deleted || it.ContentVersion != version {
		return nil // versi lama atau item dihapus: abaikan
	}
	u, err := s.repos.Users.ByID(ctx, userID)
	if err != nil {
		return nil
	}
	log := slog.With("item_id", itemID, "version", version, "kind", it.Kind)

	_ = s.repos.Items.SetStage(ctx, userID, itemID, version, "", model.Stage{}, "processing")

	// 1. fetch
	if it.Ingest.Fetch.Status == "pending" {
		s.setStage(ctx, &it, "fetch", model.Stage{Status: "running"})
		st := s.stageFetch(ctx, u, &it)
		s.setStage(ctx, &it, "fetch", st)
		log.InfoContext(ctx, "ingest fetch", "status", st.Status)
	}
	// 2. transcribe (STT / vision / ekstraksi file)
	if it.Ingest.Transcribe.Status == "pending" {
		s.setStage(ctx, &it, "transcribe", model.Stage{Status: "running"})
		st := s.stageTranscribe(ctx, u, &it)
		s.setStage(ctx, &it, "transcribe", st)
		log.InfoContext(ctx, "ingest transcribe", "status", st.Status)
	}
	// 3. understand
	if it.Ingest.Understand.Status == "pending" {
		s.setStage(ctx, &it, "understand", model.Stage{Status: "running"})
		st := s.stageUnderstand(ctx, u, &it)
		s.setStage(ctx, &it, "understand", st)
		log.InfoContext(ctx, "ingest understand", "status", st.Status)
	}
	// 4. index
	if it.Ingest.Index.Status == "pending" {
		s.setStage(ctx, &it, "index", model.Stage{Status: "running"})
		st := s.stageIndex(ctx, u, &it)
		s.setStage(ctx, &it, "index", st)
		log.InfoContext(ctx, "ingest index", "status", st.Status)
	}

	return s.repos.Items.SetStage(ctx, userID, itemID, version, "index", it.Ingest.Index, finalStatus(it))
}

func (s *IngestService) setStage(ctx context.Context, it *model.Item, stage string, st model.Stage) {
	switch stage {
	case "fetch":
		it.Ingest.Fetch = st
	case "transcribe":
		it.Ingest.Transcribe = st
	case "understand":
		it.Ingest.Understand = st
	case "index":
		it.Ingest.Index = st
	}
	if st.Status == "failed" {
		slog.WarnContext(ctx, "tahap ingest gagal", "item_id", it.ID, "stage", stage, "err", st.Error)
	}
	if err := s.repos.Items.SetStage(ctx, it.UserID, it.ID, it.ContentVersion, stage, st, ""); err != nil {
		slog.WarnContext(ctx, "tulis stage gagal", "item_id", it.ID, "stage", stage, "err", err)
	}
}

func finalStatus(it model.Item) string {
	failed := 0
	done := 0
	for _, st := range []model.Stage{it.Ingest.Fetch, it.Ingest.Transcribe, it.Ingest.Understand, it.Ingest.Index} {
		switch st.Status {
		case "failed":
			failed++
		case "done":
			done++
		}
	}
	switch {
	case failed == 0:
		return "ready"
	case done == 0 && it.Kind == "url" && it.Ingest.Fetch.Status == "failed":
		return "failed"
	default:
		return "partial"
	}
}

// ---------- fetch ----------

func (s *IngestService) stageFetch(ctx context.Context, u model.User, it *model.Item) model.Stage {
	if it.Kind != "url" || it.SourceURL == nil {
		return model.Stage{Status: "skipped"}
	}
	url := *it.SourceURL
	if it.CanonicalURL != nil && *it.CanonicalURL != "" {
		url = *it.CanonicalURL
	}
	var res fetch.Result
	hit, _ := s.repos.Cache.Get(ctx, url, s.fetchCacheTTL, &res)
	if !hit {
		var err error
		res, err = s.fetcher.Fetch(ctx, url)
		if err != nil {
			return model.Stage{Status: "failed", Error: ai.Truncate(err.Error(), 300)}
		}
		_ = s.repos.Cache.Put(ctx, url, res)
	}
	p := repository.FetchResultPatch{DurationSec: res.Duration, PublishedAt: res.PublishedAt}
	setIf(&p.CanonicalURL, res.CanonicalURL)
	setIf(&p.Platform, res.Platform)
	setIf(&p.ContentType, res.ContentType)
	setIf(&p.Title, res.Title)
	setIf(&p.Description, res.Description)
	setIf(&p.Language, res.Language)
	text := strings.TrimSpace(res.Transcript)
	if text == "" {
		text = strings.TrimSpace(res.Text)
	} else if res.Text != "" && len(res.Text) < 2000 {
		text = res.Text + "\n\n" + text // caption + transkrip
	}
	if text != "" {
		text = extract.Clean(text)
		p.ContentText = &text
	}
	if res.ThumbnailURL != "" {
		if key, err := s.copyThumbnail(ctx, u.ID, it.ID, res.ThumbnailURL); err == nil {
			p.ThumbnailKey = &key
		} else {
			slog.WarnContext(ctx, "salin thumbnail gagal", "item_id", it.ID, "err", err)
		}
	}
	if err := s.repos.Items.ApplyFetch(ctx, u.ID, it.ID, it.ContentVersion, p); err != nil {
		return model.Stage{Status: "failed", Error: "simpan hasil fetch: " + ai.Truncate(err.Error(), 200)}
	}
	// refresh item lokal untuk tahap berikutnya
	if fresh, _, err := s.repos.Items.ByIDAnyState(ctx, u.ID, it.ID); err == nil {
		keep := it.Ingest
		*it = fresh
		it.Ingest = keep
	}
	nothing := (it.Title == nil || *it.Title == "") && it.ContentText == nil && it.ThumbnailKey == nil
	if nothing {
		msg := "tidak ada metadata yang bisa diambil"
		if len(res.Warnings) > 0 {
			msg = ai.Truncate(strings.Join(res.Warnings, "; "), 300)
		}
		return model.Stage{Status: "failed", Error: msg}
	}
	if len(res.Warnings) > 0 {
		return model.Stage{Status: "done", Error: ai.Truncate(strings.Join(res.Warnings, "; "), 300)}
	}
	return model.Stage{Status: "done"}
}

func (s *IngestService) copyThumbnail(ctx context.Context, userID, itemID int64, src string) (string, error) {
	data, ct, err := s.fetcher.Download(ctx, src, 5<<20)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	ext := ".jpg"
	switch ct {
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	case "image/gif":
		ext = ".gif"
	}
	key := fmt.Sprintf("u/%d/thumb/%d-%s%s", userID, itemID, uuid.NewString()[:8], ext)
	if err := s.store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return "", err
	}
	return key, nil
}

func setIf(dst **string, v string) {
	if v = strings.TrimSpace(v); v != "" {
		*dst = &v
	}
}

// ---------- transcribe ----------

func (s *IngestService) stageTranscribe(ctx context.Context, u model.User, it *model.Item) model.Stage {
	switch it.Kind {
	case "image":
		return s.vision(ctx, u, it, *it.FileKey, false)
	case "file":
		return s.extractFile(ctx, u, it)
	case "audio":
		return s.transcribeAudio(ctx, u, it)
	case "url":
		// Screenshot pendamping (fallback sosmed): OCR lalu gabung dengan konten.
		if it.AttachmentKey != nil && *it.AttachmentKey != "" {
			return s.vision(ctx, u, it, *it.AttachmentKey, true)
		}
		if it.ContentText == nil || strings.TrimSpace(*it.ContentText) == "" {
			if it.ContentType != nil && *it.ContentType == "video" {
				return model.Stage{Status: "skipped", Error: "transkrip video tanpa caption belum didukung; tambahkan screenshot atau catatan"}
			}
		}
		return model.Stage{Status: "skipped"}
	}
	return model.Stage{Status: "skipped"}
}

func (s *IngestService) vision(ctx context.Context, u model.User, it *model.Item, key string, appendMode bool) model.Stage {
	if !s.aiOn {
		return model.Stage{Status: "skipped", Error: "AI nonaktif"}
	}
	rc, ct, err := s.store.Get(ctx, key)
	if err != nil {
		return model.Stage{Status: "failed", Error: "baca gambar: " + err.Error()}
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 20<<20))
	if err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	resp, err := s.chat.Complete(ctx, ai.ChatRequest{Model: s.visionModel, System: visionPrompt, JSON: true, MaxTokens: 1500,
		Messages: []ai.Message{{Role: "user", Content: "Baca gambar ini.", Images: []ai.Image{{MIME: ct, Data: data}}}}})
	if err != nil {
		return model.Stage{Status: "failed", Error: "vision: " + ai.Truncate(err.Error(), 200)}
	}
	var out struct {
		Text        string `json:"text"`
		Description string `json:"description"`
	}
	if err := ai.ParseJSONLoose(resp.Content, &out); err != nil {
		out.Text = resp.Content
	}
	text := extract.Clean(strings.TrimSpace(out.Description + "\n\n" + out.Text))
	if appendMode && it.ContentText != nil && *it.ContentText != "" {
		text = *it.ContentText + "\n\n[Dari screenshot]\n" + text
	}
	var title *string
	if out.Description != "" {
		t := ai.Truncate(strings.Split(out.Description, ".")[0], 80)
		title = &t
	}
	if err := s.repos.Items.SetContentText(ctx, u.ID, it.ID, it.ContentVersion, text, title); err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	it.ContentText = &text
	if it.Title == nil && title != nil {
		it.Title = title
	}
	return model.Stage{Status: "done"}
}

func (s *IngestService) extractFile(ctx context.Context, u model.User, it *model.Item) model.Stage {
	rc, _, err := s.store.Get(ctx, *it.FileKey)
	if err != nil {
		return model.Stage{Status: "failed", Error: "baca file: " + err.Error()}
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 60<<20))
	if err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	mimeType, name := "", ""
	if it.FileMime != nil {
		mimeType = *it.FileMime
	}
	if it.FileName != nil {
		name = *it.FileName
	}
	text, err := extract.Text(data, mimeType, name)
	if err != nil {
		return model.Stage{Status: "failed", Error: "ekstraksi: " + ai.Truncate(err.Error(), 200)}
	}
	text = extract.Clean(text)
	if err := s.repos.Items.SetContentText(ctx, u.ID, it.ID, it.ContentVersion, text, nil); err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	it.ContentText = &text
	return model.Stage{Status: "done"}
}

func (s *IngestService) transcribeAudio(ctx context.Context, u model.User, it *model.Item) model.Stage {
	if !s.aiOn || s.stt == nil {
		return model.Stage{Status: "skipped", Error: "STT nonaktif"}
	}
	rules := RulesFor(u.Plan)
	if rules.STTMaxMinutes == 0 {
		return model.Stage{Status: "skipped", Error: "transkrip suara hanya untuk paket Pro"}
	}
	rc, _, err := s.store.Get(ctx, *it.FileKey)
	if err != nil {
		return model.Stage{Status: "failed", Error: "baca audio: " + err.Error()}
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 100<<20))
	if err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	// Durasi tidak diketahui tanpa decoder: estimasi ~1 MB per menit (audio terkompresi) untuk kuota.
	minutes := int(int64(len(data))/(1<<20)) + 1
	if minutes > rules.STTMaxMinutes {
		return model.Stage{Status: "failed", Error: fmt.Sprintf("audio terlalu panjang (maks %d menit)", rules.STTMaxMinutes)}
	}
	if err := s.ent.Consume(ctx, u, MetricSTTMinutes, minutes); err != nil {
		return model.Stage{Status: "failed", Error: "kuota transkrip bulan ini habis"}
	}
	name := "audio.m4a"
	if it.FileName != nil && *it.FileName != "" {
		name = *it.FileName
	}
	lang := ""
	text, err := s.stt.Transcribe(ctx, bytes.NewReader(data), name, lang)
	if err != nil {
		return model.Stage{Status: "failed", Error: "stt: " + ai.Truncate(err.Error(), 200)}
	}
	text = extract.Clean(text)
	if err := s.repos.Items.SetContentText(ctx, u.ID, it.ID, it.ContentVersion, text, nil); err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	it.ContentText = &text
	return model.Stage{Status: "done"}
}

// ---------- understand ----------

type understandOut struct {
	Title              string         `json:"title"`
	Summary            string         `json:"summary"`
	KeyPoints          []string       `json:"key_points"`
	Entities           model.Entities `json:"entities"`
	Tags               []string       `json:"tags"`
	Language           string         `json:"language"`
	SuggestedReminders []struct {
		Text   string `json:"text"`
		At     string `json:"at"`
		Reason string `json:"reason"`
	} `json:"suggested_reminders"`
}

func (s *IngestService) stageUnderstand(ctx context.Context, u model.User, it *model.Item) model.Stage {
	if !s.aiOn {
		return model.Stage{Status: "skipped", Error: "AI nonaktif"}
	}
	content := buildContent(*it, 12000)
	if strings.TrimSpace(content) == "" {
		return model.Stage{Status: "skipped", Error: "tidak ada konten untuk diringkas"}
	}
	now := time.Now().In(u.Location())
	userMsg := fmt.Sprintf("WAKTU SEKARANG: %s (%s)\nJENIS: %s\nPLATFORM: %s\nUSER_NOTE: %s\n\nKONTEN:\n%s",
		now.Format(time.RFC3339), u.Timezone, it.Kind, deref(it.Platform), it.UserNote, content)
	resp, err := s.chat.Complete(ctx, ai.ChatRequest{Model: s.chatModel, System: understandPrompt, JSON: true, MaxTokens: 1200, Temperature: 0.2,
		Messages: []ai.Message{{Role: "user", Content: userMsg}}})
	if err != nil {
		return model.Stage{Status: "failed", Error: "llm: " + ai.Truncate(err.Error(), 200)}
	}
	var out understandOut
	if err := ai.ParseJSONLoose(resp.Content, &out); err != nil {
		return model.Stage{Status: "failed", Error: "jawaban LLM bukan JSON"}
	}
	p := repository.UnderstandPatch{Summary: strings.TrimSpace(out.Summary), KeyPoints: trimAll(out.KeyPoints, 8), Entities: out.Entities,
		AutoTags: excludeTags(normalizeTags(out.Tags), it.Tags)}
	setIf(&p.Title, ai.Truncate(out.Title, 120))
	setIf(&p.Language, out.Language)
	if err := s.repos.Items.ApplyUnderstand(ctx, u.ID, it.ID, it.ContentVersion, p); err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	it.Summary = &p.Summary
	it.KeyPoints = p.KeyPoints
	it.AutoTags = p.AutoTags
	if (it.Title == nil || *it.Title == "") && p.Title != nil {
		it.Title = p.Title
	}
	// Usulan reminder hanya Pro, dan hanya disimpan sebagai 'suggested' (butuh konfirmasi, ADR-010).
	if RulesFor(u.Plan).AISuggestReminds && s.remind != nil {
		for i, r := range out.SuggestedReminders {
			if i >= 3 || strings.TrimSpace(r.Text) == "" || r.At == "" {
				continue
			}
			at, err := time.Parse(time.RFC3339, r.At)
			if err != nil || at.Before(time.Now()) {
				continue
			}
			if _, err := s.remind.CreateStructured(ctx, u, ReminderInput{ItemID: &it.ID, Text: ai.Truncate(r.Text, 200), RemindAt: at, Origin: "ai", Suggested: true}); err != nil {
				slog.WarnContext(ctx, "simpan usulan reminder gagal", "item_id", it.ID, "err", err)
			}
		}
	}
	return model.Stage{Status: "done"}
}

func buildContent(it model.Item, maxChars int) string {
	var sb strings.Builder
	if it.Title != nil && *it.Title != "" {
		sb.WriteString("JUDUL: " + *it.Title + "\n")
	}
	if it.Description != nil && *it.Description != "" {
		sb.WriteString("DESKRIPSI: " + *it.Description + "\n")
	}
	if it.Body != "" {
		sb.WriteString("TEKS USER:\n" + it.Body + "\n")
	}
	if it.ContentText != nil && *it.ContentText != "" {
		sb.WriteString("ISI:\n" + *it.ContentText + "\n")
	}
	return ai.Truncate(sb.String(), maxChars)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func trimAll(in []string, max int) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, ai.Truncate(s, 200))
		}
		if len(out) >= max {
			break
		}
	}
	return out
}

func excludeTags(tags, existing []string) []string {
	seen := map[string]bool{}
	for _, t := range existing {
		seen[t] = true
	}
	out := []string{}
	for _, t := range tags {
		if !seen[t] {
			out = append(out, t)
		}
	}
	return out
}

// ---------- index ----------

func (s *IngestService) stageIndex(ctx context.Context, u model.User, it *model.Item) model.Stage {
	// Ambil state terbaru (summary dsb. baru saja ditulis).
	fresh, _, err := s.repos.Items.ByIDAnyState(ctx, u.ID, it.ID)
	if err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	keep := it.Ingest
	*it = fresh
	it.Ingest = keep

	chunks := BuildChunks(*it)
	if len(chunks) == 0 {
		return model.Stage{Status: "skipped", Error: "tidak ada teks untuk diindeks"}
	}
	modelName := ""
	if s.aiOn && s.embedder != nil {
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Text
		}
		embs, err := s.embedder.Embed(ctx, texts)
		if err != nil {
			slog.WarnContext(ctx, "embedding gagal, indeks full-text saja", "item_id", it.ID, "err", err)
		} else {
			modelName = s.embedder.Model()
			for i := range chunks {
				chunks[i].Embedding = embs[i]
			}
			if err := s.repos.Items.SetEmbedding(ctx, u.ID, it.ID, it.ContentVersion, embs[0], modelName); err != nil {
				slog.WarnContext(ctx, "simpan embedding item gagal", "item_id", it.ID, "err", err)
			}
		}
	}
	if err := s.repos.Chunks.Replace(ctx, u.ID, it.ID, it.ContentVersion, modelName, chunks); err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	if modelName == "" && s.aiOn {
		return model.Stage{Status: "done", Error: "tanpa embedding (hanya full-text)"}
	}
	return model.Stage{Status: "done"}
}

// BuildChunks: chunk pertama = ringkasan+judul+catatan (untuk embedding item), lalu isi dipotong ~900 rune dengan overlap.
func BuildChunks(it model.Item) []repository.NewChunk {
	var out []repository.NewChunk
	head := strings.TrimSpace(strings.Join(filterEmpty([]string{deref(it.Title), it.UserNote, deref(it.Summary), strings.Join(it.KeyPoints, " • ")}), "\n"))
	if head == "" && it.Body != "" {
		head = ai.Truncate(it.Body, 900)
	}
	if head != "" {
		out = append(out, repository.NewChunk{Seq: 0, Source: "summary", Text: head})
	}
	body := strings.TrimSpace(strings.Join(filterEmpty([]string{it.Body, deref(it.ContentText)}), "\n\n"))
	if body == "" || (len(out) > 0 && body == head) {
		return out
	}
	const size, overlap, maxChunks = 900, 120, 60
	r := []rune(body)
	seq := len(out)
	for start := 0; start < len(r) && seq < maxChunks; {
		end := start + size
		if end > len(r) {
			end = len(r)
		} else {
			// potong di batas kalimat/baris terdekat ke belakang
			for k := end; k > start+size/2; k-- {
				if r[k-1] == '\n' || r[k-1] == '.' || r[k-1] == '!' || r[k-1] == '?' {
					end = k
					break
				}
			}
		}
		text := strings.TrimSpace(string(r[start:end]))
		if utf8.RuneCountInString(text) > 20 {
			out = append(out, repository.NewChunk{Seq: seq, Source: "content", Text: text})
			seq++
		}
		if end >= len(r) {
			break
		}
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return out
}

func filterEmpty(in []string) []string {
	out := []string{}
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// ---------- safety net ----------

// RequeueStuck meng-enqueue ulang item yang macet (worker mati saat memproses).
func (s *IngestService) RequeueStuck(ctx context.Context, q Enqueuer) (int, error) {
	items, err := s.repos.Items.Stuck(ctx, 15*time.Minute, 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		// Tahap 'running' yang terputus kembali ke pending supaya dijalankan lagi.
		for _, pair := range []struct {
			name string
			st   *model.Stage
		}{{"fetch", &it.Ingest.Fetch}, {"transcribe", &it.Ingest.Transcribe}, {"understand", &it.Ingest.Understand}, {"index", &it.Ingest.Index}} {
			if pair.st.Status == "running" {
				_ = s.repos.Items.SetStage(ctx, it.UserID, it.ID, it.ContentVersion, pair.name, model.Stage{Status: "pending"}, "")
			}
		}
		u, err := s.repos.Users.ByID(ctx, it.UserID)
		if err != nil {
			continue
		}
		if err := q.EnqueueIngest(ctx, it.ID, it.UserID, it.ContentVersion, RulesFor(u.Plan).QueuePriority); err == nil {
			n++
		}
	}
	return n, nil
}

// ReindexStale: ganti model embedding → re-index item yang embedding-nya dari model lama.
func (s *IngestService) ReindexStale(ctx context.Context, q Enqueuer) (int, error) {
	if !s.aiOn || s.embedder == nil {
		return 0, nil
	}
	pairs, err := s.repos.Chunks.StaleEmbeddingItems(ctx, s.embedder.Model(), 50)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range pairs {
		it, err := s.repos.Items.BumpVersion(ctx, p[1], p[0], []string{"index"})
		if err != nil {
			continue
		}
		if err := q.EnqueueIngest(ctx, it.ID, it.UserID, it.ContentVersion, "low"); err == nil {
			n++
		}
	}
	return n, nil
}
