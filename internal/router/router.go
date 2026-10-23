// Package router: chi + huma, /healthz, /readyz, Swagger UI, file lokal (dev).
package router

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	v1 "vaulty/internal/handler/v1"
	"vaulty/internal/middleware"
	"vaulty/pkg/storage"
)

type Deps struct {
	Handlers   *v1.Handlers
	Auth       middleware.UserResolver
	ReadyDB    func(ctx context.Context) error
	ReadyRedis func(ctx context.Context) error
	LocalStore *storage.Local // nil bila pakai S3
	Secret     string
	MaxBodyMB  int64
	IsProd     bool
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(middleware.RequestLog)
	r.Use(middleware.MaxBody(d.MaxBodyMB << 20))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		if err := d.ReadyDB(ctx); err != nil {
			http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if d.ReadyRedis != nil {
			if err := d.ReadyRedis(ctx); err != nil {
				http.Error(w, "redis: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ready"))
	})

	cfg := huma.DefaultConfig("Vaulty API", "1.0.0")
	cfg.Info.Description = "API Vaulty: simpan apa pun, tanya Vee, diingatkan."
	cfg.Servers = []*huma.Server{{URL: "/api/v1"}}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": {Type: "http", Scheme: "bearer"}}
	cfg.DocsPath = "" // Swagger UI sendiri di /api/docs
	cfg.OpenAPIPath = "/openapi"

	sub := chi.NewRouter()
	api := humachi.New(sub, cfg)
	api.UseMiddleware(middleware.Auth(api, d.Auth))

	h := d.Handlers
	h.RegisterAuth(api)
	h.RegisterMe(api)
	h.RegisterDevices(api)
	h.RegisterItems(api)
	h.RegisterChat(api)
	h.RegisterReminders(api)
	h.RegisterBilling(api)
	h.RegisterBriefing(api)

	if d.LocalStore != nil {
		sub.Get("/files/*", localFileHandler(d.LocalStore, d.Secret))
	}
	r.Mount("/api/v1", sub)

	r.Get("/api/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerHTML))
	})
	return r
}

// Sign membuat tanda tangan URL file lokal (dev); S3 memakai presigned URL sendiri.
func Sign(secret string) func(key string, exp time.Time) string {
	return func(key string, exp time.Time) string {
		m := hmac.New(sha256.New, []byte(secret))
		_, _ = fmt.Fprintf(m, "%s|%d", key, exp.Unix())
		return hex.EncodeToString(m.Sum(nil))
	}
}

func localFileHandler(store *storage.Local, secret string) http.HandlerFunc {
	sign := Sign(secret)
	return func(w http.ResponseWriter, req *http.Request) {
		key := chi.URLParam(req, "*")
		expS, sig := req.URL.Query().Get("exp"), req.URL.Query().Get("sig")
		exp, err := strconv.ParseInt(expS, 10, 64)
		if err != nil || time.Now().Unix() > exp || !hmac.Equal([]byte(sig), []byte(sign(key, time.Unix(exp, 0)))) {
			http.Error(w, "link tidak valid atau kedaluwarsa", http.StatusForbidden)
			return
		}
		rc, ct, err := store.Get(req.Context(), key)
		if err != nil {
			http.Error(w, "tidak ditemukan", http.StatusNotFound)
			return
		}
		defer rc.Close()
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if strings.HasSuffix(key, ".zip") {
			w.Header().Set("Content-Disposition", "attachment; filename=vaulty-export.zip")
		}
		_, _ = io.Copy(w, rc)
	}
}

const swaggerHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Vaulty API</title>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14/swagger-ui.min.css"></head>
<body><div id="ui"></div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14/swagger-ui-bundle.min.js"></script>
<script>SwaggerUIBundle({url:"/api/v1/openapi.json",dom_id:"#ui",persistAuthorization:true});</script></body></html>`
