package tools

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func NewHTTPHandler(service *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "gopilot"})
	})
	mux.HandleFunc("GET /api/tools/service-status", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, service.GetServiceStatus(r.Context()))
	})
	mux.HandleFunc("GET /api/tools/runtime-metrics", func(w http.ResponseWriter, r *http.Request) {
		value, ok := integerQuery(r, "window_seconds", DefaultMetricsWindow)
		if !ok {
			writeEnvelope(w, service.factory.Failure(RuntimeMetricsTool, "invalid_argument", "window_seconds must be an integer", map[string]any{"argument": "window_seconds"}))
			return
		}
		writeEnvelope(w, service.GetRuntimeMetrics(value))
	})
	mux.HandleFunc("GET /api/tools/goroutine-profile", func(w http.ResponseWriter, r *http.Request) {
		value, ok := integerQuery(r, "top_n", DefaultProfileTopN)
		if !ok {
			writeEnvelope(w, service.factory.Failure(GoroutineProfileTool, "invalid_argument", "top_n must be an integer", map[string]any{"argument": "top_n"}))
			return
		}
		writeEnvelope(w, service.GetGoroutineProfile(r.Context(), value))
	})
	return mux
}

func integerQuery(r *http.Request, name string, defaultValue int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil
}

func writeEnvelope(w http.ResponseWriter, envelope Envelope) {
	status := http.StatusOK
	if !envelope.OK {
		status = http.StatusBadRequest
		if envelope.Error != nil && (envelope.Error.Code == "target_unavailable" || envelope.Error.Code == "upstream_http_error" || envelope.Error.Code == "evidence_unavailable") {
			status = http.StatusBadGateway
		}
	}
	writeJSON(w, status, envelope)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
