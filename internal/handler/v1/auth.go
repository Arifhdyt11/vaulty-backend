// Package v1: handler + request/response /api/v1. Hanya validasi & pemetaan; logika di service.
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

type Handlers struct {
	Auth          *service.AuthService
	Account       *service.AccountService
	Items         *service.ItemService
	Search        *service.SearchService
	Answer        *service.AnswerService
	Remind        *service.ReminderService
	Ent           *service.EntitlementService
	Billing       *service.BillingService
	Briefing      *service.BriefingService
	Repos         RepoAccess
	Admin         *service.AdminService
	Settings      *service.SettingsService
	WebhookSecret string
}

// RepoAccess: akses kecil ke repository untuk endpoint sederhana (devices, conversations).
type RepoAccess interface {
	UpsertDevice(ctx context.Context, userID int64, platform, token, appVersion string) (model.Device, error)
	DeleteDevice(ctx context.Context, userID, id int64) error
	ListConversations(ctx context.Context, userID int64, cursor int64, limit int) ([]model.Conversation, *int64, error)
	Conversation(ctx context.Context, userID, id int64) (model.Conversation, error)
	Messages(ctx context.Context, userID, convID int64, limit int) ([]model.Message, error)
	DeleteConversation(ctx context.Context, userID, id int64) error
}

type AuthResponse struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	ExpiresAt    time.Time  `json:"expires_at"`
	User         model.User `json:"user"`
	Created      bool       `json:"created,omitempty"`
}

func authResp(u model.User, s model.Session, created bool) *AuthResponse {
	return &AuthResponse{AccessToken: s.AccessToken, RefreshToken: s.RefreshToken, ExpiresAt: s.AccessExpiresAt, User: u, Created: created}
}

type authOutput struct {
	Body *AuthResponse
}

func meta(ctx context.Context, ua string) service.ClientMeta {
	return service.ClientMeta{UserAgent: ua, IP: middleware.IPFrom(ctx)}
}

func (h *Handlers) RegisterAuth(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "auth-register", Method: http.MethodPost, Path: "/auth/register", Summary: "Daftar dengan email & password", Tags: []string{"auth"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				Email    string `json:"email" format:"email"`
				Password string `json:"password" minLength:"8" maxLength:"200"`
				Name     string `json:"name" maxLength:"80"`
			}
		}) (*authOutput, error) {
			u, s, err := h.Auth.Register(ctx, in.Body.Email, in.Body.Password, in.Body.Name, meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			return &authOutput{Body: authResp(u, s, true)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/auth/login", Summary: "Login email & password", Tags: []string{"auth"}},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			}
		}) (*authOutput, error) {
			u, s, err := h.Auth.Login(ctx, in.Body.Email, in.Body.Password, meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			return &authOutput{Body: authResp(u, s, false)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-google", Method: http.MethodPost, Path: "/auth/google", Summary: "Login dengan id_token Google", Tags: []string{"auth"}},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				IDToken string `json:"id_token"`
			}
		}) (*authOutput, error) {
			u, s, created, err := h.Auth.LoginWithGoogle(ctx, in.Body.IDToken, meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			return &authOutput{Body: authResp(u, s, created)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-refresh", Method: http.MethodPost, Path: "/auth/refresh", Summary: "Tukar refresh token (rotasi)", Tags: []string{"auth"}},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				RefreshToken string `json:"refresh_token"`
			}
		}) (*authOutput, error) {
			u, s, err := h.Auth.Refresh(ctx, in.Body.RefreshToken, meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			return &authOutput{Body: authResp(u, s, false)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/auth/logout", Summary: "Logout (sesi ini / semua perangkat)", Tags: []string{"auth"}, Security: middleware.Secured, DefaultStatus: 204},
		func(ctx context.Context, in *struct {
			Body struct {
				RefreshToken string `json:"refresh_token,omitempty"`
				AllDevices   bool   `json:"all_devices,omitempty"`
			}
		}) (*struct{}, error) {
			u := middleware.UserFrom(ctx)
			if err := h.Auth.Logout(ctx, u.ID, middleware.TokenFrom(ctx), in.Body.RefreshToken, in.Body.AllDevices); err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})
}

// ---- /me ----

type MeResponse struct {
	User   model.User    `json:"user"`
	Quotas []model.Quota `json:"quotas"`
}

func (h *Handlers) RegisterMe(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "me-get", Method: http.MethodGet, Path: "/me", Summary: "Profil + sisa kuota", Tags: []string{"akun"}, Security: middleware.Secured},
		func(ctx context.Context, _ *struct{}) (*struct{ Body MeResponse }, error) {
			u, q, err := h.Account.Me(ctx, middleware.UserFrom(ctx))
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body MeResponse }{MeResponse{User: u, Quotas: q}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-update", Method: http.MethodPatch, Path: "/me", Summary: "Ubah nama / zona waktu", Tags: []string{"akun"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Body struct {
				Name     *string `json:"name,omitempty"`
				Timezone *string `json:"timezone,omitempty"`
			}
		}) (*struct{ Body model.User }, error) {
			u, err := h.Account.Update(ctx, middleware.UserFrom(ctx), in.Body.Name, in.Body.Timezone)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.User }{u}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-delete", Method: http.MethodDelete, Path: "/me", Summary: "Hapus akun beserta semua data", Tags: []string{"akun"}, Security: middleware.Secured, DefaultStatus: 204},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			if err := h.Account.RequestDelete(ctx, middleware.UserFrom(ctx)); err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-export", Method: http.MethodPost, Path: "/me/export", Summary: "Minta ekspor data (zip)", Tags: []string{"akun"}, Security: middleware.Secured, DefaultStatus: 202},
		func(ctx context.Context, _ *struct{}) (*struct{ Body model.ExportJob }, error) {
			j, err := h.Account.RequestExport(ctx, middleware.UserFrom(ctx))
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.ExportJob }{j}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-export-status", Method: http.MethodGet, Path: "/me/export/{job_id}", Summary: "Status ekspor", Tags: []string{"akun"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			JobID int64 `path:"job_id"`
		}) (*struct{ Body model.ExportJob }, error) {
			j, err := h.Account.ExportStatus(ctx, middleware.UserFrom(ctx), in.JobID)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.ExportJob }{j}, nil
		})
}

// ---- devices ----

func (h *Handlers) RegisterDevices(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "device-register", Method: http.MethodPost, Path: "/devices", Summary: "Daftarkan push token", Tags: []string{"devices"}, Security: middleware.Secured, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			Body struct {
				Platform   string `json:"platform" enum:"ios,android,web"`
				PushToken  string `json:"push_token" minLength:"8"`
				AppVersion string `json:"app_version,omitempty"`
			}
		}) (*struct{ Body model.Device }, error) {
			d, err := h.Repos.UpsertDevice(ctx, middleware.UserFrom(ctx).ID, in.Body.Platform, in.Body.PushToken, in.Body.AppVersion)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.Device }{d}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "device-delete", Method: http.MethodDelete, Path: "/devices/{id}", Summary: "Hapus perangkat", Tags: []string{"devices"}, Security: middleware.Secured, DefaultStatus: 204},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct{}, error) {
			if err := h.Repos.DeleteDevice(ctx, middleware.UserFrom(ctx).ID, in.ID); err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})
}
