package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gopilot/internal/collector"
	"gopilot/internal/demo"
	"gopilot/internal/tools"
)

func TestRunnerExecutesRealToolsThroughFakeChatCompletionsProvider(t *testing.T) {
	demoApp := demo.NewServer()
	t.Cleanup(func() { demoApp.ResetLeaks() })
	demoServer := httptest.NewServer(demoApp.Handler())
	defer demoServer.Close()

	collectorClient, err := collector.NewClient(demoServer.URL, demoServer.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sampler, err := collector.NewSampler(collectorClient, time.Second, 20)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	sampler.SampleOnce(context.Background())
	injectBody, _ := json.Marshal(map[string]int{"count": 8})
	injectResponse, err := http.Post(demoServer.URL+"/api/leak", "application/json", bytes.NewReader(injectBody))
	if err != nil {
		t.Fatalf("inject leak: %v", err)
	}
	injectResponse.Body.Close()
	sampler.SampleOnce(context.Background())
	toolService := tools.NewService(collectorClient, sampler)

	provider := newSequencedProvider(t)
	providerServer := httptest.NewServer(provider)
	defer providerServer.Close()
	chatClient, err := NewClient(providerServer.URL, "integration-key", "fake-model", providerServer.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner, err := NewRunner(chatClient, toolService, DefaultMaxSteps)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	logger := &recordingLogger{}
	runner.logger = logger

	result, err := runner.Run(context.Background(), "Diagnose the current service problem.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if providerError := provider.errorMessage(); providerError != "" {
		t.Fatal(providerError)
	}
	if provider.callCount() != 3 {
		t.Fatalf("provider calls = %d, want 3", provider.callCount())
	}
	if result.Diagnosis == nil || result.Investigation.Status != "completed" {
		t.Fatalf("result = %+v", result)
	}
	if result.Investigation.ObservationCount() != 3 || len(result.Diagnosis.Evidence) != 3 {
		t.Fatalf("observations=%d evidence=%d", result.Investigation.ObservationCount(), len(result.Diagnosis.Evidence))
	}
	if len(result.Diagnosis.Investigation) != 4 {
		t.Fatalf("investigation steps = %d, want 4", len(result.Diagnosis.Investigation))
	}
	wantCallIDs := []string{"call_status", "call_metrics", "call_profile", "call_submit"}
	wantTools := []string{tools.ServiceStatusTool, tools.RuntimeMetricsTool, tools.GoroutineProfileTool, SubmitDiagnosisTool}
	wantAgentSteps := []int{1, 1, 2, 3}
	for index, want := range wantCallIDs {
		step := result.Diagnosis.Investigation[index]
		if step.Sequence != index+1 || step.ToolCallID != want || step.Tool != wantTools[index] || step.AgentStep != wantAgentSteps[index] {
			t.Fatalf("step %d = %+v", index, step)
		}
		if step.Status != "success" {
			t.Fatalf("step %d status = %q, want success", index, step.Status)
		}
		if index < 3 && step.ObservationID == "" {
			t.Fatalf("step %d missing successful observation ID", index)
		}
		if index == 3 && step.ObservationID != "" {
			t.Fatalf("submit step unexpectedly has observation ID %q", step.ObservationID)
		}
	}
	if string(result.Diagnosis.Investigation[0].Arguments) != `{}` || string(result.Diagnosis.Investigation[1].Arguments) != `{"window_seconds":5}` || string(result.Diagnosis.Investigation[2].Arguments) != `{"top_n":10}` {
		t.Fatalf("timeline does not contain canonical validated arguments: %+v", result.Diagnosis.Investigation)
	}
	metrics, ok := result.Diagnosis.Evidence[1].Data.(tools.RuntimeMetricsData)
	if !ok || metrics.Goroutines.AbsoluteDelta < 8 {
		t.Fatalf("materialized metrics evidence = %#v", result.Diagnosis.Evidence[1].Data)
	}
	profile, ok := result.Diagnosis.Evidence[2].Data.(tools.GoroutineProfileData)
	if !ok {
		t.Fatalf("materialized profile evidence type = %T", result.Diagnosis.Evidence[2].Data)
	}
	foundLeak := false
	for _, group := range profile.Groups {
		if group.ContainsIntentionalLeak && group.Count >= 8 {
			foundLeak = true
		}
	}
	if !foundLeak {
		t.Fatalf("profile did not contain real grouped leak evidence: %+v", profile.Groups)
	}
	if result.Diagnosis.Evidence[2].Description == "model-authored evidence" {
		t.Fatal("model-authored evidence was copied into the diagnosis")
	}
	logs := logger.String()
	for _, expected := range []string{"investigation=", "model_step=1", "tool=get_service_status", "tool=get_runtime_metrics", "observation_id=", "status=completed", "diagnosis="} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("logs do not contain %q:\n%s", expected, logs)
		}
	}
	if strings.Contains(logs, "integration-key") {
		t.Fatal("logs contain the provider API key")
	}
}

