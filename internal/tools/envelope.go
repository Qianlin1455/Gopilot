package tools

import (
	"fmt"
	"sync/atomic"
	"time"
)

type ToolError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Envelope struct {
	OK            bool       `json:"ok"`
	ObservationID string     `json:"observation_id"`
	Tool          string     `json:"tool"`
	CapturedAt    time.Time  `json:"captured_at"`
	Data          any        `json:"data,omitempty"`
	Error         *ToolError `json:"error,omitempty"`
}

type EnvelopeFactory struct {
	now      func() time.Time
	sequence atomic.Uint64
}

func NewEnvelopeFactory() *EnvelopeFactory {
	return &EnvelopeFactory{now: func() time.Time { return time.Now().UTC() }}
}

func (f *EnvelopeFactory) Success(tool string, data any) Envelope {
	now := f.now().UTC()
	return Envelope{
		OK:            true,
		ObservationID: f.observationID(now),
		Tool:          tool,
		CapturedAt:    now,
		Data:          data,
	}
}

func (f *EnvelopeFactory) Failure(tool, code, message string, details map[string]any) Envelope {
	now := f.now().UTC()
	return Envelope{
		OK:            false,
		ObservationID: f.observationID(now),
		Tool:          tool,
		CapturedAt:    now,
		Error:         &ToolError{Code: code, Message: message, Details: details},
	}
}

func (f *EnvelopeFactory) observationID(now time.Time) string {
	return fmt.Sprintf("obs_%d_%06d", now.UnixMilli(), f.sequence.Add(1))
}
