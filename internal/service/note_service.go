package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/pkg/aiagent"
	"vaulty-api/pkg/storage"
)

// IndexEnqueuer menjadwalkan indexing note (ekstraksi, auto-tag, embedding) di worker.
type IndexEnqueuer interface {
	EnqueueIndex(ctx context.Context, noteID, userID int64, version int32) error
}

// NoteService menangani item Vaulty: note, link, command, document, dan tipe lain (ADR-016).
type NoteService struct {
	notes    *repository.NoteRepository
	store    storage.Storage
	enqueuer IndexEnqueuer
}

func NewNoteService(notes *repository.NoteRepository, store storage.Storage, enqueuer IndexEnqueuer) *NoteService {
	return &NoteService{notes: notes, store: store, enqueuer: enqueuer}
}

const maxTags = 20

func (s *NoteService) Create(ctx context.Context, userID int64, in model.CreateNoteInput) (model.NoteResult, error) {
	typ := in.Type
	if strings.TrimSpace(typ) == "" {
		typ, in.Body, in.URL = InferType(in.Body, in.URL)
	}
	typ, err := NormalizeType(typ)
	if err != nil {
		return model.NoteResult{}, err
	}
	if isBlank(in.Body) && isBlank(in.URL) && isBlank(in.Title) {
		return model.NoteResult{}, model.ErrEmptyNote
	}
	meta, err := marshalMetadata(in.Metadata)
	if err != nil {
		return model.NoteResult{}, err
	}
	n, err := s.notes.Create(ctx, repository.NewNote{
		UserID:   userID,
		Type:     typ,
		Title:    strings.TrimSpace(in.Title),
		Body:     in.Body,
		URL:      strings.TrimSpace(in.URL),
		Tags:     aiagent.NormalizeTags(in.Tags, maxTags),
		Project:  strings.TrimSpace(in.Project),
		Metadata: meta,
	})
	if err != nil {
		return model.NoteResult{}, err
	}
	s.enqueue(ctx, n, userID)
	return result(n, DetectSecrets(in.Title, in.Body, in.URL)), nil
}

