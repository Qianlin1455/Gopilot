package demo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

type Server struct {
	leaks     *LeakController
	startedAt time.Time
	handler   http.Handler
}

type runtimeSnapshot struct {
	Timestamp      time.Time `json:"timestamp"`
	GoVersion      string    `json:"go_version"`
	Goroutines     int       `json:"goroutines"`
	HeapAllocBytes uint64    `json:"heap_alloc_bytes"`
	HeapObjects    uint64    `json:"heap_objects"`
	UptimeMillis   int64     `json:"uptime_ms"`
}

type leakRequest struct {
	Count int `json:"count"`
}

func NewServer() *Server {
	server := &Server{
		leaks:     NewLeakController(),
		startedAt: time.Now().UTC(),
	}
	server.handler = withCORS(server.routes())
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) ResetLeaks() LeakStatus {
	return s.leaks.Reset()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/runtime", s.runtime)
	mux.HandleFunc("GET /api/leak/status", s.leakStatus)
	mux.HandleFunc("POST /api/leak", s.injectLeak)
	mux.HandleFunc("POST /api/leak/reset", s.resetLeak)

	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	for _, profile := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("GET /debug/pprof/"+profile, pprof.Handler(profile))
	}
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"service":    "demo-service",
		"started_at": s.startedAt,
	})
}

func (s *Server) runtime(w http.ResponseWriter, _ *http.Request) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, runtimeSnapshot{
		Timestamp:      now,
		GoVersion:      runtime.Version(),
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocBytes: memory.HeapAlloc,
		HeapObjects:    memory.HeapObjects,
		UptimeMillis:   now.Sub(s.startedAt).Milliseconds(),
	})
}

func (s *Server) leakStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.leaks.Status())
}

func (s *Server) injectLeak(w http.ResponseWriter, r *http.Request) {
	request := leakRequest{Count: DefaultLeakCount}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "body must be JSON with an integer count")
		return
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "body must contain one JSON object")
		return
	}

	status, err := s.leaks.Inject(request.Count)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_leak_count", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, status)
}

func (s *Server) resetLeak(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.leaks.Reset())
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
