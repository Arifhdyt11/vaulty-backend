// Package v1 berisi handler HTTP untuk /api/v1: definisi request/response (kontrak v1)
// dan pendaftaran route ke huma. Path didaftarkan relatif; prefix dipasang router.
package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/handler"
	"vaulty-api/internal/middleware"
	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
	"vaulty-api/internal/service"
)

type AuthHandler struct {
	auth  *service.AuthService
	audit *service.AuditService
}

func NewAuthHandler(auth *service.AuthService, audit *service.AuditService) *AuthHandler {
	return &AuthHandler{auth: auth, audit: audit}
}

// --- Request & response ---

// ClientMeta di-embed di request auth. Harus tipe exported: huma tidak membaca field dari
// embedded struct yang unexported.
type ClientMeta struct {
	UserAgent string `header:"User-Agent"`
	RealIP    string `header:"X-Real-IP"`
}

func (m ClientMeta) toService() service.ClientMeta {
	return service.ClientMeta{UserAgent: m.UserAgent, IP: m.RealIP}
}

type registerRequest struct {
	ClientMeta
	Body struct {
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" minLength:"8" maxLength:"128"`
		Name     string `json:"name,omitempty" maxLength:"100"`
	}
}

type loginRequest struct {
	ClientMeta
	Body struct {
		Email    string `json:"email" maxLength:"254"`
		Password string `json:"password" maxLength:"128"`
	}
}

type googleLoginRequest struct {
	ClientMeta
	Body struct {
		IDToken string `json:"id_token" minLength:"1" doc:"id_token dari Google (NextAuth di web, Google Sign-In di iOS)"`
	}
}

type logoutRequest struct {
	Authorization string `header:"Authorization"`
}

type sessionResponse struct{ Body model.Session }

type meResponse struct {
	Body struct {
		User model.User `json:"user"`
	}
}

// --- Routes ---

func (h *AuthHandler) Register(api huma.API) {
	tags := []string{"Auth"}

	huma.Register(api, huma.Operation{
		OperationID:   "auth-register",
		Method:        http.MethodPost,
		Path:          "/auth/register",
		Summary:       "Daftar akun baru (role user)",
		Description:   "Selama registrasi belum dibuka (REGISTRATION_OPEN=false), hanya email di ALLOWED_EMAILS yang bisa mendaftar; selain itu 403.",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
	}, h.register)

	huma.Register(api, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Login email & password",
		Description: "401 jika email/password salah.",
		Tags:        tags,
	}, h.login)

	huma.Register(api, huma.Operation{
		OperationID: "auth-google",
		Method:      http.MethodPost,
		Path:        "/auth/google",
		Summary:     "Login dengan id_token Google",
		Description: "Memverifikasi id_token (tanda tangan, issuer, audience = GOOGLE_CLIENT_IDS). " +
			"401 jika token tidak valid; 403 jika email belum terverifikasi atau registrasi belum dibuka untuk email tersebut.",
		Tags: tags,
	}, h.loginGoogle)

	huma.Register(api, huma.Operation{
		OperationID:   "auth-logout",
		Method:        http.MethodPost,
		Path:          "/auth/logout",
		Summary:       "Cabut access token yang sedang dipakai",
		Tags:          tags,
		Security:      middleware.Secured,
		DefaultStatus: http.StatusNoContent,
	}, h.logout)

	huma.Register(api, huma.Operation{
		OperationID: "auth-me",
		Method:      http.MethodGet,
		Path:        "/auth/me",
		Summary:     "User yang sedang login",
		Tags:        tags,
		Security:    middleware.Secured,
	}, h.me)
}

func (h *AuthHandler) register(ctx context.Context, in *registerRequest) (*sessionResponse, error) {
	sess, err := h.auth.Register(ctx, in.Body.Email, in.Body.Password, in.Body.Name, in.toService())
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.logAuth(ctx, "auth.register", sess.User.ID, in.RealIP)
	return &sessionResponse{Body: sess}, nil
}

func (h *AuthHandler) login(ctx context.Context, in *loginRequest) (*sessionResponse, error) {
	sess, err := h.auth.Login(ctx, in.Body.Email, in.Body.Password, in.toService())
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	h.logAuth(ctx, "auth.login", sess.User.ID, in.RealIP)
	return &sessionResponse{Body: sess}, nil
}

func (h *AuthHandler) loginGoogle(ctx context.Context, in *googleLoginRequest) (*sessionResponse, error) {
	sess, created, err := h.auth.LoginWithGoogle(ctx, in.Body.IDToken, in.toService())
	if err != nil {
		if errors.Is(err, model.ErrInvalidGoogleToken) {
			slog.WarnContext(ctx, "id_token Google ditolak", "err", err)
		}
		return nil, handler.ToHTTPError(err)
	}
	action := "auth.login_google"
	if created {
		action = "auth.register_google"
	}
	h.logAuth(ctx, action, sess.User.ID, in.RealIP)
	return &sessionResponse{Body: sess}, nil
}

func (h *AuthHandler) logout(ctx context.Context, in *logoutRequest) (*struct{}, error) {
	if err := h.auth.Logout(ctx, middleware.BearerToken(in.Authorization)); err != nil {
		return nil, err
	}
	h.logAuth(ctx, "auth.logout", middleware.CurrentUser(ctx).ID, "")
	return nil, nil
}

func (h *AuthHandler) me(ctx context.Context, _ *struct{}) (*meResponse, error) {
	out := &meResponse{}
	out.Body.User = middleware.CurrentUser(ctx)
	return out, nil
}

func (h *AuthHandler) logAuth(ctx context.Context, action string, userID int64, ip string) {
	h.audit.Log(ctx, repository.AuditEntry{
		UserID:   userID,
		Action:   action,
		Entity:   "user",
		EntityID: userID,
		IP:       ip,
	})
}
