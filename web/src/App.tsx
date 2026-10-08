import { useCallback, useEffect, useState } from 'react'
import {
  APIError,
  api,
  type Diagnosis,
  type Evidence,
  type Health,
  type InvestigationStep,
  type LeakStatus,
  type RuntimeSnapshot,
} from './api'

const MAX_POINTS = 60
const LEAK_BATCH = 25

type RuntimeMetricsEvidence = {
  samples?: RuntimeSnapshot[]
  sample_count?: number
  goroutines?: {
    start?: number
    end?: number
    minimum?: number
    maximum?: number
    absolute_delta?: number
    growth_rate_per_second?: number
  }
  capture_window?: { actual_seconds?: number; requested_seconds?: number }
  insufficient_history?: boolean
  insufficient_history_reason?: string
}

type ProfileGroup = {
  signature?: string
  count?: number
  state?: string
  frames?: string[]
  contains_intentional_leak?: boolean
}

type ProfileEvidence = {
  total_goroutine_count?: number
  stack_groups?: ProfileGroup[]
  truncation?: { groups_truncated?: boolean; input_truncated?: boolean }
}

type ServiceEvidence = {
  service_identity?: string
  health_status?: string
  http_status?: number
  request_latency_ms?: number
  uptime_ms?: number
  current_goroutine_count?: number
  active_intentional_leak_count?: number
}

function formatUptime(milliseconds?: number) {
  if (milliseconds === undefined) return '—'
  const seconds = Math.floor(milliseconds / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

function formatTime(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value))
}

function formatDelta(value?: number) {
  if (value === undefined) return '—'
  return `${value > 0 ? '+' : ''}${value}`
}

function TrendChart({ samples, compact = false }: { samples: RuntimeSnapshot[]; compact?: boolean }) {
  const width = 1000
  const height = compact ? 126 : 238
  const paddingX = compact ? 10 : 30
  const paddingY = compact ? 12 : 24
  const counts = samples.map((sample) => sample.goroutines)
  const minimum = counts.length ? Math.min(...counts) : 0
  const maximum = counts.length ? Math.max(...counts) : 0
  const range = Math.max(maximum - minimum, 1)
  const points = samples.map((sample, index) => ({
    x: paddingX + (index / Math.max(samples.length - 1, 1)) * (width - paddingX * 2),
    y: height - paddingY - ((sample.goroutines - minimum) / range) * (height - paddingY * 2),
  }))
  const line = points.map(({ x, y }) => `${x},${y}`).join(' ')
  const area = points.length > 1
    ? `${paddingX},${height - paddingY} ${line} ${width - paddingX},${height - paddingY}`
    : ''
  const gradientID = compact ? 'evidence-area' : 'runtime-area'

  return (
    <div className={`chart-wrap ${compact ? 'compact-chart' : ''}`}>
      {!compact && <div className="chart-value chart-value-high">{samples.length ? maximum : '—'}</div>}
      {!compact && <div className="chart-value chart-value-low">{samples.length ? minimum : '—'}</div>}
      {!samples.length && <div className="chart-empty">Waiting for runtime samples…</div>}
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Recent goroutine count">
        <title>Recent goroutine count</title>
        <desc>{samples.length ? `Goroutine count ranges from ${minimum} to ${maximum} across ${samples.length} samples.` : 'No runtime samples have been collected yet.'}</desc>
        <defs>
          <linearGradient id={gradientID} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#67f4c5" stopOpacity="0.3" />
            <stop offset="100%" stopColor="#67f4c5" stopOpacity="0" />
          </linearGradient>
        </defs>
        {!compact && [0.25, 0.5, 0.75].map((ratio) => (
          <line key={ratio} x1={paddingX} y1={height * ratio} x2={width - paddingX} y2={height * ratio} className="chart-grid" />
        ))}
        {points.length > 1 && (
          <>
            <polygon points={area} fill={`url(#${gradientID})`} />
            <polyline points={line} className="trend-line" />
            <circle cx={points.at(-1)?.x} cy={points.at(-1)?.y} r={compact ? 4 : 5} className="trend-dot" />
          </>
        )}
        {points.length === 1 && <circle cx={width / 2} cy={height / 2} r="5" className="trend-dot" />}
      </svg>
    </div>
  )
}

