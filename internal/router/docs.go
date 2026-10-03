package router

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
)

// DocsPath: Swagger UI untuk semua versi API. Di bawah /api/ karena Nginx hanya
// meneruskan /api/* ke Go.
const DocsPath = "/api/docs"

const swaggerUIVersion = "5.33.0"

var swaggerPage = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Vaulty API Docs</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@{{.Version}}/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@{{.Version}}/swagger-ui-bundle.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@{{.Version}}/swagger-ui-standalone-preset.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      urls: {{.URLs}},
      "urls.primaryName": {{.Primary}},
      dom_id: "#swagger-ui",
      deepLinking: true,
      persistAuthorization: true,
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
      layout: "StandaloneLayout"
    });
  </script>
</body>
</html>`))

// docs memasang Swagger UI dengan dropdown berisi setiap versi yang terdaftar
// (versi terbaru dipilih default).
func (r *Router) docs() {
	type specURL struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	urls := make([]specURL, 0, len(r.versions))
	for i := len(r.versions) - 1; i >= 0; i-- {
		v := r.versions[i]
		urls = append(urls, specURL{URL: v.prefix + "/openapi.json", Name: v.name})
	}
	urlsJSON, _ := json.Marshal(urls)
	data := map[string]any{
		"Version": swaggerUIVersion,
		"URLs":    template.JS(urlsJSON),
		"Primary": urls[0].Name,
	}
	r.engine.GET(DocsPath, func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		if err := swaggerPage.Execute(c.Writer, data); err != nil {
			c.Status(http.StatusInternalServerError)
		}
	})
}
