package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strings"
	"time"

	"gopilot/internal/diagnosis"
	"gopilot/internal/tools"
)

type Runner struct {
	client   ChatClient
	tools    *tools.Service
	maxSteps int
	ids      *idFactory
	now      func() time.Time
	logger   Logger
}

type Logger interface {
	Printf(format string, values ...any)
}

func NewRunner(client ChatClient, toolService *tools.Service, maxSteps int) (*Runner, error) {
	if client == nil {
		return nil, errors.New("chat client is required")
	}
	if toolService == nil {
		return nil, errors.New("tool service is required")
	}
	if maxSteps < 1 {
		return nil, errors.New("max steps must be positive")
	}
	return &Runner{
		client:   client,
		tools:    toolService,
		maxSteps: maxSteps,
		ids:      newIDFactory(),
		now:      func() time.Time { return time.Now().UTC() },
		logger:   log.Default(),
	}, nil
}

func (r *Runner) Run(ctx context.Context, question string) (Result, error) {
	startedAt := r.now()
	investigation := newInvestigation(r.ids.next("inv"), startedAt)
	result := Result{Investigation: investigation}
	r.logger.Printf("agent investigation=%s status=started", investigation.ID)
	question = strings.TrimSpace(question)
	if question == "" {
		question = "Diagnose the current service problem."
	}
	messages := []Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: question},
	}
	definitions := ToolDefinitions()
	sequence := 0
	var lastDiagnosisValidation error

	for agentStep := 1; agentStep <= r.maxSteps; agentStep++ {
		if err := ctx.Err(); err != nil {
			investigation.Status = "cancelled"
			r.logger.Printf("agent investigation=%s model_step=%d status=cancelled", investigation.ID, agentStep)
			return result, runError("context_cancelled", "diagnosis request was cancelled", investigation.ID, err)
		}
		r.logger.Printf("agent investigation=%s model_step=%d provider_request=started", investigation.ID, agentStep)
		assistant, err := r.client.Complete(ctx, messages, definitions)
		if err != nil {
			investigation.Status = "failed"
			if ctx.Err() != nil {
				investigation.Status = "cancelled"
				r.logger.Printf("agent investigation=%s model_step=%d status=cancelled provider_error=%q", investigation.ID, agentStep, ctx.Err())
				return result, runError("context_cancelled", "diagnosis request was cancelled", investigation.ID, ctx.Err())
			}
			var providerError *ProviderError
			if errors.As(err, &providerError) {
				if providerError.Code == "malformed_response" {
					r.logger.Printf("agent investigation=%s model_step=%d protocol_error_code=%s", investigation.ID, agentStep, providerError.Code)
					return result, runError("malformed_model_response", providerError.Error(), investigation.ID, err)
				}
				r.logger.Printf("agent investigation=%s model_step=%d provider_error_code=%s http_status=%d", investigation.ID, agentStep, providerError.Code, providerError.HTTPStatus)
			} else {
				r.logger.Printf("agent investigation=%s model_step=%d provider_error_type=%T", investigation.ID, agentStep, err)
			}
			return result, runError("provider_error", err.Error(), investigation.ID, err)
		}
		if len(assistant.ToolCalls) == 0 {
			investigation.Status = "failed"
			r.logger.Printf("agent investigation=%s model_step=%d protocol_error=%q", investigation.ID, agentStep, "model response did not contain a tool call")
			return result, runError("malformed_model_response", "model response did not contain a tool call", investigation.ID, nil)
		}
		messages = append(messages, assistant)

		for _, call := range assistant.ToolCalls {
			sequence++
			r.logger.Printf("agent investigation=%s model_step=%d tool=%s tool_call_id=%s status=requested", investigation.ID, agentStep, call.Function.Name, call.ID)
			step := diagnosis.InvestigationStep{
				Sequence:   sequence,
				AgentStep:  agentStep,
				ToolCallID: call.ID,
				Tool:       call.Function.Name,
				Status:     "running",
				StartedAt:  r.now(),
			}

			if call.Function.Name == SubmitDiagnosisTool {
				input, validationErr := decodeAndValidateSubmission(call.Function.Arguments, investigation)
				if validationErr != nil {
					lastDiagnosisValidation = validationErr
					failure := r.tools.Failure(SubmitDiagnosisTool, "invalid_diagnosis", validationErr.Error(), nil)
					step.Status = "failed"
					step.Error = validationErr.Error()
					step.CompletedAt = r.now()
					investigation.Steps = append(investigation.Steps, step)
					investigation.FailedToolCalls++
					r.logger.Printf("agent investigation=%s model_step=%d tool=%s tool_call_id=%s status=failed error=%q", investigation.ID, agentStep, call.Function.Name, call.ID, validationErr)
					message, marshalErr := toolResultMessage(call.ID, failure)
					if marshalErr != nil {
						investigation.Status = "failed"
						return result, runError("internal_error", marshalErr.Error(), investigation.ID, marshalErr)
					}
					messages = append(messages, message)
					continue
				}

				step.Arguments = canonicalJSON(input)
				step.Status = "success"
				step.CompletedAt = r.now()
				investigation.Steps = append(investigation.Steps, step)
				finalDiagnosis := r.materializeDiagnosis(investigation, input)
				investigation.Status = "completed"
				investigation.FinalDiagnosis = finalDiagnosis
				result.Diagnosis = finalDiagnosis
				r.logger.Printf("agent investigation=%s model_step=%d tool=%s tool_call_id=%s status=success diagnosis=%s evidence=%d", investigation.ID, agentStep, call.Function.Name, call.ID, finalDiagnosis.ID, len(finalDiagnosis.Evidence))
				r.logger.Printf("agent investigation=%s status=completed diagnosis=%s steps=%d", investigation.ID, finalDiagnosis.ID, len(finalDiagnosis.Investigation))
				return result, nil
			}

			observation, validatedArguments, executionErr := r.executeEvidenceTool(ctx, call)
			if executionErr != nil {
				if errors.Is(executionErr, errUnsupportedTool) {
					step.Status = "failed"
					step.Error = executionErr.Error()
					step.CompletedAt = r.now()
					investigation.Steps = append(investigation.Steps, step)
					investigation.FailedToolCalls++
					investigation.Status = "failed"
					r.logger.Printf("agent investigation=%s model_step=%d tool=%s tool_call_id=%s status=failed error=%q", investigation.ID, agentStep, call.Function.Name, call.ID, executionErr)
					return result, runError("unsupported_tool", executionErr.Error(), investigation.ID, executionErr)
				}
				investigation.Status = "failed"
				return result, runError("internal_error", executionErr.Error(), investigation.ID, executionErr)
			}
			if err := ctx.Err(); err != nil {
				investigation.Status = "cancelled"
				return result, runError("context_cancelled", "diagnosis request was cancelled", investigation.ID, err)
			}
			step.Arguments = validatedArguments
			step.CompletedAt = r.now()
			if observation.OK {
				step.Status = "success"
				step.ObservationID = observation.ObservationID
				investigation.storeObservation(observation)
			} else {
				step.Status = "failed"
				investigation.FailedToolCalls++
				if observation.Error != nil {
					step.Error = observation.Error.Message
				}
			}
			investigation.Steps = append(investigation.Steps, step)
			r.logger.Printf("agent investigation=%s model_step=%d tool=%s tool_call_id=%s status=%s observation_id=%s", investigation.ID, agentStep, call.Function.Name, call.ID, step.Status, step.ObservationID)
			message, marshalErr := toolResultMessage(call.ID, observation)
			if marshalErr != nil {
				investigation.Status = "failed"
				return result, runError("internal_error", marshalErr.Error(), investigation.ID, marshalErr)
			}
			messages = append(messages, message)
		}
	}

	investigation.Status = "failed"
	if lastDiagnosisValidation != nil {
		r.logger.Printf("agent investigation=%s status=failed error=diagnosis_validation_failed", investigation.ID)
		return result, runError("diagnosis_validation_failed", lastDiagnosisValidation.Error(), investigation.ID, lastDiagnosisValidation)
	}
	r.logger.Printf("agent investigation=%s status=failed error=max_steps_exceeded max_steps=%d", investigation.ID, r.maxSteps)
	return result, runError("max_steps_exceeded", fmt.Sprintf("agent exceeded the maximum of %d steps", r.maxSteps), investigation.ID, nil)
}

