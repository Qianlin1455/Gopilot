package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"gopilot/internal/diagnosis"
	"gopilot/internal/tools"
)

const (
	SubmitDiagnosisTool = "submit_diagnosis"
	DefaultMaxSteps     = 8
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type ChatClient interface {
	Complete(ctx context.Context, messages []Message, definitions []ToolDefinition) (Message, error)
}

type SubmitDiagnosisInput struct {
	Summary           string   `json:"summary"`
	RootCause         string   `json:"root_cause"`
	EvidenceRefs      []string `json:"evidence_refs"`
	Confidence        float64  `json:"confidence"`
	AffectedComponent string   `json:"affected_component"`
	Recommendation    string   `json:"recommendation"`
}

type Investigation struct {
	ID               string                        `json:"investigation_id"`
	StartedAt        time.Time                     `json:"started_at"`
	Steps            []diagnosis.InvestigationStep `json:"tool_call_sequence"`
	FailedToolCalls  int                           `json:"failed_tool_calls"`
	Status           string                        `json:"status"`
	FinalDiagnosis   *diagnosis.Diagnosis          `json:"final_diagnosis,omitempty"`
	observations     map[string]tools.Envelope
	observationOrder []string
}

func newInvestigation(id string, startedAt time.Time) *Investigation {
	return &Investigation{
		ID:           id,
		StartedAt:    startedAt,
		Status:       "running",
		observations: make(map[string]tools.Envelope),
	}
}

func (i *Investigation) storeObservation(observation tools.Envelope) {
	if !observation.OK {
		return
	}
	i.observations[observation.ObservationID] = observation
	i.observationOrder = append(i.observationOrder, observation.ObservationID)
}

func (i *Investigation) observation(id string) (tools.Envelope, bool) {
	observation, ok := i.observations[id]
	return observation, ok
}

func (i *Investigation) ObservationCount() int {
	return len(i.observations)
}

func (i *Investigation) ObservationIDs() []string {
	return append([]string(nil), i.observationOrder...)
}

type Result struct {
	Diagnosis     *diagnosis.Diagnosis `json:"diagnosis,omitempty"`
	Investigation *Investigation       `json:"investigation"`
}

type RunError struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	InvestigationID string `json:"investigation_id,omitempty"`
	Cause           error  `json:"-"`
}

func (e *RunError) Error() string {
	return e.Message
}

func (e *RunError) Unwrap() error {
	return e.Cause
}

type idFactory struct {
	sequence atomic.Uint64
	now      func() time.Time
}

func newIDFactory() *idFactory {
	return &idFactory{now: func() time.Time { return time.Now().UTC() }}
}

func (f *idFactory) next(prefix string) string {
	now := f.now().UTC()
	return fmt.Sprintf("%s_%d_%06d", prefix, now.UnixMilli(), f.sequence.Add(1))
}

func toolResultMessage(callID string, result any) (Message, error) {
	content, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("marshal tool result: %w", err)
	}
	return Message{Role: "tool", ToolCallID: callID, Content: string(content)}, nil
}
