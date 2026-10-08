# GoPilot runtime demo

GoPilot currently demonstrates a real, controlled goroutine leak, a minimal
runtime dashboard, structured evidence collection, and a bounded diagnostic
Agent that can use an OpenAI-compatible Chat Completions provider.

## Run locally

Requirements: Go 1.23 or newer, Node.js, and npm.

Start the demo service from the repository root:

```powershell
go run ./cmd/demo-service
```

In a second terminal, configure an OpenAI-compatible provider and start GoPilot:

```powershell
$env:DEMO_SERVICE_URL = "http://localhost:8081"
$env:LLM_BASE_URL = "https://api.openai.com/v1"
$env:LLM_API_KEY = "your-api-key"
$env:LLM_MODEL = "your-tool-capable-model"
go run ./cmd/gopilot
```

`LLM_API_KEY` may be empty for a local provider that does not require
authentication. If `LLM_BASE_URL` or `LLM_MODEL` is absent, GoPilot keeps the
evidence endpoints available and returns `agent_not_configured` from the
diagnosis endpoint rather than fabricating a result.

The evidence endpoints are available on <http://localhost:8080>:

- `GET /healthz`
- `GET /api/tools/service-status`
- `GET /api/tools/runtime-metrics?window_seconds=30`
- `GET /api/tools/goroutine-profile?top_n=10`
- `POST /api/diagnoses` with an optional body such as
  `{"question":"Diagnose the current service problem."}`

In another terminal, start the optional dashboard:

```powershell
cd web
npm ci
npm run dev
```

Open <http://localhost:3000>. The dashboard polls the service at
<http://localhost:8081> once per second and sends diagnosis requests to GoPilot
at <http://localhost:8080>. During local development, Vite proxies these as
`/demo/*` and `/gopilot/*`, so the browser does not need cross-origin access.
Use **Inject 25 leaks** to create the controlled failure, **Diagnose service**
to run the evidence-grounded Agent, and **Reset** to stop all intentionally
leaked goroutines while retaining the completed investigation for review.

The real goroutine profile is available at
<http://localhost:8081/debug/pprof/goroutine?debug=2>.

## API

- `GET /healthz`
- `GET /api/runtime`
- `GET /api/leak/status`
- `POST /api/leak` with `{"count": 25}` (1-100 per request, 500 active maximum)
- `POST /api/leak/reset`
- `GET /debug/pprof/`

Set `DEMO_ADDR` to change the service listen address. Set
`VITE_DEMO_API_URL` or `VITE_GOPILOT_API_URL` when the dashboard should use URLs
other than the local Vite proxies. Set `GOPILOT_ADDR` to change the evidence
backend listen address and `DEMO_SERVICE_URL` to configure its single
collection target. The diagnosis Agent runs at most eight model steps and
accepts a final diagnosis only through a grounded `submit_diagnosis` tool call.
