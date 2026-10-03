package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/aiagent"
	"vaulty-api/pkg/extract"
	"vaulty-api/pkg/storage"
)

// IndexService dijalankan worker: ekstraksi teks file -> auto-tag -> embedding.
type IndexService struct {
	notes    *repository.NoteRepository
	store    storage.Storage
	embedder aiagent.Embedder
	tagger   aiagent.Tagger
	enqueuer IndexEnqueuer
}

func NewIndexService(notes *repository.NoteRepository, store storage.Storage, embedder aiagent.Embedder, tagger aiagent.Tagger, enqueuer IndexEnqueuer) *IndexService {
	return &IndexService{notes: notes, store: store, embedder: embedder, tagger: tagger, enqueuer: enqueuer}
}

// Batas ukuran file yang diekstrak teksnya.
const maxExtractBytes = 30 << 20

// Index memproses satu versi note. Note yang sudah dihapus atau sudah punya versi lebih baru
// dilewati tanpa error (task untuk versi baru yang akan mengerjakannya).
func (s *IndexService) Index(ctx context.Context, noteID, userID int64, version int32) error {
	n, err := s.notes.FindForIndex(ctx, userID, noteID)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.ContentVersion != version {
		return nil
	}

	content := n.ContentText
	if n.FileKey != "" && !n.HasContentText {
		if content, err = s.extractFile(ctx, n); err != nil {
			// File rusak/tidak terbaca: tetap lanjut index judul & deskripsi.
			slog.Warn("ekstraksi teks gagal", "note_id", n.ID, "err", err)
			content = ""
		}
		if err := s.notes.SaveContentText(ctx, n.UserID, n.ID, content); err != nil {
			return err
		}
	}
	doc := buildIndexText(n, content)

	var autoTags []string
	if s.tagger.Enabled() {
		tags, err := s.tagger.SuggestTags(ctx, doc)
		if err != nil {
			return fmt.Errorf("auto-tag: %w", err)
		}
		autoTags = excludeTags(tags, n.Tags)
	}

	var embedding []float32
	if s.embedder.Enabled() {
		if embedding, err = s.embedder.Embed(ctx, doc); err != nil {
			return fmt.Errorf("embedding: %w", err)
		}
	}
	return s.notes.MarkIndexed(ctx, n, embedding, s.embedder.Model(), autoTags)
}

// MarkFailed dipanggil worker jika Index gagal permanen (retry habis).
func (s *IndexService) MarkFailed(ctx context.Context, noteID, userID int64, version int32, cause error) {
	n := model.IndexableNote{ID: noteID, UserID: userID, ContentVersion: version}
	if err := s.notes.MarkIndexFailed(ctx, n, aiagent.Truncate(cause.Error(), 500)); err != nil {
		slog.Error("tandai index failed gagal", "note_id", noteID, "err", err)
	}
}

// RequeuePending adalah safety net: menandai note dengan model embedding lama (mis. setelah
// pindah Ollama -> OpenAI), lalu meng-enqueue ulang note yang tertahan pending > 2 menit.
func (s *IndexService) RequeuePending(ctx context.Context) error {
	if err := s.RequeueStaleEmbeddings(ctx); err != nil {
		return err
	}
	pending, err := s.notes.ListPending(ctx, 120, 200)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if err := s.enqueuer.EnqueueIndex(ctx, p.ID, p.UserID, p.ContentVersion); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		slog.Info("safety net: note pending di-enqueue ulang", "count", len(pending))
	}
	return nil
}

// RequeueStaleEmbeddings menandai note yang embedding-nya dari model lain agar di-embed ulang.
// Dipanggil saat worker start dan berkala, jadi ganti provider AI cukup ubah .env.
func (s *IndexService) RequeueStaleEmbeddings(ctx context.Context) error {
	if !s.embedder.Enabled() {
		return nil
	}
	const batch = 500
	for {
		n, err := s.notes.RequeueStaleEmbeddings(ctx, s.embedder.Model(), batch)
		if err != nil {
			return err
		}
		if n > 0 {
			slog.Info("note dengan model embedding lama dijadwalkan ulang", "count", n, "model", s.embedder.Model())
		}
		if n < batch {
			return nil
		}
	}
}

func (s *IndexService) extractFile(ctx context.Context, n model.IndexableNote) (string, error) {
	rc, err := s.store.Open(ctx, n.FileKey)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxExtractBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxExtractBytes {
		return "", fmt.Errorf("file lebih dari %d MB, teks tidak diekstrak", maxExtractBytes>>20)
	}
	return extract.Text(data, n.FileMime, n.FileName)
}

// buildIndexText menyusun teks yang di-embed & di-tag dari seluruh field konten.
func buildIndexText(n model.IndexableNote, content string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "type: %s\n", n.Type)
	for _, part := range []struct{ label, v string }{
		{"title", n.Title}, {"project", n.Project}, {"url", n.URL},
		{"file", n.FileName}, {"tags", strings.Join(n.Tags, ", ")},
	} {
		if part.v != "" {
			fmt.Fprintf(&b, "%s: %s\n", part.label, part.v)
		}
	}
	if n.Body != "" {
		b.WriteString("\n" + n.Body + "\n")
	}
	if content != "" {
		b.WriteString("\n" + content)
	}
	return b.String()
}

func excludeTags(tags, existing []string) []string {
	have := map[string]bool{}
	for _, t := range existing {
		have[t] = true
	}
	out := []string{}
	for _, t := range tags {
		if !have[t] {
			out = append(out, t)
		}
	}
	return out
}
