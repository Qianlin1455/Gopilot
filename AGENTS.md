# GoPilot Repository Instructions

## Project Goal

GoPilot is an AI-assisted diagnostic system for Go services.

Its primary goal is to diagnose runtime failures using observable evidence rather than allowing the LLM to guess directly.

The core diagnostic loop is:

Observe → Hypothesize → Collect Evidence → Verify → Diagnose → Recommend

The demo should prioritize a complete and reliable diagnostic workflow over feature quantity.

## Current Demo Scope

The first demo focuses on one scenario:

Goroutine Leak Diagnosis.

The complete flow should be:

1. Start a demo Go service.
2. Trigger a goroutine leak intentionally.
3. Collect runtime metrics and pprof evidence.
4. Expose diagnostic tools to the Agent runtime.
5. Let the Agent investigate the failure through tool calls.
6. Produce a structured diagnosis.
7. Display the diagnosis and evidence in the web UI.

Do not introduce unrelated failure scenarios unless explicitly requested.

## Technology

Backend:

- Go
- Gin or standard net/http
- Go runtime metrics
- net/http/pprof
- Prometheus Go client where useful

Agent:

- OpenAI-compatible Chat Completions API
- Function Calling / Tool Calling
- Multi-step Agent loop
- Tools must return real evidence from the demo service

Frontend:

- Keep the frontend lightweight.
- Prefer a small Vite/React application if a dashboard is required.
- Avoid unnecessary frontend abstractions.

Deployment:

- Docker
- Docker Compose

## Repository Structure

Prefer the following structure:

cmd/
gopilot/
demo-service/

internal/
agent/
tools/
diagnosis/
collector/

web/

docs/

deploy/

Keep business logic out of cmd/.

## Engineering Principles

Prioritize:

1. Working demo
2. Clear architecture
3. Observability
4. Reproducibility
5. Readability

Do not over-engineer the demo.

Do not introduce Kubernetes, eBPF, Kafka, service mesh, distributed storage, or additional infrastructure unless explicitly requested.

Do not mock diagnostic evidence when real Go runtime information can be collected locally.

LLM conclusions must be based on tool results.

The Agent must not claim evidence that was not returned by a tool.

## Agent Design

The Agent runtime should follow:

User Request
→ LLM
→ Tool Call
→ Tool Execution
→ Observation
→ LLM
→ Additional Tool Calls if necessary
→ Final Diagnosis

The Agent should support a bounded number of reasoning/tool steps.

Every tool should:

- have a clear name
- have a clear description
- validate arguments
- return structured data
- report errors explicitly

## Diagnosis Output

Prefer structured results containing:

- summary
- root_cause
- evidence
- confidence
- affected_component
- recommendation

Evidence should refer to actual collected runtime information.

## Development Workflow

Before implementing a substantial feature:

1. Inspect the relevant existing code.
2. Describe the implementation approach.
3. Identify files that will change.
4. Implement the smallest complete vertical slice.
5. Run formatting and tests.
6. Start the application when practical.
7. Verify the user-visible behavior.

Do not rewrite working modules unnecessarily.

## Verification

Before considering a task complete, run the relevant checks.

For Go code:

go fmt ./...
go vet ./...
go test ./...

If Docker Compose is involved, verify:

docker compose config

When practical, start the demo and verify the intended workflow manually.

A task is not complete merely because the code compiles.

## Git Discipline

Keep changes scoped to the requested task.

Do not modify unrelated files.

Do not delete existing functionality unless explicitly required.

Prefer small, understandable changes that can be reviewed independently.

## Documentation

Keep architecture and product-specific details under docs/.

AGENTS.md should contain persistent repository rules, not temporary implementation plans.

When architecture changes materially, update the corresponding documentation.