function SectionHeading({ number, eyebrow, title, description, action }: {
  number: string
  eyebrow: string
  title: string
  description: string
  action?: React.ReactNode
}) {
  return (
    <div className="section-heading">
      <div className="section-number">{number}</div>
      <div className="section-copy">
        <div className="eyebrow">{eyebrow}</div>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      {action && <div className="section-action">{action}</div>}
    </div>
  )
}

function StatusPill({ online, label }: { online: boolean | null; label: string }) {
  const state = online === null ? 'Checking' : online ? 'Online' : 'Offline'
  return (
    <div className={`status-pill ${online === false ? 'is-offline' : ''}`} role="status" aria-label={`${label}: ${state}`}>
      <span className={`status-dot ${online ? 'online' : ''}`} aria-hidden="true" />
      <span>{label}</span>
      <strong>{state}</strong>
    </div>
  )
}

function Timeline({ steps }: { steps: InvestigationStep[] }) {
  if (!steps.length) {
    return <div className="empty-state compact-empty">No tool calls were returned.</div>
  }
  return (
    <ol className="timeline-list">
      {steps.map((step) => (
        <li key={`${step.sequence}-${step.tool_call_id}`} className={`timeline-step is-${step.status}`}>
          <div className="timeline-marker" aria-hidden="true">{step.status === 'success' ? '✓' : step.status === 'failed' ? '!' : '·'}</div>
          <div className="timeline-body">
            <div className="timeline-title-row">
              <div>
                <span className="timeline-sequence">{String(step.sequence).padStart(2, '0')}</span>
                <strong>{step.tool}</strong>
              </div>
              <span className={`status-tag ${step.status}`}>{step.status}</span>
            </div>
            <div className="timeline-meta">
              <span>model step {step.agent_step}</span>
              <span>{formatTime(step.completed_at || step.started_at)}</span>
            </div>
            <code className="argument-block">{JSON.stringify(step.arguments ?? {})}</code>
            {step.observation_id && <div className="observation-ref">↳ {step.observation_id}</div>}
            {step.error && <div className="step-error">{step.error}</div>}
          </div>
        </li>
      ))}
    </ol>
  )
}

function ServiceEvidenceView({ data }: { data: ServiceEvidence }) {
  return (
    <div className="evidence-metrics">
      <div><span>Health</span><strong className="text-success">{data.health_status ?? '—'}</strong></div>
      <div><span>HTTP</span><strong>{data.http_status ?? '—'}</strong></div>
      <div><span>Latency</span><strong>{data.request_latency_ms ?? '—'} ms</strong></div>
      <div><span>Goroutines</span><strong>{data.current_goroutine_count ?? '—'}</strong></div>
      <div><span>Active leaks</span><strong className="text-warning">{data.active_intentional_leak_count ?? '—'}</strong></div>
      <div><span>Uptime</span><strong>{formatUptime(data.uptime_ms)}</strong></div>
    </div>
  )
}

function RuntimeEvidenceView({ data }: { data: RuntimeMetricsEvidence }) {
  const samples = data.samples ?? []
  return (
    <div className="evidence-runtime">
      <div className="evidence-metrics evidence-metrics-four">
        <div><span>Start</span><strong>{data.goroutines?.start ?? '—'}</strong></div>
        <div><span>End</span><strong>{data.goroutines?.end ?? '—'}</strong></div>
        <div><span>Delta</span><strong className="text-warning">{formatDelta(data.goroutines?.absolute_delta)}</strong></div>
        <div><span>Rate</span><strong>{data.goroutines?.growth_rate_per_second ?? '—'}/s</strong></div>
      </div>
      <TrendChart samples={samples} compact />
      <div className="evidence-footnote">
        {data.sample_count ?? samples.length} real samples · {data.capture_window?.actual_seconds ?? '—'}s observed
        {data.insufficient_history && <span className="history-note"> · partial window: {data.insufficient_history_reason}</span>}
      </div>
    </div>
  )
}

