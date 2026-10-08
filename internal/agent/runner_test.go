package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopilot/internal/collector"
	"gopilot/internal/tools"
)

type scriptedClient struct {
	mu        sync.Mutex
	responses []Message
	errors    []error
	calls     int
}

func (c *scriptedClient) Complete(_ context.Context, _ []Message, _ []ToolDefinition) (Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := c.calls
	c.calls++
	if index < len(c.errors) && c.errors[index] != nil {
		return Message{}, c.errors[index]
	}
	if index >= len(c.responses) {
		return Message{}, errors.New("unexpected model call")
	}
	return c.responses[index], nil
}

type repeatingClient struct{ calls int }

func (c *repeatingClient) Complete(_ context.Context, _ []Message, _ []ToolDefinition) (Message, error) {
	c.calls++
	return Message{Role: "assistant", ToolCalls: []ToolCall{{
		ID:       "call_status_" + time.Now().Format("150405.000000000"),
		Type:     "function",
		Function: FunctionCall{Name: tools.ServiceStatusTool, Arguments: `{}`},
	}}}, nil
}

type agentTestEvidenceClient struct{}

func (agentTestEvidenceClient) Health(context.Context) (collector.Health, collector.RequestMeta, error) {
	return collector.Health{Status: "ok", Service: "demo-service", StartedAt: time.Now().Add(-time.Minute)}, collector.RequestMeta{HTTPStatus: 200}, nil
}

func (agentTestEvidenceClient) Runtime(context.Context) (collector.RuntimeSample, collector.RequestMeta, error) {
	return collector.RuntimeSample{Timestamp: time.Now().UTC(), Goroutines: 7, UptimeMillis: 60000}, collector.RequestMeta{HTTPStatus: 200}, nil
}

func (agentTestEvidenceClient) LeakStatus(context.Context) (collector.LeakStatus, collector.RequestMeta, error) {
	return collector.LeakStatus{}, collector.RequestMeta{HTTPStatus: 200}, nil
}

func (agentTestEvidenceClient) GoroutineProfile(context.Context, int) (collector.ProfilePayload, error) {
	return collector.ProfilePayload{Body: []byte("goroutine 1 [running]:\nexample.main()\n\t/main.go:1\n"), LimitBytes: collector.DefaultProfileLimit}, nil
}

func (agentTestEvidenceClient) sample() collector.RuntimeSample {
	return collector.RuntimeSample{Timestamp: time.Now().UTC(), Goroutines: 7}
}

type agentTestRuntimeSource struct{ client agentTestEvidenceClient }

func (s agentTestRuntimeSource) Runtime(context.Context) (collector.RuntimeSample, collector.RequestMeta, error) {
	return s.client.sample(), collector.RequestMeta{HTTPStatus: 200}, nil
}

func newAgentTestToolService(t *testing.T) *tools.Service {
	t.Helper()
	client := agentTestEvidenceClient{}
	sampler, err := collector.NewSampler(agentTestRuntimeSource{client: client}, time.Second, 10)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	sampler.SampleOnce(context.Background())
	return tools.NewService(client, sampler)
}

func TestSubmissionValidationRejectsUnknownCrossInvestigationAndEmptyEvidence(t *testing.T) {
	factory := tools.NewEnvelopeFactory()
	observation := factory.Success(tools.ServiceStatusTool, tools.ServiceStatusData{ServiceIdentity: "demo-service"})
	first := newInvestigation("inv_first", time.Now())
	first.storeObservation(observation)
	second := newInvestigation("inv_second", time.Now())

	validJSON := func(refs string) string {
		return `{"summary":"summary","root_cause":"cause","evidence_refs":` + refs + `,"confidence":0.8,"affected_component":"service","recommendation":"fix"}`
	}
	if _, err := decodeAndValidateSubmission(validJSON(`[]`), first); err == nil {
		t.Fatal("empty evidence was accepted")
	}
	if _, err := decodeAndValidateSubmission(validJSON(`["obs_unknown"]`), first); err == nil {
		t.Fatal("unknown evidence was accepted")
	}
	if _, err := decodeAndValidateSubmission(validJSON(`["`+observation.ObservationID+`"]`), second); err == nil {
		t.Fatal("cross-investigation evidence was accepted")
	}
	if second.ObservationCount() != 0 || first.ObservationCount() != 1 {
		t.Fatalf("observations leaked between investigations: first=%d second=%d", first.ObservationCount(), second.ObservationCount())
	}
}

