package v1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"vaulty/internal/handler"
	"vaulty/internal/middleware"
	"vaulty/internal/model"
)

// AdminStats: sumber agregat lintas user untuk dashboard admin (web).
type AdminStats interface {
	Overview(ctx context.Context, days int) (model.AdminOverview, error)
}

func (h *Handlers) RegisterAdmin(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "admin-overview", Method: http.MethodGet, Path: "/admin/overview", Summary: "Ringkasan platform (hanya admin)", Tags: []string{"admin"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Days int `query:"days" minimum:"7" maximum:"90" default:"14"`
		}) (*struct{ Body model.AdminOverview }, error) {
			if middleware.UserFrom(ctx).Role != "admin" {
				return nil, handler.E(model.ErrForbidden)
			}
			if h.Admin == nil {
				return nil, huma.Error503ServiceUnavailable("statistik admin belum tersedia")
			}
			o, err := h.Admin.Overview(ctx, in.Days)
			if err != nil {
				return nil, handler.E(err)
			}
			return &struct{ Body model.AdminOverview }{o}, nil
		})
}
