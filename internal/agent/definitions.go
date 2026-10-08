package agent

import "gopilot/internal/tools"

func ToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        tools.ServiceStatusTool,
				Description: "Get the configured Go service's health, identity, latency, uptime, current goroutine count, and intentional leak state.",
				Parameters:  objectSchema(map[string]any{}, []string{}),
			},
		},
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        tools.RuntimeMetricsTool,
				Description: "Get real sampled runtime metrics and goroutine trend evidence for a recent time window.",
				Parameters: objectSchema(map[string]any{
					"window_seconds": map[string]any{
						"type":    "integer",
						"minimum": tools.MinMetricsWindow,
						"maximum": tools.MaxMetricsWindow,
					},
				}, []string{"window_seconds"}),
			},
		},
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        tools.GoroutineProfileTool,
				Description: "Capture and group the real goroutine pprof profile into bounded stack evidence.",
				Parameters: objectSchema(map[string]any{
					"top_n": map[string]any{
						"type":    "integer",
						"minimum": tools.MinProfileTopN,
						"maximum": tools.MaxProfileTopN,
					},
				}, []string{"top_n"}),
			},
		},
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        SubmitDiagnosisTool,
				Description: "Finish the investigation with a diagnosis grounded only in successful observation IDs from this investigation.",
				Parameters: objectSchema(map[string]any{
					"summary":            map[string]any{"type": "string", "minLength": 1},
					"root_cause":         map[string]any{"type": "string", "minLength": 1},
					"evidence_refs":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "uniqueItems": true},
					"confidence":         map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"affected_component": map[string]any{"type": "string", "minLength": 1},
					"recommendation":     map[string]any{"type": "string", "minLength": 1},
				}, []string{"summary", "root_cause", "evidence_refs", "confidence", "affected_component", "recommendation"}),
			},
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
