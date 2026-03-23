package metrics

import (
	"net/http"
)

// MetricsHandler exposes HTTP endpoints for the Jarvis dashboard.
// Follows the same pattern as RolesHandler: struct + RegisterRoutes + authMiddleware.
type MetricsHandler struct {
	collector *MetricsCollector
	token     string
}

// NewMetricsHandler creates a handler for metrics endpoints.
func NewMetricsHandler(collector *MetricsCollector, token string) *MetricsHandler {
	return &MetricsHandler{collector: collector, token: token}
}

// RegisterRoutes registers all metrics routes on the given mux.
func (h *MetricsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/metrics/health", h.authMiddleware(h.handleHealth))
	mux.HandleFunc("GET /v1/metrics/cognitive", h.authMiddleware(h.handleCognitive))
	mux.HandleFunc("GET /v1/metrics/org", h.authMiddleware(h.handleOrg))
	mux.HandleFunc("GET /v1/metrics/operations", h.authMiddleware(h.handleOperations))
	mux.HandleFunc("GET /v1/metrics/evolution", h.authMiddleware(h.handleEvolution))
}

func (h *MetricsHandler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenMatch(extractBearerToken(r), h.token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (h *MetricsHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.collector.CollectHealth())
}

func (h *MetricsHandler) handleCognitive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.collector.CollectCognitive())
}

func (h *MetricsHandler) handleOrg(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.collector.CollectOrg())
}

func (h *MetricsHandler) handleOperations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.collector.CollectOperations())
}

func (h *MetricsHandler) handleEvolution(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.collector.CollectEvolution())
}
