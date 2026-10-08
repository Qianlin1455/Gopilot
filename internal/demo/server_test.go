package demo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPWorkflow(t *testing.T) {
	app := NewServer()
	t.Cleanup(func() { app.ResetLeaks() })
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	assertStatus(t, http.MethodGet, server.URL+"/healthz", "", http.StatusOK)
	assertStatus(t, http.MethodGet, server.URL+"/api/runtime", "", http.StatusOK)
	assertStatus(t, http.MethodGet, server.URL+"/debug/pprof/goroutine?debug=1", "", http.StatusOK)
	assertStatus(t, http.MethodPost, server.URL+"/api/leak", `{"count":101}`, http.StatusBadRequest)
	assertStatus(t, http.MethodPost, server.URL+"/api/leak", `{"count":4}`, http.StatusCreated)

	response, err := http.Get(server.URL + "/api/leak/status")
	if err != nil {
		t.Fatalf("get leak status: %v", err)
	}
	defer response.Body.Close()
	var status LeakStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode leak status: %v", err)
	}
	if status.Active != 4 {
		t.Fatalf("active leaks = %d, want 4", status.Active)
	}

	assertStatus(t, http.MethodPost, server.URL+"/api/leak/reset", "", http.StatusOK)
	if active := app.leaks.Status().Active; active != 0 {
		t.Fatalf("active leaks after reset = %d, want 0", active)
	}
}

func assertStatus(t *testing.T, method, url, body string, want int) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, url, response.StatusCode, want, responseBody)
	}
}