var errUnsupportedTool = errors.New("unsupported tool")

func (r *Runner) executeEvidenceTool(ctx context.Context, call ToolCall) (tools.Envelope, json.RawMessage, error) {
	switch call.Function.Name {
	case tools.ServiceStatusTool:
		var arguments struct{}
		if err := decodeArguments(call.Function.Arguments, &arguments); err != nil {
			return r.tools.Failure(tools.ServiceStatusTool, "invalid_argument", err.Error(), nil), nil, nil
		}
		return r.tools.GetServiceStatus(ctx), canonicalJSON(arguments), nil
	case tools.RuntimeMetricsTool:
		var arguments struct {
			WindowSeconds *int `json:"window_seconds"`
		}
		if err := decodeArguments(call.Function.Arguments, &arguments); err != nil {
			return r.tools.Failure(tools.RuntimeMetricsTool, "invalid_argument", err.Error(), nil), nil, nil
		}
		if arguments.WindowSeconds == nil {
			return r.tools.Failure(tools.RuntimeMetricsTool, "invalid_argument", "window_seconds is required", nil), nil, nil
		}
		return r.tools.GetRuntimeMetrics(*arguments.WindowSeconds), canonicalJSON(map[string]int{"window_seconds": *arguments.WindowSeconds}), nil
	case tools.GoroutineProfileTool:
		var arguments struct {
			TopN *int `json:"top_n"`
		}
		if err := decodeArguments(call.Function.Arguments, &arguments); err != nil {
			return r.tools.Failure(tools.GoroutineProfileTool, "invalid_argument", err.Error(), nil), nil, nil
		}
		if arguments.TopN == nil {
			return r.tools.Failure(tools.GoroutineProfileTool, "invalid_argument", "top_n is required", nil), nil, nil
		}
		return r.tools.GetGoroutineProfile(ctx, *arguments.TopN), canonicalJSON(map[string]int{"top_n": *arguments.TopN}), nil
	default:
		return tools.Envelope{}, nil, fmt.Errorf("%w: %s", errUnsupportedTool, call.Function.Name)
	}
}

