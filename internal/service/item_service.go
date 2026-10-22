package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/extract"
	"vaulty/pkg/fetch"
	"vaulty/pkg/storage"
)

// Enqueuer didefinisikan di sisi pemakai; implementasinya di package worker (asynq).
type Enqueuer interface {
	EnqueueIngest(ctx context.Context, itemID, userID int64, version int32, queue string) error
	EnqueueAccountDelete(ctx context.Context, userID int64) error
	EnqueueExport(ctx context.Context, jobID, userID int64) error
}

const signedURLTTL = time.Hour

type ItemService struct {
	repos  *repository.Repos
	store  storage.Storage
	queue  Enqueuer
	ent    *EntitlementService
	remind *ReminderService
}

func NewItemService(repos *repository.Repos, store storage.Storage, queue Enqueuer, ent *EntitlementService, remind *ReminderService) *ItemService {
	return &ItemService{repos: repos, store: store, queue: queue, ent: ent, remind: remind}
}

type CaptureInput struct {
	URL      string
	Text     string
	UserNote string
	Tags     []string
	RemindAt *time.Time
	// File (opsional): gambar, audio, dokumen.
	FileName string
	FileMime string
	File     []byte
}

// Capture: simpan dulu (tidak pernah gagal karena AI), lalu enqueue pipeline (ADR-006).
func (s *ItemService) Capture(ctx context.Context, u model.User, in CaptureInput) (model.Item, error) {
	in.URL = strings.TrimSpace(in.URL)
	in.Text = strings.TrimSpace(in.Text)
	in.UserNote = strings.TrimSpace(in.UserNote)
	if in.URL == "" && in.Text == "" && len(in.File) == 0 {
		return model.Item{}, model.Invalid("isi salah satu: url, text, atau file")
	}
	// Teks bebas yang ternyata hanya URL → perlakukan sebagai link.
	if in.URL == "" && len(in.File) == 0 && looksLikeURL(in.Text) {
		in.URL, in.Text = in.Text, ""
	}
	if warn := detectSecret(in.Text, in.UserNote); warn != "" {
		return model.Item{}, model.Invalid("%s", warn)
	}

	n := repository.NewItem{UserID: u.ID, UserNote: in.UserNote, Tags: normalizeTags(in.Tags)}
	pending := model.Stage{Status: "pending"}
	skipped := model.Stage{Status: "skipped"}

	switch {
	case len(in.File) > 0:
		kind, err := s.classifyFile(u, in)
		if err != nil {
			return model.Item{}, err
		}
		n.Kind = kind
		key := fmt.Sprintf("u/%d/%s/%s%s", u.ID, kind, uuid.NewString(), safeExt(in.FileName, in.FileMime))
		if err := s.store.Put(ctx, key, bytes.NewReader(in.File), int64(len(in.File)), in.FileMime); err != nil {
			return model.Item{}, fmt.Errorf("simpan file: %w", err)
		}
		size := int64(len(in.File))
		name := filepath.Base(in.FileName)
		n.FileKey, n.FileName, n.FileMime, n.FileSize = &key, &name, &in.FileMime, &size
		title := strings.TrimSuffix(name, filepath.Ext(name))
		if title != "" && kind != "image" {
			n.Title = &title
		}
		n.Body = in.Text
		n.Ingest = model.Ingest{Fetch: skipped, Transcribe: pending, Understand: pending, Index: pending}
	case in.URL != "":
		canon, platform, err := fetch.Canonicalize(in.URL)
		if err != nil {
			return model.Item{}, model.Invalid("url tidak valid")
		}
		n.Kind = "url"
		n.SourceURL, n.CanonicalURL, n.Platform = &in.URL, &canon, &platform
		n.Body = in.Text
		n.Ingest = model.Ingest{Fetch: pending, Transcribe: pending, Understand: pending, Index: pending}
	default:
		n.Kind = "text"
		n.Body = in.Text
		n.Ingest = model.Ingest{Fetch: skipped, Transcribe: skipped, Understand: pending, Index: pending}
	}

	// Kuota: items selalu; images/files sesuai jenis (ADR-011). Dikonsumsi sebelum simpan.
	if err := s.ent.Consume(ctx, u, MetricItems, 1); err != nil {
		return model.Item{}, err
	}
	switch n.Kind {
	case "image":
		if err := s.ent.Consume(ctx, u, MetricImages, 1); err != nil {
			_ = s.ent.Consume(ctx, u, MetricItems, -1)
			return model.Item{}, err
		}
	case "file":
		if err := s.ent.Consume(ctx, u, MetricFiles, 1); err != nil {
			_ = s.ent.Consume(ctx, u, MetricItems, -1)
			return model.Item{}, err
		}
	}

	it, err := s.repos.Items.Create(ctx, n)
	if err != nil {
		return model.Item{}, fmt.Errorf("simpan item: %w", err)
	}
	if in.RemindAt != nil {
		title := in.UserNote
		if title == "" {
			title = "Lihat lagi item ini"
		}
		if _, err := s.remind.CreateStructured(ctx, u, ReminderInput{ItemID: &it.ID, Text: title, RemindAt: *in.RemindAt, Origin: "item"}); err != nil {
			slog.WarnContext(ctx, "reminder saat capture gagal", "item_id", it.ID, "err", err)
		}
	}
	_ = s.repos.Audit.Log(ctx, &u.ID, "item.create", "item", &it.ID, map[string]any{"kind": it.Kind}, "")
	s.enqueue(ctx, u, it)
	return s.decorate(ctx, it), nil
}

