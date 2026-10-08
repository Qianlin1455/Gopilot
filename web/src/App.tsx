import { useCallback, useEffect, useRef, useState } from 'react'
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
import { createBaselineState, observeBaseline } from './baseline'
import { stepStatusLabel, toolDisplayName, zhCN } from './locales/zh-CN'
import {
  isLiveDiagnosisRecovered,
  loadReplayArtifact,
  ReplayError,
  selectDiagnosisForMode,
  type DiagnosisViewMode,
  type ReplayArtifact,
} from './replay'

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
  if (seconds < 60) return zhCN.format.seconds(seconds)
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return zhCN.format.minutes(minutes, seconds % 60)
  return zhCN.format.hours(Math.floor(minutes / 60), minutes % 60)
}

function formatTime(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(value))
}

function formatDateTime(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(value))
}

function formatNumber(value?: number) {
  return value === undefined ? '—' : value.toLocaleString('zh-CN')
}

function formatDelta(value?: number) {
  if (value === undefined) return '—'
  return `${value > 0 ? '+' : ''}${value.toLocaleString('zh-CN')}`
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
      {!samples.length && <div className="chart-empty">{zhCN.chart.empty}</div>}
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={zhCN.accessibility.recentGoroutines}>
        <title>{zhCN.accessibility.recentGoroutines}</title>
        <desc>{samples.length ? zhCN.chart.description(minimum, maximum, samples.length) : zhCN.chart.noSamples}</desc>
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
  const state = online === null ? zhCN.status.checking : online ? zhCN.status.online : zhCN.status.offline
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
    return <div className="empty-state compact-empty">{zhCN.investigation.noToolCalls}</div>
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
                <strong>{toolDisplayName(step.tool)}</strong>
                <code className="tool-id">{step.tool}</code>
              </div>
              <span className={`status-tag ${step.status}`}>{stepStatusLabel(step.status)}</span>
            </div>
            <div className="timeline-meta">
              <span>{zhCN.investigation.modelStep(step.agent_step)}</span>
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
      <div><span>{zhCN.evidence.health}</span><strong className="text-success">{data.health_status ?? '—'}</strong></div>
      <div><span>{zhCN.evidence.http}</span><strong>{formatNumber(data.http_status)}</strong></div>
      <div><span>{zhCN.evidence.latency}</span><strong>{formatNumber(data.request_latency_ms)} ms</strong></div>
      <div><span>{zhCN.evidence.goroutines}</span><strong>{formatNumber(data.current_goroutine_count)}</strong></div>
      <div><span>{zhCN.evidence.activeLeaks}</span><strong className="text-warning">{formatNumber(data.active_intentional_leak_count)}</strong></div>
      <div><span>{zhCN.evidence.uptime}</span><strong>{formatUptime(data.uptime_ms)}</strong></div>
    </div>
  )
}

function RuntimeEvidenceView({ data }: { data: RuntimeMetricsEvidence }) {
  const samples = data.samples ?? []
  return (
    <div className="evidence-runtime">
      <div className="evidence-metrics evidence-metrics-four">
        <div><span>{zhCN.evidence.start}</span><strong>{formatNumber(data.goroutines?.start)}</strong></div>
        <div><span>{zhCN.evidence.end}</span><strong>{formatNumber(data.goroutines?.end)}</strong></div>
        <div><span>{zhCN.evidence.delta}</span><strong className="text-warning">{formatDelta(data.goroutines?.absolute_delta)}</strong></div>
        <div><span>{zhCN.evidence.rate}</span><strong>{formatNumber(data.goroutines?.growth_rate_per_second)}/s</strong></div>
      </div>
      <TrendChart samples={samples} compact />
      <div className="evidence-footnote">
        {zhCN.evidence.samplesObserved(data.sample_count ?? samples.length, formatNumber(data.capture_window?.actual_seconds))}
        {data.insufficient_history && <span className="history-note">{zhCN.evidence.partialWindow(data.insufficient_history_reason)}</span>}
      </div>
    </div>
  )
}

