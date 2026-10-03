// Package middleware berisi middleware HTTP: autentikasi Bearer dan batas ukuran body.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/model"
	"vaulty-api/internal/service"
)

// BearerAuth: semua client (BFF NextAuth di web, iOS) mengirim Authorization: Bearer (ADR-015).
const BearerAuth = "bearerAuth"

// Secured dipasang di huma.Operation.Security untuk route yang butuh login.
var Secured = []map[string][]string{{BearerAuth: {}}}

type userKey struct{}

// CurrentUser mengambil user yang login. Hanya dipanggil di route yang Secured,
// sehingga middleware Auth sudah menjamin user ada.
func CurrentUser(ctx context.Context) model.User {
	u, _ := ctx.Value(userKey{}).(model.User)
	return u
}

// BearerToken mengambil token dari header Authorization ("" jika tidak ada).
func BearerToken(header string) string {
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// Auth mengisi user & role ke context dari token Bearer, dan menolak (401) route Secured
// jika token tidak valid atau kedaluwarsa.
func Auth(api huma.API, auth *service.AuthService) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		var user *model.User
		if token := BearerToken(ctx.Header("Authorization")); token != "" {
			if u, err := auth.UserByToken(ctx.Context(), token); err == nil {
				user = &u
			}
		}
		op := ctx.Operation()
		if user == nil && op != nil && len(op.Security) > 0 {
			if err := huma.WriteErr(api, ctx, http.StatusUnauthorized, "token tidak valid atau kedaluwarsa"); err != nil {
				slog.Error("tulis respons 401 gagal", "err", err)
			}
			return
		}
		if user != nil {
			ctx = huma.WithValue(ctx, userKey{}, *user)
		}
		next(ctx)
	}
}
