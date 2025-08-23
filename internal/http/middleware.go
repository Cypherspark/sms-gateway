package httpapi

import (
	"net/http"
	"time"

	"github.com/Cypherspark/sms-gateway/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		// Default to path; try to replace with the route pattern.
		handler := r.URL.Path
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if rp := rc.RoutePattern(); rp != "" {
				handler = rp
			}
		}

		start := time.Now()
		next.ServeHTTP(ww, r)
		elapsed := time.Since(start).Seconds()

		metrics.HTTPRequests.WithLabelValues(handler, r.Method, http.StatusText(ww.Status())).Inc()
		metrics.HTTPDuration.WithLabelValues(handler, r.Method).Observe(elapsed)
	})
}
