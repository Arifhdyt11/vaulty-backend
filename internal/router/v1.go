package router

import v1 "vaulty-api/internal/handler/v1"

// V1Prefix adalah base path API versi 1 (ADR-005).
const V1Prefix = "/api/v1"

// v1 mendaftarkan semua endpoint /api/v1. Perubahan yang breaking tidak boleh masuk sini;
// buat v2.go + internal/handler/v2 dengan prefix /api/v2 (lihat README).
func (r *Router) v1() {
	api := r.newVersion("v1", V1Prefix, "1.0.0")
	v1.Register(api, v1.Services{
		Auth:           r.services.Auth,
		Audit:          r.services.Audit,
		Note:           r.services.Note,
		Search:         r.services.Search,
		MaxUploadBytes: r.cfg.MaxUploadBytes,
	})
}
