package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"gopilot/internal/agent"
	"gopilot/internal/tools"
)

const maxDiagnosisRequestBytes = 8 << 10

type Diagnoser interface {
	Run(context.Context, string) (agent.Result, error)
}

type Server struct {
	diagnoser Diagnoser
	handler   http.Handler
}

type diagnosisRequest struct {
	Question string `json:"question"`
}

type errorBody struct {
	OK              bool       `json:"ok"`
	Error           errorValue `json:"error"`
	InvestigationID string     `json:"investigation_id,omitempty"`
}

type errorValue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewServer(toolService *tools.Service, diagnoser Diagnoser) *Server {
	server := &Server{diagnoser: diagnoser}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/diagnoses", server.createDiagnosis)
	mux.Handle("/", tools.NewHTTPHandler(toolService))
	server.handler = mux
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) createDiagnosis(w http.ResponseWriter, r *http.Request) {
	if s.diagnoser == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{
			OK: false,
			Error: errorValue{
				Code:    "agent_not_configured",
				Message: "set LLM_BASE_URL and LLM_MODEL to enable diagnoses",
			},
		})
		return
	}

	request := diagnosisRequest{Question: "Diagnose the current service problem."}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDiagnosisRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "body must be a JSON object containing an optional question", "")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "body must contain exactly one JSON object", "")
		return
	}
	request.Question = strings.TrimSpace(request.Question)
	if request.Question == "" {
		request.Question = "Diagnose the current service problem."
	}
	if len(request.Question) > 2000 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "question must not exceed 2000 characters", "")
		return
	}

	result, err := s.diagnoser.Run(r.Context(), request.Question)
	if err != nil {
		status := http.StatusInternalServerError
		code := "internal_error"
		message := err.Error()
		investigationID := ""
		var runError *agent.RunError
		if errors.As(err, &runError) {
			code = runError.Code
			message = runError.Message
			investigationID = runError.InvestigationID
			switch runError.Code {
			case "provider_error":
				status = http.StatusBadGateway
			case "context_cancelled":
				status = http.StatusRequestTimeout
			case "unsupported_tool", "malformed_model_response", "max_steps_exceeded", "diagnosis_validation_failed":
				status = http.StatusUnprocessableEntity
			}
		}
		writeAPIError(w, status, code, message, investigationID)
		return
	}
	if result.Diagnosis == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "agent completed without a diagnosis", result.Investigation.ID)
		return
	}
	writeJSON(w, http.StatusOK, result.Diagnosis)
}

func writeAPIError(w http.ResponseWriter, status int, code, message, investigationID string) {
	writeJSON(w, status, errorBody{
		OK:              false,
		Error:           errorValue{Code: code, Message: message},
		InvestigationID: investigationID,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
