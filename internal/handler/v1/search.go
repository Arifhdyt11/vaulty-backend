package v1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/handler"
	"vaulty-api/internal/middleware"
	"vaulty-api/internal/model"
	"vaulty-api/internal/service"
)

type SearchHandler struct{ searches *service.SearchService }

func NewSearchHandler(search *service.SearchService) *SearchHandler {
	return &SearchHandler{searches: search}
}

type searchRequest struct {
	Q       string `query:"q" minLength:"1" maxLength:"500" required:"true" doc:"Pertanyaan bahasa natural atau keyword"`
	Type    string `query:"type"`
	Tag     string `query:"tag"`
	Project string `query:"project"`
	Limit   int    `query:"limit" minimum:"1" maximum:"50" default:"20"`
}

type searchResponse struct {
	Body struct {
		Items []model.SearchHit `json:"items"`
		Mode  string            `json:"mode" enum:"hybrid,fulltext" doc:"fulltext jika AI tidak aktif atau embedding gagal"`
	}
}

func (h *SearchHandler) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "search",
		Method:      http.MethodGet,
		Path:        "/search",
		Summary:     "Hybrid search (semantic + keyword)",
		Tags:        []string{"Search"},
		Security:    middleware.Secured,
	}, h.search)
}

func (h *SearchHandler) search(ctx context.Context, in *searchRequest) (*searchResponse, error) {
	f := model.NoteFilter{Type: in.Type, Tag: in.Tag, Project: in.Project}
	hits, mode, err := h.searches.Search(ctx, middleware.CurrentUser(ctx).ID, in.Q, f, in.Limit)
	if err != nil {
		return nil, handler.ToHTTPError(err)
	}
	out := &searchResponse{}
	for i := range hits {
		hits[i].Note = presentNote(hits[i].Note)
	}
	out.Body.Items, out.Body.Mode = hits, mode
	return out, nil
}
