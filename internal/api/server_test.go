package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopilot/internal/agent"
	"gopilot/internal/diagnosis"
)

type fakeDiagnoser struct {
	result   agent.Result
	err      error
	question string
}

func (f *fakeDiagnoser) Run(_ context.Context, question string) (agent.Result, error) {
	f.question = question
	return f.result, f.err
}

func TestDiagnosisEndpointReturnsStructuredDiagnosis(t *testing.T) {
	diagnoser := &fakeDiagnoser{result: agent.Result{Diagnosis: &diagnosis.Diagnosis{
		ID:              "diag_1",
		InvestigationID: "inv_1",
		GeneratedAt:     time.Now().UTC(),
		Summary:         "grounded diagnosis",
	}}}
	server := httptest.NewServer(NewServer(nil, diagnoser).Handler())
	defer server.Close()

	response, err := http.Post(server.URL+"/api/diagnoses", "application/json", bytes.NewBufferString(`{"question":"Diagnose it."}`))
	if err != nil {
		t.Fatalf("POST diagnosis: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var result diagnosis.Diagnosis
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode diagnosis: %v", err)
	}
	if result.ID != "diag_1" || diagnoser.question != "Diagnose it." {
		t.Fatalf("result=%+v question=%q", result, diagnoser.question)
	}
}

func TestDiagnosisEndpointReportsUnconfiguredAgent(t *testing.T) {
	server := httptest.NewServer(NewServer(nil, nil).Handler())
	defer server.Close()
	response, err := http.Post(server.URL+"/api/diagnoses", "application/json", nil)
	if err != nil {
		t.Fatalf("POST diagnosis: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	var body errorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.OK || body.Error.Code != "agent_not_configured" {
		t.Fatalf("body = %+v", body)
	}
}

func TestDiagnosisEndpointMapsAgentErrors(t *testing.T) {
	diagnoser := &fakeDiagnoser{err: &agent.RunError{Code: "provider_error", Message: "provider failed", InvestigationID: "inv_failed", Cause: errors.New("upstream")}}
	server := httptest.NewServer(NewServer(nil, diagnoser).Handler())
	defer server.Close()
	response, err := http.Post(server.URL+"/api/diagnoses", "application/json", nil)
	if err != nil {
		t.Fatalf("POST diagnosis: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	var body errorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "provider_error" || body.InvestigationID != "inv_failed" {
		t.Fatalf("body = %+v", body)
	}
}

func TestDiagnosisEndpointRejectsUnknownFields(t *testing.T) {
	diagnoser := &fakeDiagnoser{}
	server := httptest.NewServer(NewServer(nil, diagnoser).Handler())
	defer server.Close()
	response, err := http.Post(server.URL+"/api/diagnoses", "application/json", bytes.NewBufferString(`{"target_url":"http://example.com"}`))
	if err != nil {
		t.Fatalf("POST diagnosis: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}
