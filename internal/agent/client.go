package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxProviderResponseBytes = 4 << 20

type Client struct {
	endpoint   string
	apiKey     string
	model      string
	thinking   *thinkingConfiguration
	httpClient *http.Client
}

type ThinkingMode string

const (
	ThinkingEnabled  ThinkingMode = "enabled"
	ThinkingDisabled ThinkingMode = "disabled"
)

type ClientOption func(*Client) error

type thinkingConfiguration struct {
	Type ThinkingMode `json:"type"`
}

func WithThinking(mode ThinkingMode) ClientOption {
	return func(client *Client) error {
		switch mode {
		case ThinkingEnabled, ThinkingDisabled:
			client.thinking = &thinkingConfiguration{Type: mode}
			return nil
		default:
			return fmt.Errorf("LLM thinking must be %q or %q", ThinkingEnabled, ThinkingDisabled)
		}
	}
}

type ProviderError struct {
	Code       string
	Message    string
	HTTPStatus int
	Cause      error
}

func (e *ProviderError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("provider returned HTTP %d: %s", e.HTTPStatus, e.Message)
	}
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Cause
}

type chatCompletionRequest struct {
	Model      string                 `json:"model"`
	Messages   []Message              `json:"messages"`
	Tools      []ToolDefinition       `json:"tools"`
	ToolChoice string                 `json:"tool_choice"`
	Thinking   *thinkingConfiguration `json:"thinking,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func NewClient(baseURL, apiKey, model string, httpClient *http.Client, options ...ClientOption) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse LLM base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("LLM base URL must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("LLM base URL must contain only scheme, host, and optional base path")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("LLM model is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	client := &Client{
		endpoint:   strings.TrimRight(baseURL, "/") + "/chat/completions",
		apiKey:     apiKey,
		model:      model,
		httpClient: httpClient,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(client); err != nil {
			return nil, err
		}
	}
	return client, nil
}

func (c *Client) Complete(ctx context.Context, messages []Message, definitions []ToolDefinition) (Message, error) {
	requestBody, err := json.Marshal(chatCompletionRequest{
		Model:      c.model,
		Messages:   messages,
		Tools:      definitions,
		ToolChoice: "required",
		Thinking:   c.thinking,
	})
	if err != nil {
		return Message{}, &ProviderError{Code: "request_encoding_failed", Message: "encode provider request", Cause: err}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return Message{}, &ProviderError{Code: "request_creation_failed", Message: "create provider request", Cause: err}
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Message{}, &ProviderError{Code: "request_failed", Message: "provider request failed", Cause: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return Message{}, &ProviderError{Code: "response_read_failed", Message: "read provider response", Cause: err}
	}
	if len(body) > maxProviderResponseBytes {
		return Message{}, &ProviderError{Code: "response_too_large", Message: "provider response exceeded 4 MiB limit"}
	}

	var result chatCompletionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return Message{}, &ProviderError{Code: "malformed_response", Message: "provider returned invalid JSON", HTTPStatus: response.StatusCode, Cause: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := "provider request failed"
		code := "provider_http_error"
		if result.Error != nil {
			message = result.Error.Message
			if result.Error.Code != "" {
				code = result.Error.Code
			}
		}
		return Message{}, &ProviderError{Code: code, Message: message, HTTPStatus: response.StatusCode}
	}
	if len(result.Choices) != 1 {
		return Message{}, &ProviderError{Code: "malformed_response", Message: "provider response must contain exactly one choice"}
	}
	message := result.Choices[0].Message
	if message.Role != "assistant" {
		return Message{}, &ProviderError{Code: "malformed_response", Message: "provider choice must contain an assistant message"}
	}
	seenIDs := make(map[string]struct{}, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if call.ID == "" || call.Type != "function" || call.Function.Name == "" {
			return Message{}, &ProviderError{Code: "malformed_response", Message: "provider returned a malformed tool call"}
		}
		if _, duplicate := seenIDs[call.ID]; duplicate {
			return Message{}, &ProviderError{Code: "malformed_response", Message: "provider returned duplicate tool-call IDs"}
		}
		seenIDs[call.ID] = struct{}{}
	}
	return message, nil
}