func decodeArguments(raw string, destination any) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return errors.New("arguments must be a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("arguments must be valid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("arguments must contain exactly one JSON object")
		}
		return fmt.Errorf("arguments must contain exactly one JSON object: %w", err)
	}
	return nil
}

func canonicalJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func decodeAndValidateSubmission(raw string, investigation *Investigation) (SubmitDiagnosisInput, error) {
	var wire struct {
		Summary           string   `json:"summary"`
		RootCause         string   `json:"root_cause"`
		EvidenceRefs      []string `json:"evidence_refs"`
		Confidence        *float64 `json:"confidence"`
		AffectedComponent string   `json:"affected_component"`
		Recommendation    string   `json:"recommendation"`
	}
	if err := decodeArguments(raw, &wire); err != nil {
		return SubmitDiagnosisInput{}, err
	}
	if wire.Confidence == nil {
		return SubmitDiagnosisInput{}, errors.New("confidence is required")
	}
	input := SubmitDiagnosisInput{
		Summary:           wire.Summary,
		RootCause:         wire.RootCause,
		EvidenceRefs:      wire.EvidenceRefs,
		Confidence:        *wire.Confidence,
		AffectedComponent: wire.AffectedComponent,
		Recommendation:    wire.Recommendation,
	}
	input.Summary = strings.TrimSpace(input.Summary)
	input.RootCause = strings.TrimSpace(input.RootCause)
	input.AffectedComponent = strings.TrimSpace(input.AffectedComponent)
	input.Recommendation = strings.TrimSpace(input.Recommendation)
	switch {
	case input.Summary == "":
		return input, errors.New("summary must be non-empty")
	case input.RootCause == "":
		return input, errors.New("root_cause must be non-empty")
	case input.AffectedComponent == "":
		return input, errors.New("affected_component must be non-empty")
	case input.Recommendation == "":
		return input, errors.New("recommendation must be non-empty")
	case math.IsNaN(input.Confidence) || math.IsInf(input.Confidence, 0) || input.Confidence < 0 || input.Confidence > 1:
		return input, errors.New("confidence must be between 0 and 1")
	case len(input.EvidenceRefs) == 0:
		return input, errors.New("at least one evidence reference is required")
	}
	seen := make(map[string]struct{}, len(input.EvidenceRefs))
	for _, reference := range input.EvidenceRefs {
		if reference == "" {
			return input, errors.New("evidence references must be non-empty")
		}
		if _, duplicate := seen[reference]; duplicate {
			return input, fmt.Errorf("duplicate evidence reference %q", reference)
		}
		seen[reference] = struct{}{}
		observation, ok := investigation.observation(reference)
		if !ok || !observation.OK {
			return input, fmt.Errorf("unknown evidence reference %q for investigation %s", reference, investigation.ID)
		}
	}
	return input, nil
}

