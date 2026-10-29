package v1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"vaulty/internal/handler"
	"vaulty/internal/middleware"
	"vaulty/internal/model"
)

type messageOut struct {
	Body struct {
		Message string `json:"message"`
	}
}

func msg(s string) *messageOut { o := &messageOut{}; o.Body.Message = s; return o }

// RegisterEmailAuth: verifikasi email, lupa & ganti password dengan OTP (ADR-022).
func (h *Handlers) RegisterEmailAuth(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "auth-verify-send", Method: http.MethodPost, Path: "/auth/email/verify/send", Summary: "Kirim (ulang) kode verifikasi ke email sendiri", Tags: []string{"auth"}, Security: middleware.Secured, DefaultStatus: 202},
		func(ctx context.Context, _ *struct{}) (*messageOut, error) {
			u := middleware.UserFrom(ctx)
			if err := h.Auth.SendVerification(ctx, u); err != nil {
				return nil, handler.E(err)
			}
			return msg("Kode dikirim ke " + u.Email), nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-verify", Method: http.MethodPost, Path: "/auth/email/verify", Summary: "Verifikasi email dengan kode 6 digit", Tags: []string{"auth"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				Code string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
			}
		}) (*struct {
			Body struct {
				User model.User `json:"user"`
			}
		}, error) {
			u, err := h.Auth.VerifyEmail(ctx, middleware.UserFrom(ctx), in.Body.Code, meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			out := &struct {
				Body struct {
					User model.User `json:"user"`
				}
			}{}
			out.Body.User = u
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-password-forgot", Method: http.MethodPost, Path: "/auth/password/forgot", Summary: "Minta kode reset password (selalu 202 supaya email terdaftar tidak bocor)", Tags: []string{"auth"}, DefaultStatus: 202},
		func(ctx context.Context, in *struct {
			Body struct {
				Email string `json:"email" maxLength:"254"`
			}
		}) (*messageOut, error) {
			if err := h.Auth.ForgotPassword(ctx, in.Body.Email); err != nil {
				return nil, handler.E(err)
			}
			return msg("Kalau email itu terdaftar, kode reset sudah dikirim."), nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-password-reset", Method: http.MethodPost, Path: "/auth/password/reset", Summary: "Ganti password dengan kode reset; semua sesi lama keluar, lalu login", Tags: []string{"auth"}},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				Email    string `json:"email" maxLength:"254"`
				Code     string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
				Password string `json:"password" minLength:"8" maxLength:"128"`
			}
		}) (*authOutput, error) {
			u, s, err := h.Auth.ResetPassword(ctx, in.Body.Email, in.Body.Code, in.Body.Password, meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			return &authOutput{Body: authResp(u, s, false)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-password", Method: http.MethodPost, Path: "/me/password", Summary: "Ganti password (akun Google: buat password tanpa password lama); perangkat lain keluar", Tags: []string{"akun"}, Security: middleware.Secured, DefaultStatus: 204},
		func(ctx context.Context, in *struct {
			UserAgent string `header:"User-Agent"`
			Body      struct {
				CurrentPassword string `json:"current_password,omitempty" maxLength:"128"`
				NewPassword     string `json:"new_password" minLength:"8" maxLength:"128"`
			}
		}) (*struct{}, error) {
			err := h.Auth.ChangePassword(ctx, middleware.UserFrom(ctx), in.Body.CurrentPassword, in.Body.NewPassword, middleware.TokenFrom(ctx), meta(ctx, in.UserAgent))
			if err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})
}
