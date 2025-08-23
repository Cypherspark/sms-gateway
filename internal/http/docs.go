// internal/http/docs.go
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/Cypherspark/sms-gateway/api"
)

func (s *Server) mountDocs(r chi.Router) {
	r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, api.FS, "openapi.yaml")
	})
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<!doctype html>
<html>
  <head>
    <title>SMS Gateway API</title>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
  </head>
  <body>
    <redoc spec-url="/openapi.yaml"></redoc>
  </body>
</html>`))
	})
}
