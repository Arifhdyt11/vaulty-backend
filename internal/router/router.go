// Package router merakit gin, middleware global, endpoint operasional, dan setiap versi API.
// Satu file per versi (v1.go, v2.go, ...). Tiap versi punya OpenAPI sendiri di
// /api/vN/openapi.json; Swagger UI di /api/docs menampilkan semuanya lewat dropdown.
package router

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"

	"vaulty-api/internal/config"
	"vaulty-api/internal/middleware"
	"vaulty-api/internal/service"
)

// Services adalah semua service yang bisa dipasang ke versi API mana pun.
type Services struct {
	Auth     *service.AuthService
	Audit    *service.AuditService
	Note     *service.NoteService
	Search   *service.SearchService
	Reminder *service.ReminderService
}

// Checker memeriksa dependensi (DB, Redis) untuk /readyz.
type Checker func(ctx context.Context) error

type Router struct {
	cfg      config.Config
	engine   *gin.Engine
	services Services
	versions []apiVersion // untuk dropdown Swagger UI
}

type apiVersion struct{ name, prefix string }

func New(cfg config.Config, services Services, checks map[string]Checker) *gin.Engine {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := &Router{cfg: cfg, engine: gin.New(), services: services}
	r.engine.Use(
		middleware.RequestID(),
		middleware.AccessLog(),
		gin.Recovery(),
		middleware.MultipartLimit(cfg.MaxUploadBytes+1<<20),
	)

	// Liveness & readiness untuk load balancer / orchestrator.
	r.engine.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.engine.GET("/readyz", readiness(checks))

	r.v1()
	r.docs()
	return r.engine
}

// newVersion membuat huma API di bawah prefix versi (mis. /api/v1) lengkap dengan auth
// middleware dan OpenAPI. Handler versi tersebut mendaftarkan path relatif (/notes, ...).
func (r *Router) newVersion(name, prefix, version string) huma.API {
	hc := huma.DefaultConfig("Vaulty API "+name, version)
	// Server URL dipakai Swagger UI & generator client sebagai base path semua operation.
	hc.Servers = []*huma.Server{{URL: prefix}}
	hc.OpenAPIPath = "/openapi"
	hc.DocsPath = "" // docs digabung di /api/docs (Swagger UI dengan dropdown versi)
	hc.SchemasPath = "/schemas"
	// Tanpa field $schema di setiap respons, supaya data yang di-cache client (Dexie) bersih.
	hc.CreateHooks = nil
	hc.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		middleware.BearerAuth: {Type: "http", Scheme: "bearer"},
	}

	api := humagin.NewWithGroup(r.engine, r.engine.Group(prefix), hc)
	api.UseMiddleware(middleware.Auth(api, r.services.Auth))
	r.versions = append(r.versions, apiVersion{name: name, prefix: prefix})
	return api
}

func readiness(checks map[string]Checker) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		status, code := gin.H{}, http.StatusOK
		for name, check := range checks {
			if err := check(ctx); err != nil {
				status[name], code = err.Error(), http.StatusServiceUnavailable
			} else {
				status[name] = "ok"
			}
		}
		c.JSON(code, status)
	}
}
