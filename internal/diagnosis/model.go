package diagnosis

import (
	"encoding/json"
	"time"
)

type Evidence struct {
	ObservationID string    `json:"observation_id"`
	SourceTool    string    `json:"source_tool"`
	CapturedAt    time.Time `json:"captured_at"`
	Description   string    `json:"description"`
	Data          any       `json:"data"`
}

type InvestigationStep struct {
	Sequence      int             `json:"sequence"`
	AgentStep     int             `json:"agent_step"`
	ToolCallID    string          `json:"tool_call_id"`
	Tool          string          `json:"tool"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	Status        string          `json:"status"`
	ObservationID string          `json:"observation_id,omitempty"`
	Error         string          `json:"error,omitempty"`
	StartedAt     time.Time       `json:"started_at"`
	CompletedAt   time.Time       `json:"completed_at"`
}

type Diagnosis struct {
	ID                string              `json:"id"`
	InvestigationID   string              `json:"investigation_id"`
	GeneratedAt       time.Time           `json:"generated_at"`
	Summary           string              `json:"summary"`
	RootCause         string              `json:"root_cause"`
	Evidence          []Evidence          `json:"evidence"`
	Confidence        float64             `json:"confidence"`
	AffectedComponent string              `json:"affected_component"`
	Recommendation    string              `json:"recommendation"`
	Investigation     []InvestigationStep `json:"investigation"`
}