type recordingLogger struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (l *recordingLogger) Printf(format string, values ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(&l.buffer, format+"\n", values...)
}

func (l *recordingLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

type sequencedProvider struct {
	t     *testing.T
	mu    sync.Mutex
	calls int
	err   string
}

func newSequencedProvider(t *testing.T) *sequencedProvider {
	return &sequencedProvider{t: t}
}

func (p *sequencedProvider) errorMessage() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *sequencedProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var request chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		p.err = fmt.Sprintf("decode provider request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.ToolChoice != "required" || len(request.Tools) != 4 {
		p.err = fmt.Sprintf("tool configuration = choice %q definitions %d", request.ToolChoice, len(request.Tools))
	}

	switch p.calls {
	case 0:
		writeCompletion(p.t, w,
			ToolCall{ID: "call_status", Type: "function", Function: FunctionCall{Name: tools.ServiceStatusTool, Arguments: `{}`}},
			ToolCall{ID: "call_metrics", Type: "function", Function: FunctionCall{Name: tools.RuntimeMetricsTool, Arguments: `{"window_seconds":5}`}},
		)
	case 1:
		p.requireToolCallPair(request.Messages, "call_status")
		p.requireToolCallPair(request.Messages, "call_metrics")
		writeCompletion(p.t, w, ToolCall{ID: "call_profile", Type: "function", Function: FunctionCall{Name: tools.GoroutineProfileTool, Arguments: `{"top_n":10}`}})
	case 2:
		p.requireToolCallPair(request.Messages, "call_profile")
		references := successfulObservationIDs(request.Messages)
		arguments, _ := json.Marshal(SubmitDiagnosisInput{
			Summary:           "The service has a goroutine leak.",
			RootCause:         "Goroutines are blocked in LeakController.waitForever.",
			EvidenceRefs:      references,
			Confidence:        0.97,
			AffectedComponent: "demo-service leak controller",
			Recommendation:    "Ensure blocked goroutines receive cancellation and exit.",
		})
		writeCompletion(p.t, w, ToolCall{ID: "call_submit", Type: "function", Function: FunctionCall{Name: SubmitDiagnosisTool, Arguments: string(arguments)}})
	default:
		p.err = fmt.Sprintf("unexpected provider call %d", p.calls)
		w.WriteHeader(http.StatusInternalServerError)
	}
	p.calls++
}

func (p *sequencedProvider) requireToolCallPair(messages []Message, callID string) {
	assistantFound, resultFound := false, false
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if message.Role == "assistant" && call.ID == callID {
				assistantFound = true
			}
		}
		if message.Role == "tool" && message.ToolCallID == callID {
			resultFound = true
		}
	}
	if !assistantFound || !resultFound {
		p.err = fmt.Sprintf("tool call ID %s was not preserved: assistant=%t result=%t", callID, assistantFound, resultFound)
	}
}

func (p *sequencedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func successfulObservationIDs(messages []Message) []string {
	var references []string
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		var envelope tools.Envelope
		if err := json.Unmarshal([]byte(message.Content), &envelope); err == nil && envelope.OK && envelope.Tool != SubmitDiagnosisTool {
			references = append(references, envelope.ObservationID)
		}
	}
	return references
}