function ProfileEvidenceView({ data }: { data: ProfileEvidence }) {
  const groups = data.stack_groups ?? []
  return (
    <div className="profile-evidence">
      <div className="profile-summary">
        <strong>{formatNumber(data.total_goroutine_count)}</strong>
        <span>{zhCN.evidence.profileSummary(groups.length)}</span>
      </div>
      <div className="stack-list">
        {groups.map((group, index) => (
          <div className={`stack-group ${group.contains_intentional_leak ? 'is-leak' : ''}`} key={group.signature ?? index}>
            <div className="stack-group-head">
              <div>
                <strong>{formatNumber(group.count)}×</strong>
                <span>{group.state ?? zhCN.evidence.unknownState}</span>
              </div>
              {group.contains_intentional_leak && <span className="leak-badge">{zhCN.evidence.leakSignature}</span>}
            </div>
            <pre>{(group.frames ?? []).join('\n')}</pre>
          </div>
        ))}
      </div>
      {(data.truncation?.groups_truncated || data.truncation?.input_truncated) && (
        <div className="history-note">{zhCN.evidence.boundedProfile}</div>
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
          <strong>{toolDisplayName(evidence.source_tool)}</strong>
          <code className="tool-id">{evidence.source_tool}</code>
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
    ? zhCN.errors.agentNotConfigured
    : error.code === 'replay_load_failed' || error.code === 'invalid_replay' || error.code === 'invalid_replay_json'
      ? zhCN.errors.replayFailed
    : error.code === 'request_timeout'
      ? zhCN.errors.diagnosisTimedOut
      : error.code === 'backend_unavailable'
        ? zhCN.errors.backendUnavailable
        : error.status >= 500
          ? zhCN.errors.providerFailed
          : zhCN.errors.investigationFailed
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

function ReplayBanner({ replay }: { replay: ReplayArtifact }) {
  return (
    <div className="replay-banner" role="status" aria-atomic="true">
      <div>
        <span className="replay-label">{zhCN.replay.badge}</span>
        <strong>{replay.case_name}</strong>
        <p>{zhCN.replay.isolationNotice}</p>
      </div>
      <dl>
        <div><dt>{zhCN.replay.recordedAt}</dt><dd>{formatDateTime(replay.recorded_at)}</dd></div>
        <div><dt>{zhCN.replay.provider}</dt><dd>{replay.provider.name} / {replay.provider.model}</dd></div>
      </dl>
    </div>
  )
}

function App() {
  const [health, setHealth] = useState<Health | null>(null)
  const [gopilotOnline, setGopilotOnline] = useState<boolean | null>(null)
  const [leakStatus, setLeakStatus] = useState<LeakStatus | null>(null)
  const [samples, setSamples] = useState<RuntimeSnapshot[]>([])
  const [baseline, setBaseline] = useState(createBaselineState)
  const serviceStartedAt = useRef<string | null>(null)
  const [runtimeError, setRuntimeError] = useState<string | null>(null)
  const [controlBusy, setControlBusy] = useState(false)
  const [diagnosing, setDiagnosing] = useState(false)
  const [liveDiagnosis, setLiveDiagnosis] = useState<Diagnosis | null>(null)
  const [liveDiagnosisError, setLiveDiagnosisError] = useState<APIError | null>(null)
  const [viewMode, setViewMode] = useState<DiagnosisViewMode>('live')
  const [replay, setReplay] = useState<ReplayArtifact | null>(null)
  const [replayLoading, setReplayLoading] = useState(false)
  const [replayError, setReplayError] = useState<APIError | null>(null)

  const poll = useCallback(async () => {
    const [demoResult, gopilotResult] = await Promise.allSettled([
      Promise.all([api.demoHealth(), api.runtime(), api.leakStatus()]),
      api.gopilotHealth(),
    ])

    if (demoResult.status === 'fulfilled') {
      const [nextHealth, runtime, status] = demoResult.value
      const nextServiceStartedAt = nextHealth.started_at ?? null
      const serviceRestarted = Boolean(
        serviceStartedAt.current
        && nextServiceStartedAt
        && serviceStartedAt.current !== nextServiceStartedAt,
      )
      serviceStartedAt.current = nextServiceStartedAt
      setHealth(nextHealth)
      setLeakStatus(status)
      setBaseline((current) => observeBaseline(current, {
        goroutines: runtime.goroutines,
        activeLeaks: status.active,
        serviceStartedAt: nextServiceStartedAt ?? undefined,
      }))
      setSamples((current) => serviceRestarted ? [runtime] : [...current, runtime].slice(-MAX_POINTS))
      setRuntimeError(null)
    } else {
      setHealth(null)
      setRuntimeError(demoResult.reason instanceof Error ? demoResult.reason.message : zhCN.errors.demoUnavailable)
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
        setLiveDiagnosis(null)
        setLiveDiagnosisError(null)
      }
      await poll()
    } catch (error) {
      setRuntimeError(error instanceof Error ? error.message : zhCN.errors.controlFailed)
    } finally {
      setControlBusy(false)
    }
  }

  const diagnose = async () => {
    setViewMode('live')
    setDiagnosing(true)
    setLiveDiagnosis(null)
    setLiveDiagnosisError(null)
    try {
      setLiveDiagnosis(await api.diagnose())
    } catch (error) {
      setLiveDiagnosisError(error instanceof APIError ? error : new APIError(zhCN.errors.diagnosisUnexpected, 'unknown_error'))
    } finally {
      setDiagnosing(false)
    }
  }

  const showReplay = async () => {
    setViewMode('historical')
    setReplayError(null)
    if (replay) return
    setReplayLoading(true)
    try {
      setReplay(await loadReplayArtifact())
    } catch (error) {
      const replayFailure = error instanceof ReplayError
        ? new APIError(error.message, error.code)
        : new APIError(zhCN.errors.replayUnexpected, 'replay_load_failed')
      setReplayError(replayFailure)
    } finally {
      setReplayLoading(false)
    }
  }

  const current = samples.at(-1)
  const baselineGoroutines = baseline.value
  const currentDelta = current && baselineGoroutines !== null ? current.goroutines - baselineGoroutines : undefined
  const diagnosis = selectDiagnosisForMode(viewMode, liveDiagnosis, replay)
  const activeDiagnosisError = viewMode === 'live' ? liveDiagnosisError : replayError
  const recovered = isLiveDiagnosisRecovered(viewMode, liveDiagnosis, leakStatus?.active)

  return (
    <>
      <a className="skip-link" href="#main-content">{zhCN.accessibility.skipToWorkspace}</a>
      <main id="main-content">
      <nav className="topbar" aria-label={zhCN.accessibility.workspace}>
        <a className="brand" href="#top" aria-label={zhCN.accessibility.dashboardHome}>
          <span className="brand-mark">G</span>
          <span>GoPilot</span>
          <em>{zhCN.brand.descriptor}</em>
        </a>
        <div className="system-status">
          <StatusPill online={health ? true : runtimeError ? false : null} label={zhCN.services.demo} />
          <StatusPill online={gopilotOnline} label={zhCN.services.agent} />
        </div>
      </nav>

      <header id="top" className="hero">
        <div className="hero-copy">
          <div className="incident-label"><span aria-hidden="true" /> {zhCN.hero.label}</div>
          <h1>{zhCN.hero.title}</h1>
          <p>{zhCN.hero.description}</p>
        </div>
        <ol className="workflow-rail" aria-label={zhCN.accessibility.workflow}>
          <li className={health ? 'is-active' : ''}>
            <span>{zhCN.workflow.runtime}</span>
            <strong>{leakStatus?.active ? zhCN.workflow.intentionalLeaks(leakStatus.active) : health ? zhCN.workflow.samplingBaseline : zhCN.workflow.waitingForService}</strong>
          </li>
          <li className={diagnosing || diagnosis ? 'is-active' : ''}>
            <span>{zhCN.workflow.investigation}</span>
            <strong>{diagnosing
              ? zhCN.workflow.providerActive
              : diagnosis
                ? viewMode === 'historical' ? zhCN.workflow.replaySteps(diagnosis.investigation.length) : zhCN.workflow.ledgerSteps(diagnosis.investigation.length)
                : zhCN.workflow.ready}</strong>
          </li>
          <li className={diagnosis ? 'is-active' : ''}>
            <span>{zhCN.workflow.diagnosis}</span>
            <strong>{diagnosis
              ? viewMode === 'historical' ? zhCN.workflow.replayObservations(diagnosis.evidence.length) : zhCN.workflow.observationsLinked(diagnosis.evidence.length)
              : zhCN.workflow.awaitingEvidence}</strong>
          </li>
        </ol>
      </header>

      {runtimeError && <div className="global-alert" role="alert"><span>{zhCN.runtime.feedInterrupted}</span>{runtimeError}</div>}

      <section className="dashboard-section" id="runtime">
        <SectionHeading
          number="01"
          eyebrow={zhCN.runtime.eyebrow}
          title={zhCN.runtime.title}
          description={zhCN.runtime.description}
          action={(
            <div className="control-actions">
              <button className="button button-danger" disabled={controlBusy || !health} onClick={() => void runAction(() => api.inject(LEAK_BATCH), true)}>
                {controlBusy ? zhCN.runtime.working : zhCN.runtime.inject(LEAK_BATCH)}
              </button>
              <button className="button button-secondary" disabled={controlBusy || !health || leakStatus?.active === 0} onClick={() => void runAction(api.reset)}>
                {zhCN.runtime.reset}
              </button>
            </div>
          )}
        />

        <div className="metric-grid">
          <article className="metric-card">
            <div className="metric-label">{zhCN.runtime.serviceHealth}</div>
            <div className="metric-health"><span className={`status-dot ${health ? 'online' : ''}`} aria-hidden="true" />{health ? zhCN.runtime.online : zhCN.runtime.unavailable}</div>
            <div className="metric-note">{health ? zhCN.runtime.healthCheckPassed(health.service) : zhCN.runtime.awaitingConnection}</div>
          </article>
          <article className="metric-card metric-primary">
            <div className="metric-label">{zhCN.runtime.goroutineCount}</div>
            <div className="metric-value" aria-live="polite">{formatNumber(current?.goroutines)}</div>
            <div className={`metric-note ${currentDelta && currentDelta > 0 ? 'text-warning' : ''}`}>{currentDelta === undefined ? zhCN.runtime.baselineNotEstablished : zhCN.runtime.fromBaseline(formatDelta(currentDelta))}</div>
          </article>
          <article className="metric-card">
            <div className="metric-label">{zhCN.runtime.observedBaseline}</div>
            <div className="metric-value">{formatNumber(baselineGoroutines ?? undefined)}</div>
            <div className="metric-note">{baselineGoroutines === null ? zhCN.runtime.baselineNotEstablished : zhCN.runtime.lowestHealthySample}</div>
          </article>
          <article className="metric-card">
            <div className="metric-label">{zhCN.runtime.activeLeaks}</div>
            <div className={`metric-value ${leakStatus?.active ? 'text-warning' : ''}`}>{formatNumber(leakStatus?.active)}</div>
            <div className="metric-note">{zhCN.runtime.boundedAt(formatNumber(leakStatus?.max_active))}</div>
          </article>
          <article className="metric-card">
            <div className="metric-label">{zhCN.runtime.processUptime}</div>
            <div className="metric-value metric-compact">{formatUptime(current?.uptime_ms)}</div>
            <div className="metric-note">{current?.go_version ?? zhCN.runtime.goRuntime}</div>
          </article>
        </div>

        <article className="chart-panel">
          <div className="chart-heading">
            <div>
              <div className="panel-label">{zhCN.runtime.goroutineCount}</div>
              <strong>{zhCN.runtime.recentSamples(Math.min(samples.length, MAX_POINTS))}</strong>
            </div>
            <div className="chart-legend"><span aria-hidden="true" /> {zhCN.runtime.liveInterval}</div>
          </div>
          <TrendChart samples={samples} />
          <div className="chart-axis"><span>{samples[0] ? formatTime(samples[0].timestamp) : zhCN.runtime.secondsAgo(60)}</span><span>{zhCN.runtime.now}</span></div>
        </article>
      </section>

      <section className="dashboard-section investigation-section" id="investigation">
        <SectionHeading
          number="02"
          eyebrow={zhCN.investigation.eyebrow}
          title={zhCN.investigation.title}
          description={zhCN.investigation.description}
          action={(
            viewMode === 'live'
              ? <button className="button button-primary" disabled={diagnosing || gopilotOnline !== true} onClick={() => void diagnose()}>
                  {diagnosing ? <><span className="spinner" aria-hidden="true" /> {zhCN.investigation.diagnosing}</> : <>{zhCN.investigation.diagnose}</>}
                </button>
              : <span className="mode-indicator">{zhCN.replay.badge}</span>
          )}
        />

        <div className="diagnosis-mode-switch" role="group" aria-label={zhCN.replay.modeSelector}>
          <button
            className={`mode-option ${viewMode === 'live' ? 'is-active' : ''}`}
            type="button"
            aria-pressed={viewMode === 'live'}
            disabled={diagnosing}
            onClick={() => setViewMode('live')}
          >
            <strong>{zhCN.replay.liveTitle}</strong>
            <span>{zhCN.replay.liveDescription}</span>
          </button>
          <button
            className={`mode-option ${viewMode === 'historical' ? 'is-active' : ''}`}
            type="button"
            aria-pressed={viewMode === 'historical'}
            disabled={diagnosing || replayLoading}
            onClick={() => void showReplay()}
          >
            <strong>{replayLoading ? zhCN.replay.loading : zhCN.replay.historyTitle}</strong>
            <span>{zhCN.replay.historyDescription}</span>
          </button>
        </div>

        {diagnosing && (
          <div className="investigating-state" role="status" aria-live="polite">
            <div className="pending-mark" aria-hidden="true"><span /></div>
            <div>
              <strong>{zhCN.investigation.inProgress}</strong>
              <p>{zhCN.investigation.nonStreamingNotice}</p>
            </div>
          </div>
        )}
        {viewMode === 'historical' && replayLoading && (
          <div className="investigating-state" role="status" aria-live="polite">
            <div className="pending-mark" aria-hidden="true"><span /></div>
            <div><strong>{zhCN.replay.loading}</strong><p>{zhCN.replay.loadingDescription}</p></div>
          </div>
        )}
        {!diagnosing && !replayLoading && activeDiagnosisError && <ErrorNotice error={activeDiagnosisError} />}
        {!diagnosing && !replayLoading && !diagnosis && !activeDiagnosisError && (
          <div className="empty-state investigation-empty">
            <div className="empty-index" aria-hidden="true">02</div>
            <div>
              <strong>{viewMode === 'historical' ? zhCN.replay.emptyTitle : zhCN.investigation.emptyTitle}</strong>
              <p>{viewMode === 'historical' ? zhCN.replay.emptyDescription : zhCN.investigation.emptyDescription}</p>
            </div>
          </div>
        )}
        {viewMode === 'historical' && replay && <ReplayBanner replay={replay} />}
        {!diagnosing && !replayLoading && diagnosis && (
          <div className="investigation-result">
            <div className="result-heading">
              <div><span>{viewMode === 'historical' ? zhCN.replay.historicalLedger : zhCN.investigation.ledger}</span><code>{diagnosis.investigation_id}</code></div>
              <div>{zhCN.investigation.completed(diagnosis.investigation.length, formatTime(diagnosis.generated_at))}</div>
            </div>
            <Timeline steps={diagnosis.investigation} />
          </div>
        )}
      </section>

      <section className="dashboard-section diagnosis-section" id="diagnosis">
        <SectionHeading
          number="03"
          eyebrow={zhCN.diagnosis.eyebrow}
          title={zhCN.diagnosis.title}
          description={zhCN.diagnosis.description}
        />

        {!diagnosis && (
          <div className="empty-state diagnosis-empty">
            <div className="empty-index" aria-hidden="true">03</div>
            <div><strong>{zhCN.diagnosis.emptyTitle}</strong><p>{zhCN.diagnosis.emptyDescription}</p></div>
          </div>
        )}

        {diagnosis && (
          <>
            {recovered && <div className="recovery-banner" role="status"><span aria-hidden="true">✓</span><div><strong>{zhCN.diagnosis.recoveryConfirmed}</strong><p>{zhCN.diagnosis.recoveryDescription}</p></div></div>}
            <div className="diagnosis-grid">
              <article className="diagnosis-main">
                <div className="diagnosis-kicker"><span>{viewMode === 'historical' ? zhCN.replay.historicalConclusion : zhCN.diagnosis.conclusion}</span><code>{diagnosis.id}</code></div>
                <h3>{diagnosis.summary}</h3>
                <div className="diagnosis-block">
                  <span>{zhCN.diagnosis.rootCause}</span>
                  <p>{diagnosis.root_cause}</p>
                </div>
                <div className="component-row">
                  <span>{zhCN.diagnosis.affectedComponent}</span>
                  <code>{diagnosis.affected_component}</code>
                </div>
              </article>
              <aside className="confidence-card" aria-label={zhCN.accessibility.confidence(Math.round(diagnosis.confidence * 100))}>
                <span className="confidence-label">{zhCN.diagnosis.confidence}</span>
                <strong>{formatNumber(Math.round(diagnosis.confidence * 100))}%</strong>
                <div className="confidence-track" aria-hidden="true"><span style={{ width: `${Math.round(diagnosis.confidence * 100)}%` }} /></div>
                <p>{zhCN.diagnosis.groundedIn(diagnosis.evidence.length)}</p>
              </aside>
            </div>

            <article className="recommendation-card">
              <div><span>{zhCN.diagnosis.recommendation}</span><p>{diagnosis.recommendation}</p></div>
            </article>

            <div className="evidence-heading">
              <div><span>{zhCN.diagnosis.evidenceBundle}</span><strong>{zhCN.diagnosis.observations(diagnosis.evidence.length)}</strong></div>
              <p>{viewMode === 'historical' ? zhCN.replay.evidenceHint : zhCN.diagnosis.evidenceHint}</p>
            </div>
            {diagnosis.evidence.length ? (
              <div className="evidence-list">
                {diagnosis.evidence.map((evidence, index) => <EvidenceCard evidence={evidence} index={index} key={evidence.observation_id} />)}
              </div>
            ) : (
              <div className="error-notice"><div className="error-icon">!</div><div><strong>{zhCN.diagnosis.missingEvidence}</strong><p>{zhCN.diagnosis.missingEvidenceDescription}</p></div></div>
            )}
          </>
        )}
      </section>

      <footer>
        <div><span className="brand-mark small">G</span><strong>GoPilot</strong> · {zhCN.footer.description}</div>
        <div>{zhCN.footer.sources}</div>
      </footer>
      </main>
    </>
  )
}

export default App
