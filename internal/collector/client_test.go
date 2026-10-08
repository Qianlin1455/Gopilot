package collector_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"gopilot/internal/collector"
	"gopilot/internal/demo"
)

func TestClientCollectsFromRealDemoHandler(t *testing.T) {
	app := demo.NewServer()
	t.Cleanup(func() { app.ResetLeaks() })
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	client, err := collector.NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	health, meta, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Service != "demo-service" || meta.HTTPStatus != 200 {
		t.Fatalf("health=%+v meta=%+v", health, meta)
	}
	runtimeSample, _, err := client.Runtime(context.Background())
	if err != nil || runtimeSample.Goroutines < 1 {
		t.Fatalf("runtime sample=%+v err=%v", runtimeSample, err)
	}
	profile, err := client.GoroutineProfile(context.Background(), collector.DefaultProfileLimit)
	if err != nil || len(profile.Body) == 0 {
		t.Fatalf("profile bytes=%d err=%v", len(profile.Body), err)
	}
}
