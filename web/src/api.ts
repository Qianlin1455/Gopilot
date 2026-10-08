export type Health = {
  status: string
  service: string
  started_at?: string
}

export type RuntimeSnapshot = {
  timestamp: string
  go_version: string
  goroutines: number
  heap_alloc_bytes: number
  heap_objects: number
  uptime_ms: number
}

export type LeakStatus = {
  active: number
  generation: number
  max_batch: number
  max_active: number
}

export type InvestigationStep = {
  sequence: number
  agent_step: number
  tool_call_id: string
  tool: string
  arguments?: Record<string, unknown>
  status: 'running' | 'success' | 'failed' | string
  observation_id?: string
  error?: string
  started_at: string
  completed_at: string
}

export type Evidence = {
  observation_id: string
  source_tool: string
  captured_at: string
  description: string
  data: unknown
}

export type Diagnosis = {
  id: string
  investigation_id: string
  generated_at: string
  summary: string
  root_cause: string
  evidence: Evidence[]
  confidence: number
  affected_component: string
  recommendation: string
  investigation: InvestigationStep[]
}

type APIErrorBody = {
  error?: string | { code?: string; message?: string }
  code?: string
  investigation_id?: string
}

export class APIError extends Error {
  readonly code: string
  readonly status: number
  readonly investigationID?: string

  constructor(message: string, code: string, status = 0, investigationID?: string) {
    super(message)
    this.name = 'APIError'
    this.code = code
    this.status = status
    this.investigationID = investigationID
  }
}

const DEMO_API_BASE = import.meta.env.VITE_DEMO_API_URL ?? '/demo'
const GOPILOT_API_BASE = import.meta.env.VITE_GOPILOT_API_URL ?? '/gopilot'

async function request<T>(base: string, path: string, init?: RequestInit, timeoutMs = 8_000): Promise<T> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs)

  try {
    const response = await fetch(`${base}${path}`, { ...init, signal: controller.signal })
    const body = await response.json().catch(() => null) as APIErrorBody | T | null
    if (!response.ok) {
      const errorValue = (body as APIErrorBody | null)?.error
      const message = typeof errorValue === 'string'
        ? errorValue
        : errorValue?.message ?? `${response.status} ${response.statusText}`
      const code = typeof errorValue === 'object'
        ? errorValue?.code ?? 'request_failed'
        : (body as APIErrorBody | null)?.code ?? 'request_failed'
      throw new APIError(message, code, response.status, (body as APIErrorBody | null)?.investigation_id)
    }
    return body as T
  } catch (error) {
    if (error instanceof APIError) throw error
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new APIError('The request timed out before the server responded.', 'request_timeout')
    }
    throw new APIError('Unable to reach the service. Check that the local processes are running.', 'backend_unavailable')
  } finally {
    window.clearTimeout(timeout)
  }
}

export const api = {
  demoHealth: () => request<Health>(DEMO_API_BASE, '/healthz'),
  gopilotHealth: () => request<Health>(GOPILOT_API_BASE, '/healthz'),
  runtime: () => request<RuntimeSnapshot>(DEMO_API_BASE, '/api/runtime'),
  leakStatus: () => request<LeakStatus>(DEMO_API_BASE, '/api/leak/status'),
  inject: (count: number) =>
    request<LeakStatus>(DEMO_API_BASE, '/api/leak', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ count }),
    }),
  reset: () => request<LeakStatus>(DEMO_API_BASE, '/api/leak/reset', { method: 'POST' }),
  diagnose: () => request<Diagnosis>(GOPILOT_API_BASE, '/api/diagnoses', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ question: 'Diagnose the current Go service problem using runtime evidence.' }),
  }, 180_000),
}
