package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/handler"
	"vaulty-api/internal/middleware"
	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/internal/service"
)

// ReminderHandler: reminder sekali, berulang, dan bertahap (ADR-023). Notifikasi dikirim worker.
type ReminderHandler struct {
	reminders *service.ReminderService
	audit     *service.AuditService
}

func NewReminderHandler(reminders *service.ReminderService, audit *service.AuditService) *ReminderHandler {
	return &ReminderHandler{reminders: reminders, audit: audit}
}

type ReminderIDRequest struct {
	ID int64 `path:"id"`
}

type createReminderRequest struct {
	Body struct {
		Text        string    `json:"text" minLength:"1" maxLength:"500" example:"Follow up client X"`
		RemindAt    time.Time `json:"remind_at" doc:"Waktu acara (RFC 3339, mis. 2026-10-05T09:00:00+07:00)"`
		Repeat      string    `json:"repeat,omitempty" enum:",daily,weekly,monthly,yearly" doc:"Kosong = sekali"`
		LeadMinutes []int     `json:"lead_minutes,omitempty" maxItems:"5" doc:"Pengingat bertahap sebelum acara (menit): 43200 = H-30, 10080 = H-7, 1440 = H-1"`
		NoteID      *int64    `json:"note_id,omitempty" doc:"Tautkan ke note (opsional)"`
	}
}

type snoozeReminderRequest struct {
	ReminderIDRequest
	Body struct {
		Minutes int `json:"minutes" minimum:"1" maximum:"10080" example:"60"`
	}
}

type listRemindersRequest struct {
	Limit int `query:"limit" minimum:"1" maximum:"100" default:"50"`
}

type reminderResponse struct {
	Body model.Reminder
}

type listRemindersResponse struct {
	Body struct {
		Items []model.Reminder `json:"items"`
	}
}

func (h *ReminderHandler) Register(api huma.API) {
	tags := []string{"Reminders"}
	secured := middleware.Secured

	huma.Register(api, huma.Operation{
		OperationID: "reminders-list", Method: http.MethodGet, Path: "/reminders",
		Summary: "Daftar reminder aktif (notifikasi terdekat dulu)", Tags: tags, Security: secured,
	}, h.list)
	huma.Register(api, huma.Operation{
		OperationID: "reminders-create", Method: http.MethodPost, Path: "/reminders",
		Summary: "Buat reminder (sekali/berulang, opsional pengingat bertahap)", Tags: tags, Security: secured,
		DefaultStatus: http.StatusCreated,
	}, h.create)
	huma.Register(api, huma.Operation{
		OperationID: "reminders-get", Method: http.MethodGet, Path: "/reminders/{id}",
		Summary: "Detail reminder", Tags: tags, Security: secured,
	}, h.get)
	huma.Register(api, huma.Operation{
		OperationID: "reminders-snooze", Method: http.MethodPost, Path: "/reminders/{id}/snooze",
		Summary: "Tunda notifikasi berikutnya", Tags: tags, Security: secured,
	}, h.snooze)
	huma.Register(api, huma.Operation{
		OperationID: "reminders-done", Method: http.MethodPost, Path: "/reminders/{id}/done",
		Summary: "Tandai selesai (reminder berulang tetap lanjut ke jadwal berikutnya)", Tags: tags, Security: secured,
	}, h.done)
	huma.Register(api, huma.Operation{
		OperationID: "reminders-cancel", Method: http.MethodDelete, Path: "/reminders/{id}",
		Summary: "Batalkan reminder beserta pengulangannya", Tags: tags, Security: secured,
		DefaultStatus: http.StatusNoContent,
	}, h.cancel)
}

func (h *ReminderHandler) list(ctx context.Context, in *listRemindersRequest) (*listRemindersResponse, error) {
	items, err := h.reminders.ListUpcoming(ctx, middleware.CurrentUser(ctx).ID, in.Limit)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	out := &listRemindersResponse{}
	out.Body.Items = items
	return out, nil
}

func (h *ReminderHandler) create(ctx context.Context, in *createReminderRequest) (*reminderResponse, error) {
	u := middleware.CurrentUser(ctx)
	r, err := h.reminders.Create(ctx, u.ID, model.CreateReminderInput{
		Text: in.Body.Text, RemindAt: in.Body.RemindAt, Repeat: in.Body.Repeat,
		LeadMinutes: in.Body.LeadMinutes, NoteID: in.Body.NoteID,
	})
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.log(ctx, "reminder.create", u.ID, r.ID)
	return &reminderResponse{Body: r}, nil
}

func (h *ReminderHandler) get(ctx context.Context, in *ReminderIDRequest) (*reminderResponse, error) {
	r, err := h.reminders.Get(ctx, middleware.CurrentUser(ctx).ID, in.ID)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	return &reminderResponse{Body: r}, nil
}

func (h *ReminderHandler) snooze(ctx context.Context, in *snoozeReminderRequest) (*reminderResponse, error) {
	r, err := h.reminders.Snooze(ctx, middleware.CurrentUser(ctx).ID, in.ID, time.Duration(in.Body.Minutes)*time.Minute)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	return &reminderResponse{Body: r}, nil
}

func (h *ReminderHandler) done(ctx context.Context, in *ReminderIDRequest) (*reminderResponse, error) {
	r, err := h.reminders.Done(ctx, middleware.CurrentUser(ctx).ID, in.ID)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	return &reminderResponse{Body: r}, nil
}

func (h *ReminderHandler) cancel(ctx context.Context, in *ReminderIDRequest) (*struct{}, error) {
	u := middleware.CurrentUser(ctx)
	if _, err := h.reminders.Cancel(ctx, u.ID, in.ID); err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.log(ctx, "reminder.cancel", u.ID, in.ID)
	return nil, nil
}

func (h *ReminderHandler) log(ctx context.Context, action string, userID, id int64) {
	h.audit.Log(ctx, repository.AuditEntry{UserID: userID, Action: action, Entity: "reminder", EntityID: id})
}
