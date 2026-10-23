package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/storage"
)

// AccountService: profil, ekspor data, hapus akun (ADR-013).
type AccountService struct {
	repos *repository.Repos
	store storage.Storage
	queue Enqueuer
	ent   *EntitlementService
}

func NewAccountService(repos *repository.Repos, store storage.Storage, queue Enqueuer, ent *EntitlementService) *AccountService {
	return &AccountService{repos: repos, store: store, queue: queue, ent: ent}
}

func (s *AccountService) Me(ctx context.Context, u model.User) (model.User, []model.Quota, error) {
	q, err := s.ent.Quotas(ctx, u)
	return u, q, err
}

func (s *AccountService) Update(ctx context.Context, u model.User, name, tz *string) (model.User, error) {
	if tz != nil {
		if _, err := time.LoadLocation(*tz); err != nil || *tz == "" {
			return u, model.Invalid("timezone tidak valid (pakai nama IANA, mis. Asia/Jakarta)")
		}
	}
	if name != nil {
		t := strings.TrimSpace(*name)
		if len(t) > 80 {
			return u, model.Invalid("nama maksimal 80 karakter")
		}
		name = &t
	}
	return s.repos.Users.Update(ctx, u.ID, name, tz)
}

// RequestDelete: sesi dicabut sekarang, data dihapus worker.
func (s *AccountService) RequestDelete(ctx context.Context, u model.User) error {
	if err := s.repos.Sessions.DeleteAll(ctx, u.ID); err != nil {
		return err
	}
	if err := s.repos.Users.MarkDeleted(ctx, u.ID); err != nil {
		return err
	}
	_ = s.repos.Audit.Log(ctx, nil, "account.delete.requested", "user", &u.ID, nil, "")
	if err := s.queue.EnqueueAccountDelete(ctx, u.ID); err != nil {
		slog.ErrorContext(ctx, "enqueue hapus akun gagal", "user_id", u.ID, "err", err)
	}
	return nil
}

// PurgeAccount dijalankan worker: hapus objek storage lalu baris DB.
func (s *AccountService) PurgeAccount(ctx context.Context, userID int64) error {
	keys, err := s.repos.Items.StorageKeys(ctx, userID)
	if err != nil {
		return err
	}
	audio, err := s.repos.Briefing.AudioKeys(ctx, userID)
	if err != nil {
		return err
	}
	keys = append(keys, audio...)
	for _, k := range keys {
		if err := s.store.Delete(ctx, k); err != nil {
			slog.WarnContext(ctx, "hapus objek gagal", "key", k, "err", err)
		}
	}
	return s.repos.Users.HardDelete(ctx, userID)
}

func (s *AccountService) RequestExport(ctx context.Context, u model.User) (model.ExportJob, error) {
	j, err := s.repos.Billing.CreateExport(ctx, u.ID)
	if err != nil {
		return j, err
	}
	if err := s.queue.EnqueueExport(ctx, j.ID, u.ID); err != nil {
		return j, fmt.Errorf("enqueue ekspor: %w", err)
	}
	return j, nil
}

func (s *AccountService) ExportStatus(ctx context.Context, u model.User, jobID int64) (model.ExportJob, error) {
	j, key, err := s.repos.Billing.Export(ctx, u.ID, jobID)
	if err != nil {
		return j, err
	}
	if j.Status == "ready" && key != nil {
		if url, err := s.store.URL(ctx, *key, time.Hour); err == nil {
			j.URL = &url
		}
	}
	return j, nil
}

// BuildExport dijalankan worker: zip berisi items.json, reminders.json, dan satu Markdown per item.
func (s *AccountService) BuildExport(ctx context.Context, jobID, userID int64) error {
	items, err := s.repos.Items.AllForExport(ctx, userID)
	if err != nil {
		return s.failExport(ctx, jobID, err)
	}
	reminders, err := s.repos.Reminder.List(ctx, userID, "all", nil, 10000)
	if err != nil {
		return s.failExport(ctx, jobID, err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	ij, _ := json.MarshalIndent(items, "", "  ")
	rj, _ := json.MarshalIndent(reminders, "", "  ")
	if err := write("items.json", ij); err != nil {
		return s.failExport(ctx, jobID, err)
	}
	if err := write("reminders.json", rj); err != nil {
		return s.failExport(ctx, jobID, err)
	}
	for _, it := range items {
		if err := write(fmt.Sprintf("items/%d.md", it.ID), []byte(itemMarkdown(it))); err != nil {
			return s.failExport(ctx, jobID, err)
		}
	}
	if err := zw.Close(); err != nil {
		return s.failExport(ctx, jobID, err)
	}
	key := fmt.Sprintf("u/%d/export/%d.zip", userID, jobID)
	if err := s.store.Put(ctx, key, bytes.NewReader(buf.Bytes()), int64(buf.Len()), "application/zip"); err != nil {
		return s.failExport(ctx, jobID, err)
	}
	return s.repos.Billing.FinishExport(ctx, jobID, "ready", &key, nil)
}

func (s *AccountService) failExport(ctx context.Context, jobID int64, err error) error {
	msg := err.Error()
	_ = s.repos.Billing.FinishExport(ctx, jobID, "failed", nil, &msg)
	return err
}

func itemMarkdown(it model.Item) string {
	var sb strings.Builder
	title := fmt.Sprintf("Item %d", it.ID)
	if it.Title != nil && *it.Title != "" {
		title = *it.Title
	}
	fmt.Fprintf(&sb, "# %s\n\n", title)
	fmt.Fprintf(&sb, "- Jenis: %s\n- Disimpan: %s\n", it.Kind, it.CreatedAt.Format(time.RFC3339))
	if it.SourceURL != nil {
		fmt.Fprintf(&sb, "- Sumber: %s\n", *it.SourceURL)
	}
	if len(it.Tags) > 0 || len(it.AutoTags) > 0 {
		fmt.Fprintf(&sb, "- Tag: %s\n", strings.Join(append(append([]string{}, it.Tags...), it.AutoTags...), ", "))
	}
	if it.UserNote != "" {
		fmt.Fprintf(&sb, "\n## Catatan\n\n%s\n", it.UserNote)
	}
	if it.Summary != nil && *it.Summary != "" {
		fmt.Fprintf(&sb, "\n## Ringkasan\n\n%s\n", *it.Summary)
	}
	if len(it.KeyPoints) > 0 {
		sb.WriteString("\n## Poin penting\n\n")
		for _, k := range it.KeyPoints {
			fmt.Fprintf(&sb, "- %s\n", k)
		}
	}
	if it.Body != "" {
		fmt.Fprintf(&sb, "\n## Teks\n\n%s\n", it.Body)
	}
	if it.ContentText != nil && *it.ContentText != "" {
		fmt.Fprintf(&sb, "\n## Konten asli\n\n%s\n", *it.ContentText)
	}
	return sb.String()
}
