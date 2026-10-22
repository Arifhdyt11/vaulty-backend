package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"vaulty/internal/handler"
	"vaulty/internal/middleware"
	"vaulty/internal/model"
	"vaulty/internal/service"
)

type reminderOut struct{ Body model.Reminder }

type ReminderCreated struct {
	model.Reminder
	Parsed *service.ParsedReminder `json:"parsed,omitempty"`
}

func (h *Handlers) RegisterReminders(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "reminder-create", Method: http.MethodPost, Path: "/reminders", Summary: "Buat reminder (kalimat natural atau terstruktur)", Tags: []string{"reminders"}, Security: middleware.Secured, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			Body struct {
				Text        string     `json:"text" minLength:"1" maxLength:"500"`
				ItemID      *int64     `json:"item_id,omitempty"`
				RemindAt    *time.Time `json:"remind_at,omitempty" doc:"Kosong = text diparse Vee"`
				Repeat      string     `json:"repeat,omitempty" enum:",daily,weekly,monthly,yearly"`
				LeadMinutes []int32    `json:"lead_minutes,omitempty"`
			}
		}) (*struct{ Body ReminderCreated }, error) {
			u := middleware.UserFrom(ctx)
			if in.Body.RemindAt == nil {
				r, p, err := h.Remind.CreateFromText(ctx, u, in.Body.Text, in.Body.ItemID)
				if err != nil {
					return nil, handler.E(err)
				}
				return &struct{ Body ReminderCreated }{ReminderCreated{Reminder: r, Parsed: p}}, nil
			}
			origin := "user"
			if in.Body.ItemID != nil {
				origin = "item"
			}
			r, err := h.Remind.CreateStructured(ctx, u, service.ReminderInput{ItemID: in.Body.ItemID, Text: in.Body.Text, RemindAt: *in.Body.RemindAt, Repeat: in.Body.Repeat, LeadMinutes: in.Body.LeadMinutes, Origin: origin})
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body ReminderCreated }{ReminderCreated{Reminder: r}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "reminder-list", Method: http.MethodGet, Path: "/reminders", Summary: "Daftar reminder", Tags: []string{"reminders"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Status string `query:"status" enum:",upcoming,suggested,done,all" default:"upcoming"`
			ItemID int64  `query:"item_id" doc:"0 = semua"`
		}) (*struct {
			Body struct {
				Reminders []model.Reminder `json:"reminders"`
			}
		}, error) {
			var itemID *int64
			if in.ItemID > 0 {
				itemID = &in.ItemID
			}
			rs, err := h.Remind.List(ctx, middleware.UserFrom(ctx), in.Status, itemID)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Reminders []model.Reminder `json:"reminders"`
				}
			}{}
			out.Body.Reminders = rs
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "reminder-get", Method: http.MethodGet, Path: "/reminders/{id}", Summary: "Detail reminder", Tags: []string{"reminders"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*reminderOut, error) {
			r, err := h.Remind.Get(ctx, middleware.UserFrom(ctx), in.ID)
			if err != nil {
				return nil, handler.E(err)
			}
			return &reminderOut{r}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "reminder-patch", Method: http.MethodPatch, Path: "/reminders/{id}", Summary: "Ubah reminder", Tags: []string{"reminders"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Text        *string    `json:"text,omitempty"`
				RemindAt    *time.Time `json:"remind_at,omitempty"`
				Repeat      *string    `json:"repeat,omitempty"`
				LeadMinutes []int32    `json:"lead_minutes,omitempty"`
			}
		}) (*reminderOut, error) {
			r, err := h.Remind.Update(ctx, middleware.UserFrom(ctx), in.ID, service.ReminderPatchInput{Text: in.Body.Text, RemindAt: in.Body.RemindAt, Repeat: in.Body.Repeat, LeadMinutes: in.Body.LeadMinutes})
			if err != nil {
				return nil, handler.E(err)
			}
			return &reminderOut{r}, nil
		})

	action := func(id, path, summary string, fn func(ctx context.Context, u model.User, rid int64) (model.Reminder, error)) {
		huma.Register(api, huma.Operation{OperationID: id, Method: http.MethodPost, Path: path, Summary: summary, Tags: []string{"reminders"}, Security: middleware.Secured},
			func(ctx context.Context, in *struct {
				ID int64 `path:"id"`
			}) (*reminderOut, error) {
				r, err := fn(ctx, middleware.UserFrom(ctx), in.ID)
				if err != nil {
					return nil, handler.E(err)
				}
				return &reminderOut{r}, nil
			})
	}
	action("reminder-confirm", "/reminders/{id}/confirm", "Konfirmasi usulan AI", h.Remind.Confirm)
	action("reminder-done", "/reminders/{id}/done", "Tandai selesai", h.Remind.Done)
	action("reminder-cancel", "/reminders/{id}/cancel", "Batalkan", h.Remind.Cancel)

	huma.Register(api, huma.Operation{OperationID: "reminder-snooze", Method: http.MethodPost, Path: "/reminders/{id}/snooze", Summary: "Tunda", Tags: []string{"reminders"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Minutes  int  `json:"minutes,omitempty" minimum:"0" maximum:"43200"`
				Tomorrow bool `json:"tomorrow,omitempty" doc:"true = besok jam 9 pagi waktu user"`
			}
		}) (*reminderOut, error) {
			u := middleware.UserFrom(ctx)
			var r model.Reminder
			var err error
			if in.Body.Tomorrow || in.Body.Minutes == 0 {
				r, err = h.Remind.SnoozeUntilTomorrow(ctx, u, in.ID)
			} else {
				r, err = h.Remind.Snooze(ctx, u, in.ID, time.Duration(in.Body.Minutes)*time.Minute)
			}
			if err != nil {
				return nil, handler.E(err)
			}
			return &reminderOut{r}, nil
		})
}

// ---- billing ----

func (h *Handlers) RegisterBilling(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "billing-plans", Method: http.MethodGet, Path: "/billing/plans", Summary: "Paket & batas", Tags: []string{"billing"}, Security: middleware.Secured},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Plans []service.PlanInfo `json:"plans"`
			}
		}, error) {
			plans, err := h.Ent.Plans(ctx)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Plans []service.PlanInfo `json:"plans"`
				}
			}{}
			out.Body.Plans = plans
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "billing-sync", Method: http.MethodPost, Path: "/billing/sync", Summary: "Sinkron langganan dari RevenueCat", Tags: []string{"billing"}, Security: middleware.Secured},
		func(ctx context.Context, _ *struct {
			Body struct {
				AppUserID string `json:"app_user_id,omitempty"`
			}
		}) (*struct {
			Body struct {
				Plan string `json:"plan"`
			}
		}, error) {
			plan, err := h.Billing.SyncFromRevenueCat(ctx, middleware.UserFrom(ctx))
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Plan string `json:"plan"`
				}
			}{}
			out.Body.Plan = plan
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "billing-webhook-revenuecat", Method: http.MethodPost, Path: "/billing/webhook/revenuecat", Summary: "Webhook RevenueCat", Tags: []string{"billing"}},
		func(ctx context.Context, in *struct {
			Authorization string `header:"Authorization"`
			RawBody       []byte
		}) (*struct{}, error) {
			if h.WebhookSecret == "" || in.Authorization != "Bearer "+h.WebhookSecret {
				return nil, huma.Error401Unauthorized("webhook tidak sah")
			}
			if err := h.Billing.HandleRevenueCatWebhook(ctx, in.RawBody); err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})
}
