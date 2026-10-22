// Package middleware: auth Bearer, request id, access log.
package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"vaulty/internal/model"
)

type ctxKey int

const (
	keyUser ctxKey = iota
	keyToken
	keyRequestID
	keyIP
)

type UserResolver interface {
	UserByToken(ctx context.Context, token string) (model.User, error)
}

// Secured adalah nilai Security untuk operasi yang butuh Bearer.
var Secured = []map[string][]string{{"bearer": {}}}

// Auth: huma middleware. Operasi tanpa Security dilewati; yang punya → wajib token valid.
func Auth(api huma.API, users UserResolver) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if len(ctx.Operation().Security) == 0 {
			next(ctx)
			return
		}
		h := ctx.Header("Authorization")
		if !strings.HasPrefix(strings.ToLower(h), "bearer ") || len(h) < 8 {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "butuh Bearer token")
			return
		}
		tok := strings.TrimSpace(h[7:])
		u, err := users.UserByToken(ctx.Context(), tok)
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, model.ErrUnauthorized.Error())
			return
		}
		c := context.WithValue(ctx.Context(), keyUser, u)
		c = context.WithValue(c, keyToken, tok)
		next(huma.WithContext(ctx, c))
	}
}

func UserFrom(ctx context.Context) model.User {
	u, _ := ctx.Value(keyUser).(model.User)
	return u
}

func TokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(keyToken).(string)
	return t
}

func RequestIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(keyRequestID).(string)
	return s
}

func IPFrom(ctx context.Context) string {
	s, _ := ctx.Value(keyIP).(string)
	return s
}

// RequestID + access log (chi middleware biasa).
func RequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ip := clientIP(r)
		ctx := context.WithValue(r.Context(), keyRequestID, id)
		ctx = context.WithValue(ctx, keyIP, ip)
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r.WithContext(ctx))
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds(), "req_id", id, "ip", ip)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(s int) { w.status = s; w.ResponseWriter.WriteHeader(s) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// MaxBody membatasi ukuran body request.
func MaxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