// Upload menyimpan file lalu membuat note (default type document, ADR-017). Ekstraksi teks
// dan indexing berjalan async di worker.
func (s *NoteService) Upload(ctx context.Context, userID int64, in model.UploadNoteInput) (model.NoteResult, error) {
	typ := in.Type
	if strings.TrimSpace(typ) == "" {
		typ = model.NoteTypeDocument
	}
	typ, err := NormalizeType(typ)
	if err != nil {
		return model.NoteResult{}, err
	}
	filename := filepath.Base(strings.ReplaceAll(in.Filename, `\`, "/"))
	if filename == "." || filename == "/" || filename == "" {
		filename = "file"
	}

	head := make([]byte, 512)
	n, _ := io.ReadFull(in.File, head)
	head = head[:n]
	mimeType := detectMime(in.Mime, filename, head)

	key, size, err := s.store.Save(ctx, userID, filename, io.MultiReader(bytes.NewReader(head), in.File))
	if err != nil {
		return model.NoteResult{}, fmt.Errorf("simpan file: %w", err)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	note, err := s.notes.Create(ctx, repository.NewNote{
		UserID:   userID,
		Type:     typ,
		Title:    title,
		Body:     in.Body,
		Tags:     aiagent.NormalizeTags(in.Tags, maxTags),
		Project:  strings.TrimSpace(in.Project),
		Metadata: []byte("{}"),
		FileKey:  key,
		FileName: filename,
		FileMime: mimeType,
		FileSize: size,
	})
	if err != nil {
		if derr := s.store.Delete(ctx, key); derr != nil {
			slog.ErrorContext(ctx, "hapus file yatim gagal", "key", key, "err", derr)
		}
		return model.NoteResult{}, err
	}
	s.enqueue(ctx, note, userID)
	return result(note, DetectSecrets(in.Title, in.Body)), nil
}

func (s *NoteService) Get(ctx context.Context, userID, id int64) (model.Note, error) {
	return s.notes.FindByID(ctx, userID, id)
}

// List mengembalikan satu halaman note (terbaru dulu) dan cursor halaman berikutnya (nil jika habis).
func (s *NoteService) List(ctx context.Context, userID int64, f model.NoteFilter, cursor int64, limit int) ([]model.Note, *int64, error) {
	items, err := s.notes.List(ctx, userID, f, cursor, limit+1)
	if err != nil {
		return nil, nil, err
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	items = items[:limit]
	next := items[len(items)-1].ID
	return items, &next, nil
}

func (s *NoteService) Update(ctx context.Context, userID, id int64, in model.UpdateNoteInput) (model.NoteResult, error) {
	cur, err := s.notes.FindByID(ctx, userID, id)
	if err != nil {
		return model.NoteResult{}, err
	}
	// Last-write-wins: edit dari client yang lebih lama dari versi server diabaikan.
	if in.UpdatedAt != nil && in.UpdatedAt.Before(cur.UpdatedAt) {
		return result(cur, []string{"Perubahan diabaikan: note sudah diubah di tempat lain setelah waktu edit ini."}), nil
	}

	u := repository.NoteUpdate{
		ID:      id,
		UserID:  userID,
		Type:    cur.Type,
		Title:   deref(cur.Title),
		Body:    cur.Body,
		URL:     deref(cur.URL),
		Tags:    cur.Tags,
		Project: deref(cur.Project),
	}
	if in.Type != nil {
		if u.Type, err = NormalizeType(*in.Type); err != nil {
			return model.NoteResult{}, err
		}
	}
	if in.Title != nil {
		u.Title = strings.TrimSpace(*in.Title)
	}
	if in.Body != nil {
		u.Body = *in.Body
	}
	if in.URL != nil {
		u.URL = strings.TrimSpace(*in.URL)
	}
	if in.Tags != nil {
		u.Tags = aiagent.NormalizeTags(*in.Tags, maxTags)
	}
	if in.Project != nil {
		u.Project = strings.TrimSpace(*in.Project)
	}
	metadata := cur.Metadata
	if in.Metadata != nil {
		metadata = in.Metadata
	}
	if u.Metadata, err = marshalMetadata(metadata); err != nil {
		return model.NoteResult{}, err
	}
	// Hanya perubahan konten yang memicu index ulang (auto-tag + embedding).
	u.ContentChanged = u.Title != deref(cur.Title) || u.Body != cur.Body || u.URL != deref(cur.URL)

	n, err := s.notes.Update(ctx, u)
	if err != nil {
		return model.NoteResult{}, err
	}
	if u.ContentChanged {
		s.enqueue(ctx, n, userID)
	}
	return result(n, DetectSecrets(u.Title, u.Body, u.URL)), nil
}

// Delete adalah soft delete (ADR-006). File lampiran tetap disimpan agar note bisa dipulihkan.
func (s *NoteService) Delete(ctx context.Context, userID, id int64) error {
	return s.notes.SoftDelete(ctx, userID, id)
}

// Reindex memaksa index ulang, mis. setelah status failed atau ganti model AI.
func (s *NoteService) Reindex(ctx context.Context, userID, id int64) (model.Note, error) {
	n, err := s.notes.Requeue(ctx, userID, id)
	if err != nil {
		return model.Note{}, err
	}
	s.enqueue(ctx, n, userID)
	return n, nil
}

func (s *NoteService) Types(ctx context.Context, userID int64) ([]model.TypeCount, error) {
	return s.notes.Types(ctx, userID)
}

// OpenFile membuka file lampiran milik user. Pemanggil wajib menutup ReadCloser.
func (s *NoteService) OpenFile(ctx context.Context, userID, id int64) (io.ReadCloser, *model.File, error) {
	key, n, err := s.notes.FileKey(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}
	if key == "" || n.File == nil {
		return nil, nil, model.ErrNotFound
	}
	rc, err := s.store.Open(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	return rc, n.File, nil
}

// enqueue tidak menggagalkan request: jika Redis bermasalah, note tetap pending
// dan akan diambil safety net worker.
func (s *NoteService) enqueue(ctx context.Context, n model.Note, userID int64) {
	if err := s.enqueuer.EnqueueIndex(ctx, n.ID, userID, n.ContentVersion); err != nil {
		slog.WarnContext(ctx, "enqueue index gagal, menunggu safety net", "note_id", n.ID, "err", err)
	}
}

// detectMime: ekstensi lebih akurat untuk format berbasis zip (docx, xlsx) yang terdeteksi
// sebagai application/zip dari isinya.
func detectMime(clientMime, filename string, head []byte) string {
	if clientMime != "" && clientMime != "application/octet-stream" {
		return clientMime
	}
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); m != "" {
		return m
	}
	return http.DetectContentType(head)
}

func result(n model.Note, warnings []string) model.NoteResult {
	if warnings == nil {
		warnings = []string{}
	}
	return model.NoteResult{Note: n, Warnings: warnings}
}

func marshalMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("metadata tidak valid: %w", err)
	}
	return b, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }
