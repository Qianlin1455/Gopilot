package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopilot/internal/collector"
	"gopilot/internal/demo"
)

func TestToolsCollectRealDemoEvidence(t *testing.T) {
	app := demo.NewServer()
	t.Cleanup(func() { app.ResetLeaks() })
	demoServer := httptest.NewServer(app.Handler())
	defer demoServer.Close()

	client, err := collector.NewClient(demoServer.URL, demoServer.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sampler, err := collector.NewSampler(client, time.Second, 10)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	sampler.SampleOnce(context.Background())

	body, _ := json.Marshal(map[string]int{"count": 8})
	response, err := http.Post(demoServer.URL+"/api/leak", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("inject leak: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("inject status = %d", response.StatusCode)
	}
	sampler.SampleOnce(context.Background())

	service := NewService(client, sampler)
	metricsEnvelope := service.GetRuntimeMetrics(5)
	if !metricsEnvelope.OK {
		t.Fatalf("metrics error = %+v", metricsEnvelope.Error)
	}
	metrics := metricsEnvelope.Data.(RuntimeMetricsData)
	if metrics.Goroutines.AbsoluteDelta < 8 {
		t.Fatalf("goroutine delta = %d, want at least 8", metrics.Goroutines.AbsoluteDelta)
	}

	profileEnvelope := service.GetGoroutineProfile(context.Background(), 10)
	if !profileEnvelope.OK {
		t.Fatalf("profile error = %+v", profileEnvelope.Error)
	}
	profile := profileEnvelope.Data.(GoroutineProfileData)
	found := false
	for _, group := range profile.Groups {
		if group.ContainsIntentionalLeak && group.Count >= 8 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("profile groups do not contain grouped leak evidence: %+v", profile.Groups)
	}
}
