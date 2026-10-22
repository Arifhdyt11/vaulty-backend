package v1

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"vaulty/internal/handler"
	"vaulty/internal/middleware"
	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/internal/service"
)

type itemOut struct{ Body model.Item }

type CaptureBody struct {
	URL      string     `json:"url,omitempty" doc:"Link yang mau disimpan"`
	Text     string     `json:"text,omitempty" doc:"Tulisan bebas"`
	UserNote string     `json:"user_note,omitempty" maxLength:"2000"`
	Tags     []string   `json:"tags,omitempty"`
	RemindAt *time.Time `json:"remind_at,omitempty" doc:"Buat reminder 'lihat lagi' pada waktu ini"`
}

func (h *Handlers) RegisterItems(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "item-capture", Method: http.MethodPost, Path: "/items", Summary: "Simpan link / teks", Description: "Simpan dulu, proses di belakang. Untuk gambar/suara/file pakai POST /items/upload (multipart).", Tags: []string{"items"}, Security: middleware.Secured, DefaultStatus: 202},
		func(ctx context.Context, in *struct{ Body CaptureBody }) (*itemOut, error) {
			it, err := h.Items.Capture(ctx, middleware.UserFrom(ctx), service.CaptureInput{URL: in.Body.URL, Text: in.Body.Text, UserNote: in.Body.UserNote, Tags: in.Body.Tags, RemindAt: in.Body.RemindAt})
			if err != nil {
				return nil, handler.E(err)
			}
			return &itemOut{it}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-upload", Method: http.MethodPost, Path: "/items/upload", Summary: "Simpan gambar / suara / file (multipart)", Description: "Field: file (wajib), user_note, tags (dipisah koma), remind_at (RFC3339), text.", Tags: []string{"items"}, Security: middleware.Secured, DefaultStatus: 202},
		func(ctx context.Context, in *struct {
			RawBody multipart.Form
		}) (*itemOut, error) {
			u := middleware.UserFrom(ctx)
			name, mimeType, data, err := readFile(in.RawBody, "file", service.MaxUploadBytes(u.Plan))
			if err != nil {
				return nil, handler.E(err)
			}
			cin := service.CaptureInput{FileName: name, FileMime: mimeType, File: data, UserNote: formValue(in.RawBody, "user_note"), Text: formValue(in.RawBody, "text"), Tags: splitTags(formValue(in.RawBody, "tags"))}
			if ra := formValue(in.RawBody, "remind_at"); ra != "" {
				t, err := time.Parse(time.RFC3339, ra)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("remind_at harus RFC3339")
				}
				cin.RemindAt = &t
			}
			it, err := h.Items.Capture(ctx, u, cin)
			if err != nil {
				return nil, handler.E(err)
			}
			return &itemOut{it}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-list", Method: http.MethodGet, Path: "/items", Summary: "Library (cursor)", Tags: []string{"items"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Cursor      int64  `query:"cursor"`
			Limit       int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
			Kind        string `query:"kind" enum:",url,image,text,audio,file"`
			Platform    string `query:"platform"`
			Tag         string `query:"tag"`
			Status      string `query:"status" enum:",queued,processing,ready,partial,failed"`
			HasReminder string `query:"has_reminder" enum:",true,false"`
		}) (*struct {
			Body struct {
				Items      []model.Card `json:"items"`
				NextCursor *int64       `json:"next_cursor"`
			}
		}, error) {
			f := repository.ItemFilter{Kind: in.Kind, Platform: in.Platform, Tag: in.Tag, Status: in.Status}
			if in.HasReminder != "" {
				v := in.HasReminder == "true"
				f.HasReminder = &v
			}
			items, next, err := h.Items.List(ctx, middleware.UserFrom(ctx), f, in.Cursor, in.Limit)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Items      []model.Card `json:"items"`
					NextCursor *int64       `json:"next_cursor"`
				}
			}{}
			out.Body.Items, out.Body.NextCursor = items, next
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-updates", Method: http.MethodGet, Path: "/items/updates", Summary: "Item yang berubah sejak waktu tertentu (polling)", Tags: []string{"items"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Since time.Time `query:"since"`
		}) (*struct {
			Body struct {
				Items      []model.Card `json:"items"`
				ServerTime time.Time    `json:"server_time"`
			}
		}, error) {
			items, err := h.Items.UpdatedSince(ctx, middleware.UserFrom(ctx), in.Since)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Items      []model.Card `json:"items"`
					ServerTime time.Time    `json:"server_time"`
				}
			}{}
			out.Body.Items, out.Body.ServerTime = items, time.Now().UTC()
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-get", Method: http.MethodGet, Path: "/items/{id}", Summary: "Detail item", Tags: []string{"items"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*itemOut, error) {
			it, err := h.Items.Get(ctx, middleware.UserFrom(ctx), in.ID)
			if err != nil {
				return nil, handler.E(err)
			}
			return &itemOut{it}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-patch", Method: http.MethodPatch, Path: "/items/{id}", Summary: "Ubah catatan / tag / judul", Tags: []string{"items"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				UserNote *string  `json:"user_note,omitempty"`
				Tags     []string `json:"tags,omitempty"`
				Title    *string  `json:"title,omitempty"`
			}
		}) (*itemOut, error) {
			it, err := h.Items.Patch(ctx, middleware.UserFrom(ctx), in.ID, repository.ItemPatch{UserNote: in.Body.UserNote, Tags: in.Body.Tags, Title: in.Body.Title})
			if err != nil {
				return nil, handler.E(err)
			}
			return &itemOut{it}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-delete", Method: http.MethodDelete, Path: "/items/{id}", Summary: "Hapus (sampah)", Tags: []string{"items"}, Security: middleware.Secured, DefaultStatus: 204},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct{}, error) {
			if err := h.Items.Delete(ctx, middleware.UserFrom(ctx), in.ID); err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-reprocess", Method: http.MethodPost, Path: "/items/{id}/reprocess", Summary: "Proses ulang tahap tertentu", Tags: []string{"items"}, Security: middleware.Secured, DefaultStatus: 202},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Stages []string `json:"stages,omitempty" doc:"fetch|transcribe|understand|index; kosong = semua"`
			}
		}) (*itemOut, error) {
			it, err := h.Items.Reprocess(ctx, middleware.UserFrom(ctx), in.ID, in.Body.Stages)
			if err != nil {
				return nil, handler.E(err)
			}
			return &itemOut{it}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-attachment", Method: http.MethodPost, Path: "/items/{id}/attachments", Summary: "Tambah screenshot pendamping (multipart: file)", Tags: []string{"items"}, Security: middleware.Secured, DefaultStatus: 202},
		func(ctx context.Context, in *struct {
			ID      int64 `path:"id"`
			RawBody multipart.Form
		}) (*itemOut, error) {
			u := middleware.UserFrom(ctx)
			name, mimeType, data, err := readFile(in.RawBody, "file", service.MaxUploadBytes(u.Plan))
			if err != nil {
				return nil, handler.E(err)
			}
			it, err := h.Items.AddAttachment(ctx, u, in.ID, name, mimeType, data)
			if err != nil {
				return nil, handler.E(err)
			}
			return &itemOut{it}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "item-related", Method: http.MethodGet, Path: "/items/{id}/related", Summary: "Item serupa", Tags: []string{"items"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct {
			Body struct {
				Items []model.Card `json:"items"`
			}
		}, error) {
			items, err := h.Items.Related(ctx, middleware.UserFrom(ctx), in.ID)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Items []model.Card `json:"items"`
				}
			}{}
			out.Body.Items = items
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "search", Method: http.MethodGet, Path: "/search", Summary: "Pencarian cepat tanpa LLM", Tags: []string{"items"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Q        string `query:"q" minLength:"1"`
			Limit    int    `query:"limit" default:"20" minimum:"1" maximum:"50"`
			Kind     string `query:"kind"`
			Platform string `query:"platform"`
		}) (*struct {
			Body struct {
				Hits []model.SearchHit `json:"hits"`
			}
		}, error) {
			hits, err := h.Search.Quick(ctx, middleware.UserFrom(ctx), in.Q, repository.ItemFilter{Kind: in.Kind, Platform: in.Platform}, in.Limit)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Hits []model.SearchHit `json:"hits"`
				}
			}{}
			out.Body.Hits = hits
			return out, nil
		})
}

func readFile(f multipart.Form, field string, max int64) (name, mimeType string, data []byte, err error) {
	files := f.File[field]
	if len(files) == 0 {
		return "", "", nil, huma.Error422UnprocessableEntity("field '" + field + "' wajib berisi file")
	}
	fh := files[0]
	if fh.Size > max {
		return "", "", nil, model.ErrTooLarge
	}
	r, err := fh.Open()
	if err != nil {
		return "", "", nil, err
	}
	defer r.Close()
	data, err = io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return "", "", nil, err
	}
	if int64(len(data)) > max {
		return "", "", nil, model.ErrTooLarge
	}
	return fh.Filename, strings.Split(fh.Header.Get("Content-Type"), ";")[0], data, nil
}

func formValue(f multipart.Form, key string) string {
	if v := f.Value[key]; len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

var _ = strconv.Itoa