func (r *Runner) materializeDiagnosis(investigation *Investigation, input SubmitDiagnosisInput) *diagnosis.Diagnosis {
	evidence := make([]diagnosis.Evidence, 0, len(input.EvidenceRefs))
	for _, reference := range input.EvidenceRefs {
		observation, _ := investigation.observation(reference)
		evidence = append(evidence, diagnosis.Evidence{
			ObservationID: observation.ObservationID,
			SourceTool:    observation.Tool,
			CapturedAt:    observation.CapturedAt,
			Description:   describeEvidence(observation),
			Data:          observation.Data,
		})
	}
	return &diagnosis.Diagnosis{
		ID:                r.ids.next("diag"),
		InvestigationID:   investigation.ID,
		GeneratedAt:       r.now(),
		Summary:           input.Summary,
		RootCause:         input.RootCause,
		Evidence:          evidence,
		Confidence:        input.Confidence,
		AffectedComponent: input.AffectedComponent,
		Recommendation:    input.Recommendation,
		Investigation:     append([]diagnosis.InvestigationStep(nil), investigation.Steps...),
	}
}

func describeEvidence(observation tools.Envelope) string {
	switch data := observation.Data.(type) {
	case tools.ServiceStatusData:
		return fmt.Sprintf("Service %s reported health %s with %d goroutines and %d active intentional leaks.", data.ServiceIdentity, data.HealthStatus, data.CurrentGoroutines, data.ActiveIntentionalLeak)
	case tools.RuntimeMetricsData:
		return fmt.Sprintf("Goroutine count changed from %d to %d over %.3f seconds (delta %+d).", data.Goroutines.Start, data.Goroutines.End, data.Window.ActualSeconds, data.Goroutines.AbsoluteDelta)
	case tools.GoroutineProfileData:
		for _, group := range data.Groups {
			if group.ContainsIntentionalLeak {
				frame := "a recognized intentional leak frame"
				for _, candidate := range group.Frames {
					if strings.Contains(candidate, "waitForever") {
						frame = candidate
						break
					}
				}
				return fmt.Sprintf("%d goroutines shared the stack containing %s and were in state %s.", group.Count, frame, group.State)
			}
		}
		if len(data.Groups) > 0 {
			return fmt.Sprintf("Captured %d goroutines across %d returned stack groups; the largest group contained %d goroutines in state %s.", data.TotalGoroutines, len(data.Groups), data.Groups[0].Count, data.Groups[0].State)
		}
		return fmt.Sprintf("Captured a goroutine profile reporting %d goroutines and no parsed stack groups.", data.TotalGoroutines)
	default:
		return fmt.Sprintf("Successful observation returned by %s.", observation.Tool)
	}
}

func runError(code, message, investigationID string, cause error) *RunError {
	return &RunError{Code: code, Message: message, InvestigationID: investigationID, Cause: cause}
}
