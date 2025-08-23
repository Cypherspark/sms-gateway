// internal/http/docs.go
package httpapi

import (
	"io"
	"log"
	"net/http"

	"github.com/Cypherspark/sms-gateway/api"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountDocs(r chi.Router) {
	// Serve raw spec
	r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		f, err := api.SpecFS.Open("openapi.yaml")
		if err != nil {
			http.Error(w, "spec not found", http.StatusInternalServerError)
			return
		}
		defer func() {
			if err := f.Close(); err != nil {
				log.Printf("error closing openapi.yaml: %v", err) // Or handle differently
			}
		}()
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = io.Copy(w, f)
	})

	// Serve ReDoc UI
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <title>SMS Gateway API</title>
    <script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
    <style>html,body,#app{height:100%;margin:0}</style>
  </head>
  <body>
    <div id="app"></div>
    <script>
      Redoc.init('/openapi.yaml', {}, document.getElementById('app'));
    </script>
  </body>
</html>`))
	})
}
