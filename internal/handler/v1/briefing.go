package v1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"vaulty/internal/handler"
	"vaulty/internal/middleware"
	"vaulty/internal/model"
)

type briefingOut struct{ Body model.Briefing }

// RegisterBriefing: Siaran Vee, briefing audio harian dari simpanan user (ADR-016).
func (h *Handlers) RegisterBriefing(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "briefing-today", Method: http.MethodGet, Path: "/briefing/today", Summary: "Cerita Vee hari ini",
		Description: "Bila belum ada siaran hari ini (zona waktu user), server memilih bab dan menjadwalkan naskah + suara (status building). App mengulang tiap 3 detik selama building; bab bisa diputar begitu status bab ready.",
		Tags:        []string{"briefing"}, Security: middleware.Secured},
		func(ctx context.Context, _ *struct{}) (*briefingOut, error) {
			b, err := h.Briefing.Today(ctx, middleware.UserFrom(ctx))
			if err != nil {
				return nil, handler.E(err)
			}
			return &briefingOut{b}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "briefing-rebuild", Method: http.MethodPost, Path: "/briefing/today/rebuild", Summary: "Susun ulang siaran hari ini",
		Description: "Buang siaran hari ini beserta audionya lalu pilih bab lagi (mis. setelah menyimpan banyak item).",
		Tags:        []string{"briefing"}, Security: middleware.Secured, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, _ *struct{}) (*briefingOut, error) {
			b, err := h.Briefing.Rebuild(ctx, middleware.UserFrom(ctx))
			if err != nil {
				return nil, handler.E(err)
			}
			return &briefingOut{b}, nil
		})
}