func (s *ItemService) classifyFile(u model.User, in CaptureInput) (string, error) {
	if int64(len(in.File)) > MaxUploadBytes(u.Plan) {
		return "", fmt.Errorf("%w: maksimal %d MB untuk paket %s", model.ErrTooLarge, RulesFor(u.Plan).MaxUploadMB, u.Plan)
	}
	m := in.FileMime
	if m == "" || m == "application/octet-stream" {
		m = http.DetectContentType(in.File)
		if m == "application/octet-stream" {
			if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(in.FileName))); byExt != "" {
				m = byExt
			}
		}
	}
	m = strings.Split(m, ";")[0]
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image", nil
	case strings.HasPrefix(m, "audio/"), m == "video/mp4", m == "video/webm", m == "application/ogg":
		return "audio", nil
	case extract.IsSupported(m, in.FileName):
		return "file", nil
	}
	return "", fmt.Errorf("%w: %s", model.ErrUnsupportedFile, m)
}

func (s *ItemService) enqueue(ctx context.Context, u model.User, it model.Item) {
	if err := s.queue.EnqueueIngest(ctx, it.ID, u.ID, it.ContentVersion, RulesFor(u.Plan).QueuePriority); err != nil {
		// Gagal enqueue tidak menggagalkan capture; safety net worker akan menyusul.
		slog.WarnContext(ctx, "enqueue ingest gagal", "item_id", it.ID, "err", err)
	}
}

func (s *ItemService) Get(ctx context.Context, u model.User, id int64) (model.Item, error) {
	it, err := s.repos.Items.ByID(ctx, u.ID, id)
	if err != nil {
		return it, err
	}
	return s.decorate(ctx, it), nil
}

func (s *ItemService) List(ctx context.Context, u model.User, f repository.ItemFilter, cursor int64, limit int) ([]model.Card, *int64, error) {
	items, next, err := s.repos.Items.List(ctx, u.ID, f, cursor, limit)
	if err != nil {
		return nil, nil, err
	}
	return s.cards(ctx, items), next, nil
}

func (s *ItemService) UpdatedSince(ctx context.Context, u model.User, since time.Time) ([]model.Card, error) {
	items, err := s.repos.Items.UpdatedSince(ctx, u.ID, since, 200)
	if err != nil {
		return nil, err
	}
	return s.cards(ctx, items), nil
}

func (s *ItemService) Patch(ctx context.Context, u model.User, id int64, p repository.ItemPatch) (model.Item, error) {
	if p.UserNote != nil {
		if warn := detectSecret(*p.UserNote); warn != "" {
			return model.Item{}, model.Invalid("%s", warn)
		}
	}
	if p.Tags != nil {
		p.Tags = normalizeTags(p.Tags)
	}
	it, err := s.repos.Items.Patch(ctx, u.ID, id, p)
	if err != nil {
		return it, err
	}
	// Catatan/tag ikut diindeks → index ulang saja, tanpa fetch/understand.
	it, err = s.repos.Items.BumpVersion(ctx, u.ID, id, []string{"index"})
	if err != nil {
		return it, err
	}
	s.enqueue(ctx, u, it)
	return s.decorate(ctx, it), nil
}

func (s *ItemService) Delete(ctx context.Context, u model.User, id int64) error {
	if err := s.repos.Items.SoftDelete(ctx, u.ID, id); err != nil {
		return err
	}
	_ = s.repos.Audit.Log(ctx, &u.ID, "item.delete", "item", &id, nil, "")
	return nil
}

func (s *ItemService) Reprocess(ctx context.Context, u model.User, id int64, stages []string) (model.Item, error) {
	if len(stages) == 0 {
		stages = []string{"fetch", "transcribe", "understand", "index"}
	}
	it, err := s.repos.Items.BumpVersion(ctx, u.ID, id, stages)
	if err != nil {
		return it, err
	}
	s.enqueue(ctx, u, it)
	return s.decorate(ctx, it), nil
}

