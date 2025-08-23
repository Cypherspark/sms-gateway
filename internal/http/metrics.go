package httpapi

import (
	"github.com/Cypherspark/sms-gateway/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Server) mountMetrics(r chi.Router) {
	metrics.MustRegister()
	r.Method("GET", "/metrics", promhttp.Handler())
}
