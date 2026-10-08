package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendsChatCompletionsToolRequest(t *testing.T) {
	var received chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeCompletion(t, w, ToolCall{ID: "call_status", Type: "function", Function: FunctionCall{Name: "get_service_status", Arguments: `{}`}})
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/v1", "secret", "test-model", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	message, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "diagnose"}}, ToolDefinitions())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if received.Model != "test-model" || received.ToolChoice != "required" || len(received.Tools) != 4 {
		t.Fatalf("request = %+v", received)
	}
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].ID != "call_status" {
		t.Fatalf("message = %+v", message)
	}
}

func TestClientThinkingConfiguration(t *testing.T) {
	testCases := []struct {
		name        string
		options     []ClientOption
		wantPresent bool
		wantType    ThinkingMode
	}{
		{name: "unset"},
		{name: "disabled", options: []ClientOption{WithThinking(ThinkingDisabled)}, wantPresent: true, wantType: ThinkingDisabled},
		{name: "enabled", options: []ClientOption{WithThinking(ThinkingEnabled)}, wantPresent: true, wantType: ThinkingEnabled},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				var toolChoice string
				if err := json.Unmarshal(request["tool_choice"], &toolChoice); err != nil {
					t.Errorf("decode tool_choice: %v", err)
				}
				if toolChoice != "required" {
					t.Errorf("tool_choice = %q, want required", toolChoice)
				}
				rawThinking, present := request["thinking"]
				if present != testCase.wantPresent {
					t.Errorf("thinking present = %t, want %t; request=%s", present, testCase.wantPresent, request["thinking"])
				}
				if present {
					var thinking thinkingConfiguration
					if err := json.Unmarshal(rawThinking, &thinking); err != nil {
						t.Errorf("decode thinking: %v", err)
					}
					if thinking.Type != testCase.wantType {
						t.Errorf("thinking.type = %q, want %q", thinking.Type, testCase.wantType)
					}
				}
				writeCompletion(t, w, ToolCall{ID: "call_status", Type: "function", Function: FunctionCall{Name: "get_service_status", Arguments: `{}`}})
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "", "test-model", server.Client(), testCase.options...)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if _, err := client.Complete(context.Background(), nil, ToolDefinitions()); err != nil {
				t.Fatalf("Complete: %v", err)
			}
		})
	}
}

func TestClientRejectsInvalidThinkingMode(t *testing.T) {
	if _, err := NewClient("http://localhost:1234", "", "test-model", nil, WithThinking("automatic")); err == nil {
		t.Fatal("NewClient accepted an unsupported thinking mode")
	}
}

func TestClientProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","code":"rate_limit"}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "test-model", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "diagnose"}}, ToolDefinitions())
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.HTTPStatus != http.StatusTooManyRequests || providerError.Code != "rate_limit" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "test-model", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), nil, ToolDefinitions())
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "malformed_response" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientAcceptsNullContentMultipleToolCallsAndNonstandardFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {"id":"call_one","type":"function","function":{"name":"get_service_status","arguments":"{}"}},
        {"id":"call_two","type":"function","function":{"name":"get_runtime_metrics","arguments":"{\"window_seconds\":30}"}}
      ]
    },
    "finish_reason": "stop"
  }]
}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "test-model", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	message, err := client.Complete(context.Background(), nil, ToolDefinitions())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if message.Content != "" || len(message.ToolCalls) != 2 || message.ToolCalls[1].ID != "call_two" {
		t.Fatalf("message = %+v", message)
	}
}

func TestClientRejectsMalformedToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"","type":"function","function":{"name":"get_service_status","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "test-model", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), nil, ToolDefinitions())
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "malformed_response" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		baseURL string
		model   string
	}{
		{baseURL: "file:///tmp/provider", model: "model"},
		{baseURL: "http://localhost:1234", model: "  "},
	} {
		if _, err := NewClient(testCase.baseURL, "", testCase.model, nil); err == nil {
			t.Fatalf("NewClient(%q, model=%q) succeeded", testCase.baseURL, testCase.model)
		}
	}
}

func writeCompletion(t *testing.T, w http.ResponseWriter, calls ...ToolCall) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message": Message{Role: "assistant", ToolCalls: calls},
		}},
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