// AddAttachment: screenshot pendamping untuk link sosmed yang gagal di-fetch.
func (s *ItemService) AddAttachment(ctx context.Context, u model.User, id int64, name, mimeType string, data []byte) (model.Item, error) {
	if _, err := s.repos.Items.ByID(ctx, u.ID, id); err != nil {
		return model.Item{}, err
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return model.Item{}, fmt.Errorf("%w: lampiran harus gambar", model.ErrUnsupportedFile)
	}
	if int64(len(data)) > MaxUploadBytes(u.Plan) {
		return model.Item{}, model.ErrTooLarge
	}
	if err := s.ent.Consume(ctx, u, MetricImages, 1); err != nil {
		return model.Item{}, err
	}
	key := fmt.Sprintf("u/%d/attach/%s%s", u.ID, uuid.NewString(), safeExt(name, mimeType))
	if err := s.store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
		return model.Item{}, err
	}
	if err := s.repos.Items.SetAttachment(ctx, u.ID, id, key); err != nil {
		return model.Item{}, err
	}
	it, err := s.repos.Items.BumpVersion(ctx, u.ID, id, []string{"transcribe", "understand", "index"})
	if err != nil {
		return it, err
	}
	s.enqueue(ctx, u, it)
	return s.decorate(ctx, it), nil
}

func (s *ItemService) Related(ctx context.Context, u model.User, id int64) ([]model.Card, error) {
	if _, err := s.repos.Items.ByID(ctx, u.ID, id); err != nil {
		return nil, err
	}
	items, err := s.repos.Items.Related(ctx, u.ID, id, 5)
	if err != nil {
		return nil, err
	}
	return s.cards(ctx, items), nil
}

func (s *ItemService) OpenFile(ctx context.Context, u model.User, id int64) (io.ReadCloser, string, string, error) {
	it, err := s.repos.Items.ByID(ctx, u.ID, id)
	if err != nil {
		return nil, "", "", err
	}
	if it.FileKey == nil {
		return nil, "", "", model.ErrNotFound
	}
	rc, ct, err := s.store.Get(ctx, *it.FileKey)
	if err != nil {
		return nil, "", "", err
	}
	name := ""
	if it.FileName != nil {
		name = *it.FileName
	}
	return rc, ct, name, nil
}

// decorate mengisi URL bertanda tangan untuk thumbnail dan file.
func (s *ItemService) decorate(ctx context.Context, it model.Item) model.Item {
	if it.ThumbnailKey != nil && *it.ThumbnailKey != "" {
		if u, err := s.store.URL(ctx, *it.ThumbnailKey, signedURLTTL); err == nil {
			it.ThumbnailURL = &u
		}
	} else if it.Kind == "image" && it.FileKey != nil {
		if u, err := s.store.URL(ctx, *it.FileKey, signedURLTTL); err == nil {
			it.ThumbnailURL = &u
		}
	}
	if it.FileKey != nil && it.FileName != nil {
		u, err := s.store.URL(ctx, *it.FileKey, signedURLTTL)
		if err == nil {
			var size int64
			if it.FileSize != nil {
				size = *it.FileSize
			}
			m := ""
			if it.FileMime != nil {
				m = *it.FileMime
			}
			it.File = &model.FileInfo{Name: *it.FileName, Mime: m, Size: size, URL: u}
		}
	}
	return it
}

func (s *ItemService) Card(ctx context.Context, it model.Item) model.Card {
	return s.decorate(ctx, it).Card()
}

func (s *ItemService) cards(ctx context.Context, items []model.Item) []model.Card {
	out := make([]model.Card, 0, len(items))
	for _, it := range items {
		out = append(out, s.decorate(ctx, it).Card())
	}
	return out
}

// ---- helpers ----

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " \n\t") {
		return false
	}
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "#")))
		t = strings.ReplaceAll(t, " ", "-")
		if t == "" || len(t) > 40 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func safeExt(name, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" || len(ext) > 6 {
		if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
			ext = exts[0]
		}
	}
	return ext
}

// detectSecret menolak pola credential yang jelas (ADR-012). Bukan filter sempurna, cukup pagar.
func detectSecret(texts ...string) string {
	patterns := []string{"-----BEGIN PRIVATE KEY", "-----BEGIN RSA PRIVATE KEY", "sk-live_", "AKIA", "ghp_", "xoxb-", "password:", "password=", "api_key=", "apikey="}
	for _, t := range texts {
		lt := strings.ToLower(t)
		for _, p := range patterns {
			if strings.Contains(lt, strings.ToLower(p)) {
				return "teks terdeteksi berisi credential/API key; Vaulty tidak menyimpan credential, gunakan password manager"
			}
		}
	}
	return ""
}