func TestSubmissionValidationRejectsMalformedAndInvalidFields(t *testing.T) {
	investigation := newInvestigation("inv_test", time.Now())
	for name, raw := range map[string]string{
		"malformed":          `{"summary":`,
		"missing confidence": `{"summary":"s","root_cause":"r","evidence_refs":["x"],"affected_component":"c","recommendation":"r"}`,
		"invalid confidence": `{"summary":"s","root_cause":"r","evidence_refs":["x"],"confidence":1.5,"affected_component":"c","recommendation":"r"}`,
		"empty summary":      `{"summary":" ","root_cause":"r","evidence_refs":["x"],"confidence":0.5,"affected_component":"c","recommendation":"r"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeAndValidateSubmission(raw, investigation); err == nil {
				t.Fatalf("submission %s was accepted", raw)
			}
		})
	}
}

func TestRunnerRejectsUnsupportedTool(t *testing.T) {
	client := &scriptedClient{responses: []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: FunctionCall{Name: "delete_service", Arguments: `{}`}},
			},
		},
	}}
	runner, _ := NewRunner(client, newAgentTestToolService(t), 2)
	result, err := runner.Run(context.Background(), "diagnose")
	assertRunError(t, err, "unsupported_tool")
	if result.Investigation.FailedToolCalls != 1 || len(result.Investigation.Steps) != 1 {
		t.Fatalf("investigation = %+v", result.Investigation)
	}
}

func TestRunnerHandlesProviderFailure(t *testing.T) {
	client := &scriptedClient{errors: []error{errors.New("provider down")}}
	runner, _ := NewRunner(client, newAgentTestToolService(t), 2)
	_, err := runner.Run(context.Background(), "diagnose")
	assertRunError(t, err, "provider_error")
}

func TestRunnerHonorsContextCancellation(t *testing.T) {
	client := &scriptedClient{}
	runner, _ := NewRunner(client, newAgentTestToolService(t), 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runner.Run(ctx, "diagnose")
	assertRunError(t, err, "context_cancelled")
}

func TestRunnerStopsAtMaximumSteps(t *testing.T) {
	client := &repeatingClient{}
	runner, _ := NewRunner(client, newAgentTestToolService(t), 2)
	result, err := runner.Run(context.Background(), "diagnose")
	assertRunError(t, err, "max_steps_exceeded")
	if client.calls != 2 || len(result.Investigation.Steps) != 2 || result.Investigation.ObservationCount() != 2 {
		t.Fatalf("calls=%d steps=%d observations=%d", client.calls, len(result.Investigation.Steps), result.Investigation.ObservationCount())
	}
}

func TestRunnerRejectsMalformedModelResponse(t *testing.T) {
	client := &scriptedClient{responses: []Message{{Role: "assistant", Content: "I think it is a leak."}}}
	runner, _ := NewRunner(client, newAgentTestToolService(t), 2)
	_, err := runner.Run(context.Background(), "diagnose")
	assertRunError(t, err, "malformed_model_response")
}

func TestRunnerReturnsDiagnosisValidationErrorAtLimit(t *testing.T) {
	client := &scriptedClient{responses: []Message{{Role: "assistant", ToolCalls: []ToolCall{{
		ID:       "call_submit",
		Type:     "function",
		Function: FunctionCall{Name: SubmitDiagnosisTool, Arguments: `{"summary":`},
	}}}}}
	runner, _ := NewRunner(client, newAgentTestToolService(t), 1)
	result, err := runner.Run(context.Background(), "diagnose")
	assertRunError(t, err, "diagnosis_validation_failed")
	if result.Investigation.FailedToolCalls != 1 {
		t.Fatalf("failed tool calls = %d, want 1", result.Investigation.FailedToolCalls)
	}
	step := result.Investigation.Steps[0]
	if step.Status != "failed" || step.Error == "" || step.ObservationID != "" || len(step.Arguments) != 0 {
		t.Fatalf("failed timeline step exposed unvalidated data: %+v", step)
	}
}

func assertRunError(t *testing.T, err error, code string) {
	t.Helper()
	var runError *RunError
	if !errors.As(err, &runError) || runError.Code != code {
		t.Fatalf("error = %#v, want RunError code %q", err, code)
	}
}