function ProfileEvidenceView({ data }: { data: ProfileEvidence }) {
  const groups = data.stack_groups ?? []
  return (
    <div className="profile-evidence">
      <div className="profile-summary">
        <strong>{data.total_goroutine_count ?? '—'}</strong>
        <span>goroutines captured across {groups.length} stack groups</span>
      </div>
      <div className="stack-list">
        {groups.map((group, index) => (
          <div className={`stack-group ${group.contains_intentional_leak ? 'is-leak' : ''}`} key={group.signature ?? index}>
            <div className="stack-group-head">
              <div>
                <strong>{group.count ?? '—'}×</strong>
                <span>{group.state ?? 'unknown state'}</span>
              </div>
              {group.contains_intentional_leak && <span className="leak-badge">Leak signature</span>}
            </div>
            <pre>{(group.frames ?? []).join('\n')}</pre>
          </div>
        ))}
      </div>
      {(data.truncation?.groups_truncated || data.truncation?.input_truncated) && (
        <div className="history-note">Profile output was bounded by the evidence collector.</div>
      )}
    </div>
  )
}

function EvidenceCard({ evidence, index }: { evidence: Evidence; index: number }) {
  const [expanded, setExpanded] = useState(index === 0)
  const contentID = `evidence-${evidence.observation_id}`
  return (
    <article className={`evidence-card ${expanded ? 'is-expanded' : ''}`}>
      <button className="evidence-toggle" onClick={() => setExpanded((current) => !current)} aria-expanded={expanded} aria-controls={contentID}>
        <div className="evidence-index">{String(index + 1).padStart(2, '0')}</div>
        <div className="evidence-title">
          <strong>{evidence.source_tool}</strong>
          <span>{evidence.description}</span>
        </div>
        <div className="evidence-capture">
          <span>{formatTime(evidence.captured_at)}</span>
          <code>{evidence.observation_id}</code>
        </div>
        <span className="chevron" aria-hidden="true">{expanded ? '−' : '+'}</span>
      </button>
      {expanded && (
        <div className="evidence-content" id={contentID}>
          {evidence.source_tool === 'get_service_status' && <ServiceEvidenceView data={evidence.data as ServiceEvidence} />}
          {evidence.source_tool === 'get_runtime_metrics' && <RuntimeEvidenceView data={evidence.data as RuntimeMetricsEvidence} />}
          {evidence.source_tool === 'get_goroutine_profile' && <ProfileEvidenceView data={evidence.data as ProfileEvidence} />}
          {!['get_service_status', 'get_runtime_metrics', 'get_goroutine_profile'].includes(evidence.source_tool) && (
            <pre>{JSON.stringify(evidence.data, null, 2)}</pre>
          )}
        </div>
      )}
    </article>
  )
}

function ErrorNotice({ error }: { error: APIError }) {
  const title = error.code === 'agent_not_configured'
    ? 'Agent not configured'
    : error.code === 'request_timeout'
      ? 'Diagnosis timed out'
      : error.code === 'backend_unavailable'
        ? 'GoPilot backend unavailable'
        : error.status >= 500
          ? 'Provider request failed'
          : 'Investigation failed'
  return (
    <div className="error-notice" role="alert">
      <div className="error-icon">!</div>
      <div>
        <strong>{title}</strong>
        <p>{error.message}</p>
        {error.investigationID && <code>{error.investigationID}</code>}
      </div>
    </div>
  )
}

