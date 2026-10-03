package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/handler"
	"vaulty-api/internal/middleware"
	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/internal/service"
)

type NoteHandler struct {
	notes          *service.NoteService
	audit          *service.AuditService
	maxUploadBytes int64
}

func NewNoteHandler(notes *service.NoteService, audit *service.AuditService, maxUploadBytes int64) *NoteHandler {
	return &NoteHandler{notes: notes, audit: audit, maxUploadBytes: maxUploadBytes}
}

// --- Request & response ---

// NoteIDRequest exported karena di-embed di updateNoteRequest (huma tidak membaca embedded unexported).
type NoteIDRequest struct {
	ID int64 `path:"id"`
}

type createNoteRequest struct {
	Body struct {
		Type     string         `json:"type,omitempty" maxLength:"32" doc:"Bebas (note, link, command, document, ...). Kosong: otomatis note/link. Tipe credential ditolak." example:"command"`
		Title    string         `json:"title,omitempty" maxLength:"500"`
		Body     string         `json:"body,omitempty" maxLength:"200000" doc:"Isi note. Untuk command, disimpan & ditampilkan verbatim."`
		URL      string         `json:"url,omitempty" maxLength:"2048"`
		Tags     []string       `json:"tags,omitempty" maxItems:"20"`
		Project  string         `json:"project,omitempty" maxLength:"100"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}
}

// updateNoteRequest mengikuti JSON merge-patch: field tidak dikirim = tidak diubah,
// null = dikosongkan (untuk title, url, project).
type updateNoteRequest struct {
	NoteIDRequest
	Body struct {
		UpdatedAt *time.Time     `json:"updated_at,omitempty" doc:"Waktu edit di client. Jika lebih lama dari versi server, perubahan diabaikan (last-write-wins) dan note terkini dikembalikan."`
		Type      *string        `json:"type,omitempty" maxLength:"32"`
		Title     *string        `json:"title,omitempty" maxLength:"500"`
		Body      *string        `json:"body,omitempty" maxLength:"200000"`
		URL       *string        `json:"url,omitempty" maxLength:"2048"`
		Tags      *[]string      `json:"tags,omitempty" maxItems:"20"`
		Project   *string        `json:"project,omitempty" maxLength:"100"`
		Metadata  map[string]any `json:"metadata,omitempty"`
	}
	RawBody []byte
}

type listNotesRequest struct {
	Type    string `query:"type"`
	Tag     string `query:"tag" doc:"Cocok dengan tag user maupun auto_tags"`
	Project string `query:"project"`
	Cursor  int64  `query:"cursor" doc:"next_cursor dari halaman sebelumnya"`
	Limit   int    `query:"limit" minimum:"1" maximum:"100" default:"30"`
}

type uploadNoteRequest struct {
	RawBody multipart.Form
}

type noteResponse struct{ Body model.Note }

type noteResultResponse struct{ Body model.NoteResult }

type listNotesResponse struct {
	Body struct {
		Items      []model.Note `json:"items"`
		NextCursor *int64       `json:"next_cursor" doc:"null jika tidak ada halaman berikutnya"`
	}
}

type typesResponse struct {
	Body struct {
		Items []model.TypeCount `json:"items"`
	}
}

// --- Routes ---

func (h *NoteHandler) Register(api huma.API) {
	tags := []string{"Notes"}
	secured := middleware.Secured

	huma.Register(api, huma.Operation{
		OperationID: "notes-list",
		Method:      http.MethodGet,
		Path:        "/notes",
		Summary:     "Daftar note (filter type/tag/project)",
		Tags:        tags,
		Security:    secured,
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID:   "notes-create",
		Method:        http.MethodPost,
		Path:          "/notes",
		Summary:       "Buat note (tipe bebas)",
		Tags:          tags,
		Security:      secured,
		DefaultStatus: http.StatusCreated,
	}, h.create)

	huma.Register(api, huma.Operation{
		OperationID: "notes-upload",
		Method:      http.MethodPost,
		Path:        "/notes/upload",
		Summary:     "Upload dokumen/file",
		Description: fmt.Sprintf(
			"multipart/form-data: `file` (wajib, maks %d MB), `title`, `body`, `type` (default document), "+
				"`project`, `tags` (dipisah koma atau diulang). Teks file (PDF, DOCX, teks/kode) diekstrak async agar bisa dicari.",
			h.maxUploadBytes>>20,
		),
		Tags:          tags,
		Security:      secured,
		DefaultStatus: http.StatusCreated,
	}, h.upload)

	huma.Register(api, huma.Operation{
		OperationID: "notes-get",
		Method:      http.MethodGet,
		Path:        "/notes/{id}",
		Summary:     "Detail note",
		Tags:        tags,
		Security:    secured,
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "notes-update",
		Method:      http.MethodPatch,
		Path:        "/notes/{id}",
		Summary:     "Ubah note (merge-patch, last-write-wins)",
		Tags:        tags,
		Security:    secured,
	}, h.update)

	huma.Register(api, huma.Operation{
		OperationID:   "notes-delete",
		Method:        http.MethodDelete,
		Path:          "/notes/{id}",
		Summary:       "Hapus note (soft delete)",
		Tags:          tags,
		Security:      secured,
		DefaultStatus: http.StatusNoContent,
	}, h.delete)

	huma.Register(api, huma.Operation{
		OperationID:   "notes-reindex",
		Method:        http.MethodPost,
		Path:          "/notes/{id}/reindex",
		Summary:       "Index ulang (auto-tag + embedding)",
		Tags:          tags,
		Security:      secured,
		DefaultStatus: http.StatusAccepted,
	}, h.reindex)

	huma.Register(api, huma.Operation{
		OperationID: "notes-file",
		Method:      http.MethodGet,
		Path:        "/notes/{id}/file",
		Summary:     "Unduh file lampiran",
		Tags:        tags,
		Security:    secured,
	}, h.downloadFile)

	huma.Register(api, huma.Operation{
		OperationID: "types-list",
		Method:      http.MethodGet,
		Path:        "/types",
		Summary:     "Daftar tipe yang dipakai user beserta jumlahnya",
		Tags:        tags,
		Security:    secured,
	}, h.types)
}

func (h *NoteHandler) list(ctx context.Context, in *listNotesRequest) (*listNotesResponse, error) {
	f := model.NoteFilter{Type: in.Type, Tag: in.Tag, Project: in.Project}
	items, next, err := h.notes.List(ctx, middleware.CurrentUser(ctx).ID, f, in.Cursor, in.Limit)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	out := &listNotesResponse{}
	out.Body.Items, out.Body.NextCursor = presentNotes(items), next
	return out, nil
}

func (h *NoteHandler) create(ctx context.Context, in *createNoteRequest) (*noteResultResponse, error) {
	u := middleware.CurrentUser(ctx)
	b := in.Body
	res, err := h.notes.Create(ctx, u.ID, model.CreateNoteInput{
		Type:     b.Type,
		Title:    b.Title,
		Body:     b.Body,
		URL:      b.URL,
		Tags:     b.Tags,
		Project:  b.Project,
		Metadata: b.Metadata,
	})
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.logNote(ctx, "note.create", u.ID, res.Note.ID, map[string]any{"type": res.Note.Type})
	return &noteResultResponse{Body: presentResult(res)}, nil
}

func (h *NoteHandler) upload(ctx context.Context, in *uploadNoteRequest) (*noteResultResponse, error) {
	u := middleware.CurrentUser(ctx)
	files := in.RawBody.File["file"]
	if len(files) != 1 {
		return nil, huma.Error422UnprocessableEntity("wajib tepat satu field 'file'")
	}
	fh := files[0]
	if fh.Size > h.maxUploadBytes {
		return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("ukuran file maksimal %d MB", h.maxUploadBytes>>20))
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	form := in.RawBody
	res, err := h.notes.Upload(ctx, u.ID, model.UploadNoteInput{
		Type:     formValue(form, "type"),
		Title:    formValue(form, "title"),
		Body:     formValue(form, "body"),
		Project:  formValue(form, "project"),
		Tags:     formTags(form),
		Filename: fh.Filename,
		Mime:     fh.Header.Get("Content-Type"),
		File:     f,
	})
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.logNote(ctx, "note.upload", u.ID, res.Note.ID, map[string]any{"type": res.Note.Type, "size": fh.Size})
	return &noteResultResponse{Body: presentResult(res)}, nil
}

func (h *NoteHandler) get(ctx context.Context, in *NoteIDRequest) (*noteResponse, error) {
	n, err := h.notes.Get(ctx, middleware.CurrentUser(ctx).ID, in.ID)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	return &noteResponse{Body: presentNote(n)}, nil
}

func (h *NoteHandler) update(ctx context.Context, in *updateNoteRequest) (*noteResultResponse, error) {
	u := middleware.CurrentUser(ctx)
	b := in.Body
	upd := model.UpdateNoteInput{
		UpdatedAt: b.UpdatedAt,
		Type:      b.Type,
		Title:     b.Title,
		Body:      b.Body,
		URL:       b.URL,
		Tags:      b.Tags,
		Project:   b.Project,
		Metadata:  b.Metadata,
	}
	applyNulls(in.RawBody, map[string]**string{"title": &upd.Title, "url": &upd.URL, "project": &upd.Project})

	res, err := h.notes.Update(ctx, u.ID, in.ID, upd)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.logNote(ctx, "note.update", u.ID, in.ID, nil)
	return &noteResultResponse{Body: presentResult(res)}, nil
}

func (h *NoteHandler) delete(ctx context.Context, in *NoteIDRequest) (*struct{}, error) {
	u := middleware.CurrentUser(ctx)
	if err := h.notes.Delete(ctx, u.ID, in.ID); err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.logNote(ctx, "note.delete", u.ID, in.ID, nil)
	return nil, nil
}

func (h *NoteHandler) reindex(ctx context.Context, in *NoteIDRequest) (*noteResponse, error) {
	n, err := h.notes.Reindex(ctx, middleware.CurrentUser(ctx).ID, in.ID)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	return &noteResponse{Body: presentNote(n)}, nil
}

func (h *NoteHandler) downloadFile(ctx context.Context, in *NoteIDRequest) (*huma.StreamResponse, error) {
	rc, file, err := h.notes.OpenFile(ctx, middleware.CurrentUser(ctx).ID, in.ID)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer rc.Close()
		hctx.SetHeader("Content-Type", file.Mime)
		// Selalu attachment + nosniff: file upload (mis. HTML) tidak boleh dirender di origin app.
		hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": sanitizeFilename(file.Name)}))
		hctx.SetHeader("X-Content-Type-Options", "nosniff")
		hctx.SetHeader("Content-Length", fmt.Sprint(file.Size))
		if _, err := io.Copy(hctx.BodyWriter(), rc); err != nil {
			slog.WarnContext(ctx, "stream file terputus", "note_id", in.ID, "err", err)
		}
	}}, nil
}

func (h *NoteHandler) types(ctx context.Context, _ *struct{}) (*typesResponse, error) {
	items, err := h.notes.Types(ctx, middleware.CurrentUser(ctx).ID)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	out := &typesResponse{}
	out.Body.Items = items
	return out, nil
}

func (h *NoteHandler) logNote(ctx context.Context, action string, userID, noteID int64, meta map[string]any) {
	h.audit.Log(ctx, repository.AuditEntry{
		UserID:   userID,
		Action:   action,
		Entity:   "note",
		EntityID: noteID,
		Metadata: meta,
	})
}

// presentNote melengkapi field yang bergantung pada HTTP, seperti path unduh file.
func presentNote(n model.Note) model.Note {
	if n.File != nil {
		f := *n.File
		f.URL = "/notes/" + strconv.FormatInt(n.ID, 10) + "/file"
		n.File = &f
	}
	return n
}

func presentNotes(notes []model.Note) []model.Note {
	for i := range notes {
		notes[i] = presentNote(notes[i])
	}
	return notes
}

func presentResult(r model.NoteResult) model.NoteResult {
	r.Note = presentNote(r.Note)
	return r
}

// applyNulls mengubah field yang dikirim sebagai null menjadi "" (dikosongkan),
// karena setelah decode, null dan "tidak dikirim" sama-sama menjadi nil.
func applyNulls(raw []byte, fields map[string]**string) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	empty := ""
	for key, ptr := range fields {
		if v, ok := m[key]; ok && string(v) == "null" {
			*ptr = &empty
		}
	}
}

func formValue(f multipart.Form, key string) string {
	if v := f.Value[key]; len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

func formTags(f multipart.Form) []string {
	var out []string
	for _, v := range f.Value["tags"] {
		out = append(out, strings.Split(v, ",")...)
	}
	return out
}

func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r == 0x7f {
			return '_'
		}
		return r
	}, name)
}
