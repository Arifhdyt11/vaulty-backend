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

// Konsol admin web (ADR-021). Semua operasi /admin hanya untuk role admin.

func adminOp(id, method, path, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"admin"}, Security: middleware.Secured}
}

// requireAdmin dipanggil di awal setiap handler /admin.
func (h *Handlers) requireAdmin(ctx context.Context) (model.User, error) {
	u := middleware.UserFrom(ctx)
	if u.Role != "admin" {
		return u, handler.E(model.ErrForbidden)
	}
	if h.Admin == nil {
		return u, huma.Error503ServiceUnavailable("konsol admin belum tersedia")
	}
	return u, nil
}

type okOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

func ok() *okOut { o := &okOut{}; o.Body.OK = true; return o }

// Parameter ditulis langsung di tiap struct input: huma tidak membaca tag dari struct embedded.
func offset(page, limit int) int { return (max(page, 1) - 1) * limit }

type userIDIn struct {
	ID int64 `path:"id"`
}

func (h *Handlers) RegisterAdmin(api huma.API) {
	huma.Register(api, adminOp("admin-overview", http.MethodGet, "/admin/overview", "Ringkasan platform"),
		func(ctx context.Context, in *struct {
			Days int `query:"days" minimum:"7" maximum:"90" default:"14"`
		}) (*struct{ Body model.AdminOverview }, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			o, err := h.Admin.Overview(ctx, in.Days)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.AdminOverview }{o}, nil
		})

	// ---- pengguna ----

	type usersOut struct {
		Body struct {
			Users []model.AdminUserRow `json:"users"`
			Total int                  `json:"total"`
		}
	}
	huma.Register(api, adminOp("admin-users", http.MethodGet, "/admin/users", "Daftar pengguna"),
		func(ctx context.Context, in *struct {
			Page   int    `query:"page" minimum:"1" default:"1"`
			Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"20"`
			Q      string `query:"q" maxLength:"120"`
			Plan   string `query:"plan" enum:"free,pro,"`
			Role   string `query:"role" enum:"user,admin,"`
			Status string `query:"status" enum:"active,suspended,"`
		}) (*usersOut, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			list, total, err := h.Admin.Users(ctx, model.AdminUserFilter{Q: in.Q, Plan: in.Plan, Role: in.Role, Status: in.Status, Limit: in.Limit, Offset: offset(in.Page, in.Limit)})
			if err != nil {
				return nil, handler.E(err)
			}
			out := &usersOut{}
			out.Body.Users, out.Body.Total = list, total
			return out, nil
		})

	huma.Register(api, adminOp("admin-user", http.MethodGet, "/admin/users/{id}", "Detail pengguna"),
		func(ctx context.Context, in *userIDIn) (*struct{ Body model.AdminUserDetail }, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			d, err := h.Admin.User(ctx, in.ID)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.AdminUserDetail }{d}, nil
		})

	huma.Register(api, adminOp("admin-user-update", http.MethodPatch, "/admin/users/{id}", "Ubah role / nonaktifkan akun / tandai email terverifikasi"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Role          *string `json:"role,omitempty" enum:"user,admin"`
				Suspended     *bool   `json:"suspended,omitempty"`
				EmailVerified *bool   `json:"email_verified,omitempty"`
			}
		}) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			p := service.AdminUserPatch{Role: in.Body.Role, Suspended: in.Body.Suspended, EmailVerified: in.Body.EmailVerified}
			if err := h.Admin.UpdateUser(ctx, admin, in.ID, p); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	huma.Register(api, adminOp("admin-user-password", http.MethodPost, "/admin/users/{id}/password", "Ganti password pengguna; semua sesinya keluar"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Password string `json:"password" minLength:"8" maxLength:"128"`
			}
		}) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.SetPassword(ctx, admin, in.ID, in.Body.Password); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	huma.Register(api, adminOp("admin-user-email", http.MethodPost, "/admin/users/{id}/email", "Kirim kode verifikasi email atau reset password ke pengguna"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Kind string `json:"kind" enum:"verify,reset"`
			}
		}) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.SendEmail(ctx, admin, in.ID, in.Body.Kind); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	huma.Register(api, adminOp("admin-user-pro-grant", http.MethodPost, "/admin/users/{id}/pro", "Beri Pro manual"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Days int `json:"days" minimum:"1" maximum:"3650"`
			}
		}) (*struct {
			Body struct {
				Until time.Time `json:"until"`
			}
		}, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			end, err := h.Admin.GrantPro(ctx, admin, in.ID, in.Body.Days)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Until time.Time `json:"until"`
				}
			}{}
			out.Body.Until = end
			return out, nil
		})

	huma.Register(api, adminOp("admin-user-pro-revoke", http.MethodDelete, "/admin/users/{id}/pro", "Cabut Pro manual"),
		func(ctx context.Context, in *userIDIn) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.RevokePro(ctx, admin, in.ID); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	huma.Register(api, adminOp("admin-user-quota-reset", http.MethodPost, "/admin/users/{id}/quota/reset", "Reset pemakaian kuota periode berjalan"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Metric string `json:"metric,omitempty" doc:"Kosong = semua kuota"`
			}
		}) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.ResetQuota(ctx, admin, in.ID, in.Body.Metric); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	huma.Register(api, adminOp("admin-user-quota-bonus", http.MethodPost, "/admin/users/{id}/quota/bonus", "Tambah kuota periode berjalan"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Metric string `json:"metric"`
				Amount int    `json:"amount" doc:"Boleh negatif untuk mengurangi bonus"`
			}
		}) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.AddBonus(ctx, admin, in.ID, in.Body.Metric, in.Body.Amount); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	// ---- pembayaran ----

	type subsOut struct {
		Body struct {
			Subscriptions []model.AdminSubscription      `json:"subscriptions"`
			Total         int                            `json:"total"`
			Summary       model.AdminSubscriptionSummary `json:"summary"`
		}
	}
	huma.Register(api, adminOp("admin-subscriptions", http.MethodGet, "/admin/subscriptions", "Langganan & pembayaran"),
		func(ctx context.Context, in *struct {
			Page     int    `query:"page" minimum:"1" default:"1"`
			Limit    int    `query:"limit" minimum:"1" maximum:"100" default:"20"`
			Q        string `query:"q" maxLength:"120"`
			Status   string `query:"status" enum:"active,grace,expired,cancelled,"`
			Provider string `query:"provider" enum:"revenuecat,stripe,midtrans,manual,"`
		}) (*subsOut, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			list, total, sum, err := h.Admin.Subscriptions(ctx, model.AdminSubscriptionFilter{Q: in.Q, Status: in.Status, Provider: in.Provider, Limit: in.Limit, Offset: offset(in.Page, in.Limit)})
			if err != nil {
				return nil, handler.E(err)
			}
			out := &subsOut{}
			out.Body.Subscriptions, out.Body.Total, out.Body.Summary = list, total, sum
			return out, nil
		})

	// ---- kupon ----

	type couponOut struct{ Body model.Coupon }
	huma.Register(api, adminOp("admin-coupons", http.MethodGet, "/admin/coupons", "Daftar kupon"),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Coupons []model.Coupon `json:"coupons"`
			}
		}, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			list, err := h.Admin.Coupons(ctx)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Coupons []model.Coupon `json:"coupons"`
				}
			}{}
			out.Body.Coupons = list
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "admin-coupon-create", Method: http.MethodPost, Path: "/admin/coupons", Summary: "Buat kupon", Tags: []string{"admin"}, Security: middleware.Secured, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			Body struct {
				Code           string     `json:"code" minLength:"3" maxLength:"32"`
				Description    string     `json:"description,omitempty" maxLength:"200"`
				Kind           string     `json:"kind" enum:"pro_days,bonus"`
				Metric         string     `json:"metric,omitempty"`
				Amount         int        `json:"amount" minimum:"1"`
				MaxRedemptions int        `json:"max_redemptions,omitempty" minimum:"0"`
				StartsAt       *time.Time `json:"starts_at,omitempty"`
				ExpiresAt      *time.Time `json:"expires_at,omitempty"`
			}
		}) (*couponOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			c, err := h.Admin.CreateCoupon(ctx, admin, model.Coupon{Code: b.Code, Description: b.Description, Kind: b.Kind, Metric: b.Metric, Amount: b.Amount,
				MaxRedemptions: b.MaxRedemptions, StartsAt: b.StartsAt, ExpiresAt: b.ExpiresAt, Active: true})
			if err != nil {
				return nil, handler.E(err)
			}
			return &couponOut{c}, nil
		})

	huma.Register(api, adminOp("admin-coupon-update", http.MethodPatch, "/admin/coupons/{id}", "Aktifkan / nonaktifkan kupon"),
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Active bool `json:"active"`
			}
		}) (*couponOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			c, err := h.Admin.SetCouponActive(ctx, admin, in.ID, in.Body.Active)
			if err != nil {
				return nil, handler.E(err)
			}
			return &couponOut{c}, nil
		})

	huma.Register(api, adminOp("admin-coupon-delete", http.MethodDelete, "/admin/coupons/{id}", "Hapus kupon yang belum dipakai"),
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*okOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.DeleteCoupon(ctx, admin, in.ID); err != nil {
				return nil, handler.E(err)
			}
			return ok(), nil
		})

	// ---- pengaturan & paket ----

	type settingsOut struct{ Body service.Settings }
	huma.Register(api, adminOp("admin-settings", http.MethodGet, "/admin/settings", "Pengaturan platform"),
		func(ctx context.Context, _ *struct{}) (*settingsOut, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			return &settingsOut{h.Admin.Settings(ctx)}, nil
		})

	huma.Register(api, adminOp("admin-settings-update", http.MethodPatch, "/admin/settings", "Ubah pengaturan platform"),
		func(ctx context.Context, in *struct {
			Body struct {
				ProEnabled *bool `json:"pro_enabled,omitempty"`
			}
		}) (*settingsOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if in.Body.ProEnabled != nil {
				if err := h.Admin.SetProEnabled(ctx, admin, *in.Body.ProEnabled); err != nil {
					return nil, handler.E(err)
				}
			}
			return &settingsOut{h.Admin.Settings(ctx)}, nil
		})

	type plansOut struct {
		Body struct {
			Plans []service.AdminPlan `json:"plans"`
		}
	}
	huma.Register(api, adminOp("admin-plans", http.MethodGet, "/admin/plans", "Paket, limit, dan aturan"),
		func(ctx context.Context, _ *struct{}) (*plansOut, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			plans, err := h.Admin.Plans(ctx)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &plansOut{}
			out.Body.Plans = plans
			return out, nil
		})

	huma.Register(api, adminOp("admin-plan-update", http.MethodPut, "/admin/plans/{plan}", "Ubah limit & aturan satu paket"),
		func(ctx context.Context, in *struct {
			Plan string `path:"plan" enum:"free,pro"`
			Body struct {
				Limits []service.PlanLimitInput `json:"limits,omitempty"`
				Rules  *service.PlanRulesConfig `json:"rules,omitempty"`
			}
		}) (*plansOut, error) {
			admin, err := h.requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := h.Admin.UpdatePlan(ctx, admin, in.Plan, in.Body.Limits, in.Body.Rules); err != nil {
				return nil, handler.E(err)
			}
			plans, err := h.Admin.Plans(ctx)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &plansOut{}
			out.Body.Plans = plans
			return out, nil
		})

	// ---- laporan ----

	huma.Register(api, adminOp("admin-reports", http.MethodGet, "/admin/reports", "Laporan pertumbuhan & pemakaian"),
		func(ctx context.Context, in *struct {
			Days int `query:"days" minimum:"7" maximum:"365" default:"30"`
		}) (*struct{ Body model.AdminReport }, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			r, err := h.Admin.Report(ctx, in.Days)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.AdminReport }{r}, nil
		})

	huma.Register(api, adminOp("admin-audit", http.MethodGet, "/admin/audit", "Log aksi admin"),
		func(ctx context.Context, in *struct {
			Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
		}) (*struct {
			Body struct {
				Entries []model.AdminAuditEntry `json:"entries"`
			}
		}, error) {
			if _, err := h.requireAdmin(ctx); err != nil {
				return nil, err
			}
			list, err := h.Admin.Audit(ctx, in.Limit)
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					Entries []model.AdminAuditEntry `json:"entries"`
				}
			}{}
			out.Body.Entries = list
			return out, nil
		})
}

// RegisterPublicConfig: konfigurasi publik untuk landing page & app (tanpa login).
func (h *Handlers) RegisterPublicConfig(api huma.API) {
	type configOut struct {
		CacheControl string `header:"Cache-Control"`
		Body         struct {
			ProEnabled   bool               `json:"pro_enabled" doc:"false = sembunyikan semua UI upgrade/Pro"`
			EmailEnabled bool               `json:"email_enabled" doc:"false = server tanpa SMTP: jangan minta verifikasi email, sembunyikan lupa password"`
			Plans        []service.PlanInfo `json:"plans" doc:"Paket yang ditawarkan; tanpa pro bila Pro ditutup"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "config", Method: http.MethodGet, Path: "/config", Summary: "Konfigurasi publik: fitur Pro dibuka atau tidak, limit paket", Tags: []string{"config"}},
		func(ctx context.Context, _ *struct{}) (*configOut, error) {
			out := &configOut{CacheControl: "public, max-age=60"}
			out.Body.ProEnabled = h.Settings == nil || h.Settings.ProEnabled(ctx)
			out.Body.EmailEnabled = h.Auth.EmailEnabled()
			plans, err := h.Ent.Plans(ctx)
			if err != nil {
				return nil, handler.E(err)
			}
			out.Body.Plans = []service.PlanInfo{}
			for _, p := range plans {
				if p.Plan == "pro" && !out.Body.ProEnabled {
					continue
				}
				out.Body.Plans = append(out.Body.Plans, p)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "coupon-redeem", Method: http.MethodPost, Path: "/coupons/redeem", Summary: "Tukar kode kupon", Tags: []string{"billing"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Body struct {
				Code string `json:"code" minLength:"1" maxLength:"32"`
			}
		}) (*struct{ Body service.RedeemResult }, error) {
			if h.Admin == nil {
				return nil, huma.Error503ServiceUnavailable("kupon belum tersedia")
			}
			r, err := h.Admin.RedeemCoupon(ctx, middleware.UserFrom(ctx), in.Body.Code)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body service.RedeemResult }{r}, nil
		})
}