function App() {
  const [health, setHealth] = useState<Health | null>(null)
  const [gopilotOnline, setGopilotOnline] = useState<boolean | null>(null)
  const [leakStatus, setLeakStatus] = useState<LeakStatus | null>(null)
  const [samples, setSamples] = useState<RuntimeSnapshot[]>([])
  const [baselineGoroutines, setBaselineGoroutines] = useState<number | null>(null)
  const [runtimeError, setRuntimeError] = useState<string | null>(null)
  const [controlBusy, setControlBusy] = useState(false)
  const [diagnosing, setDiagnosing] = useState(false)
  const [diagnosis, setDiagnosis] = useState<Diagnosis | null>(null)
  const [diagnosisError, setDiagnosisError] = useState<APIError | null>(null)

  const poll = useCallback(async () => {
    const [demoResult, gopilotResult] = await Promise.allSettled([
      Promise.all([api.demoHealth(), api.runtime(), api.leakStatus()]),
      api.gopilotHealth(),
    ])

    if (demoResult.status === 'fulfilled') {
      const [nextHealth, runtime, status] = demoResult.value
      setHealth(nextHealth)
      setLeakStatus(status)
      setBaselineGoroutines((baseline) => status.active === 0
        ? Math.min(baseline ?? runtime.goroutines, runtime.goroutines)
        : baseline ?? runtime.goroutines)
      setSamples((current) => [...current, runtime].slice(-MAX_POINTS))
      setRuntimeError(null)
    } else {
      setHealth(null)
      setRuntimeError(demoResult.reason instanceof Error ? demoResult.reason.message : 'Unable to reach demo-service.')
    }
    setGopilotOnline(gopilotResult.status === 'fulfilled')
  }, [])

  useEffect(() => {
    let timer: number | undefined
    const startPolling = () => {
      if (timer !== undefined || document.hidden) return
      void poll()
      timer = window.setInterval(() => void poll(), 1_000)
    }
    const stopPolling = () => {
      if (timer === undefined) return
      window.clearInterval(timer)
      timer = undefined
    }
    const handleVisibility = () => document.hidden ? stopPolling() : startPolling()

    startPolling()
    document.addEventListener('visibilitychange', handleVisibility)
    return () => {
      stopPolling()
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [poll])

  const runAction = async (action: () => Promise<LeakStatus>, clearDiagnosis = false) => {
    setControlBusy(true)
    try {
      const status = await action()
      setLeakStatus(status)
      setRuntimeError(null)
      if (clearDiagnosis) {
        setDiagnosis(null)
        setDiagnosisError(null)
      }
      await poll()
    } catch (error) {
      setRuntimeError(error instanceof Error ? error.message : 'The control action failed.')
    } finally {
      setControlBusy(false)
    }
  }

  const diagnose = async () => {
    setDiagnosing(true)
    setDiagnosis(null)
    setDiagnosisError(null)
    try {
      setDiagnosis(await api.diagnose())
    } catch (error) {
      setDiagnosisError(error instanceof APIError ? error : new APIError('Diagnosis failed unexpectedly.', 'unknown_error'))
    } finally {
      setDiagnosing(false)
    }
  }

  const current = samples.at(-1)
  const currentDelta = current && baselineGoroutines !== null ? current.goroutines - baselineGoroutines : undefined
  const recovered = Boolean(diagnosis && leakStatus?.active === 0)

  return (
    <>
      <a className="skip-link" href="#main-content">Skip to runtime workspace</a>
      <main id="main-content">
      <nav className="topbar" aria-label="GoPilot workspace">
        <a className="brand" href="#top" aria-label="GoPilot dashboard home">
          <span className="brand-mark">G</span>
          <span>GoPilot</span>
          <em>Runtime Intelligence</em>
        </a>
        <div className="system-status">
          <StatusPill online={health ? true : runtimeError ? false : null} label="Demo service" />
          <StatusPill online={gopilotOnline} label="Agent backend" />
        </div>
      </nav>

      <header id="top" className="hero">
        <div className="hero-copy">
          <div className="incident-label"><span aria-hidden="true" /> Goroutine leak workspace</div>
          <h1>Trace the runtime signal to a proven cause.</h1>
          <p>Collect live Go evidence, inspect every Agent tool call, and keep the conclusion tied to the observations that support it.</p>
        </div>
        <ol className="workflow-rail" aria-label="Diagnostic workflow">
          <li className={health ? 'is-active' : ''}>
            <span>Runtime signal</span>
            <strong>{leakStatus?.active ? `${leakStatus.active} intentional leaks` : health ? 'Baseline sampling' : 'Waiting for service'}</strong>
          </li>
          <li className={diagnosing || diagnosis ? 'is-active' : ''}>
            <span>Agent investigation</span>
            <strong>{diagnosing ? 'Provider request active' : diagnosis ? `${diagnosis.investigation.length} ledger steps` : 'Ready on demand'}</strong>
          </li>
          <li className={diagnosis ? 'is-active' : ''}>
            <span>Grounded diagnosis</span>
            <strong>{diagnosis ? `${diagnosis.evidence.length} observations linked` : 'Awaiting evidence'}</strong>
          </li>
        </ol>
      </header>

      {runtimeError && <div className="global-alert" role="alert"><span>Runtime feed interrupted</span>{runtimeError}</div>}

      <section className="dashboard-section" id="runtime">
        <SectionHeading
          number="01"
           eyebrow="Live runtime"
           title="Watch the process"
          description="Direct observations from the demo service, sampled every second."
          action={(
            <div className="control-actions">
              <button className="button button-danger" disabled={controlBusy || !health} onClick={() => void runAction(() => api.inject(LEAK_BATCH), true)}>
                {controlBusy ? 'Working…' : `Inject ${LEAK_BATCH} leaks`}
              </button>
              <button className="button button-secondary" disabled={controlBusy || !health || leakStatus?.active === 0} onClick={() => void runAction(api.reset)}>
                Reset leaks
              </button>
            </div>
          )}
        />

        <div className="metric-grid">
          <article className="metric-card">
            <div className="metric-label">Service health</div>
            <div className="metric-health"><span className={`status-dot ${health ? 'online' : ''}`} aria-hidden="true" />{health ? 'Healthy' : 'Offline'}</div>
            <div className="metric-note">{health?.service ?? 'Awaiting connection'}</div>
          </article>
          <article className="metric-card metric-primary">
            <div className="metric-label">Goroutines</div>
            <div className="metric-value" aria-live="polite">{current?.goroutines ?? '—'}</div>
            <div className={`metric-note ${currentDelta && currentDelta > 0 ? 'text-warning' : ''}`}>{currentDelta === undefined ? 'Establishing baseline' : `${formatDelta(currentDelta)} from observed baseline`}</div>
          </article>
          <article className="metric-card">
            <div className="metric-label">Observed baseline</div>
            <div className="metric-value">{baselineGoroutines ?? '—'}</div>
            <div className="metric-note">Lowest healthy sample</div>
          </article>
          <article className="metric-card">
            <div className="metric-label">Intentional leaks</div>
            <div className={`metric-value ${leakStatus?.active ? 'text-warning' : ''}`}>{leakStatus?.active ?? '—'}</div>
            <div className="metric-note">Bounded at {leakStatus?.max_active ?? '—'} active</div>
          </article>
          <article className="metric-card">
            <div className="metric-label">Process uptime</div>
            <div className="metric-value metric-compact">{formatUptime(current?.uptime_ms)}</div>
            <div className="metric-note">{current?.go_version ?? 'Go runtime'}</div>
          </article>
        </div>

        <article className="chart-panel">
          <div className="chart-heading">
            <div>
              <div className="panel-label">Goroutine count</div>
              <strong>Last {Math.min(samples.length, MAX_POINTS)} samples</strong>
            </div>
            <div className="chart-legend"><span aria-hidden="true" /> Live · 1s interval</div>
          </div>
          <TrendChart samples={samples} />
          <div className="chart-axis"><span>{samples[0] ? formatTime(samples[0].timestamp) : '60 seconds ago'}</span><span>Now</span></div>
        </article>
      </section>

      <section className="dashboard-section investigation-section" id="investigation">
        <SectionHeading
          number="02"
          eyebrow="AI investigation"
          title="Evidence before inference"
          description="DeepSeek chooses diagnostic tools; GoPilot executes them and owns the investigation ledger."
          action={(
            <button className="button button-primary" disabled={diagnosing || gopilotOnline !== true} onClick={() => void diagnose()}>
              {diagnosing ? <><span className="spinner" aria-hidden="true" /> Investigating…</> : <>Diagnose service</>}
            </button>
          )}
        />

        {diagnosing && (
          <div className="investigating-state" role="status" aria-live="polite">
            <div className="pending-mark" aria-hidden="true"><span /></div>
            <div>
              <strong>Agent investigation in progress</strong>
              <p>The request is non-streaming. Tool steps will appear only when the server returns its verified investigation ledger.</p>
            </div>
          </div>
        )}
        {!diagnosing && diagnosisError && <ErrorNotice error={diagnosisError} />}
        {!diagnosing && !diagnosis && !diagnosisError && (
          <div className="empty-state investigation-empty">
            <div className="empty-index" aria-hidden="true">02</div>
            <div><strong>No investigation yet</strong><p>Inject the controlled leak, let the trend stabilize, then ask GoPilot to diagnose it.</p></div>
          </div>
        )}
        {diagnosis && (
          <div className="investigation-result">
            <div className="result-heading">
              <div><span>Investigation ledger</span><code>{diagnosis.investigation_id}</code></div>
              <div>{diagnosis.investigation.length} tool calls · completed {formatTime(diagnosis.generated_at)}</div>
            </div>
            <Timeline steps={diagnosis.investigation} />
          </div>
        )}
      </section>

      <section className="dashboard-section diagnosis-section" id="diagnosis">
        <SectionHeading
          number="03"
          eyebrow="Root cause & evidence"
          title="Grounded diagnosis"
          description="Model-authored conclusions paired with server-materialized observations."
        />

        {!diagnosis && (
          <div className="empty-state diagnosis-empty">
            <div className="empty-index" aria-hidden="true">03</div>
            <div><strong>Awaiting grounded evidence</strong><p>The diagnosis, recommendation, confidence estimate, and source observations will appear here.</p></div>
          </div>
        )}

        {diagnosis && (
          <>
            {recovered && <div className="recovery-banner" role="status"><span aria-hidden="true">✓</span><div><strong>Recovery confirmed</strong><p>Intentional leak count is zero. The completed diagnosis remains available for review.</p></div></div>}
            <div className="diagnosis-grid">
              <article className="diagnosis-main">
                <div className="diagnosis-kicker"><span>Grounded conclusion</span><code>{diagnosis.id}</code></div>
                <h3>{diagnosis.summary}</h3>
                <div className="diagnosis-block">
                  <span>Root cause</span>
                  <p>{diagnosis.root_cause}</p>
                </div>
                <div className="component-row">
                  <span>Affected component</span>
                  <code>{diagnosis.affected_component}</code>
                </div>
              </article>
              <aside className="confidence-card" aria-label={`Model confidence estimate: ${Math.round(diagnosis.confidence * 100)} percent`}>
                <span className="confidence-label">Model confidence estimate</span>
                <strong>{Math.round(diagnosis.confidence * 100)}%</strong>
                <div className="confidence-track" aria-hidden="true"><span style={{ width: `${Math.round(diagnosis.confidence * 100)}%` }} /></div>
                <p>Grounded in {diagnosis.evidence.length} verified observation{diagnosis.evidence.length === 1 ? '' : 's'}.</p>
              </aside>
            </div>

            <article className="recommendation-card">
              <div><span>Recommended action</span><p>{diagnosis.recommendation}</p></div>
            </article>

            <div className="evidence-heading">
              <div><span>Evidence bundle</span><strong>{diagnosis.evidence.length} observations</strong></div>
              <p>Expand each card to inspect the exact runtime data returned to the model.</p>
            </div>
            {diagnosis.evidence.length ? (
              <div className="evidence-list">
                {diagnosis.evidence.map((evidence, index) => <EvidenceCard evidence={evidence} index={index} key={evidence.observation_id} />)}
              </div>
            ) : (
              <div className="error-notice"><div className="error-icon">!</div><div><strong>Missing evidence</strong><p>The backend returned a diagnosis without materialized evidence.</p></div></div>
            )}
          </>
        )}
      </section>

      <footer>
        <div><span className="brand-mark small">G</span><strong>GoPilot</strong> · Evidence-grounded Go diagnostics</div>
        <div>runtime/metrics · net/http/pprof · server-owned ledger</div>
      </footer>
      </main>
    </>
  )
}

export default App
