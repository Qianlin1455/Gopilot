import type { Diagnosis, Evidence, InvestigationStep } from './api'

export const DEFAULT_REPLAY_URL = '/replays/goroutine-leak.json'

export type DiagnosisViewMode = 'live' | 'historical'

export type ReplayArtifact = {
  schema_version: 1
  mode: 'historical_replay'
  case_id: string
  case_name: string
  recorded_at: string
  provider: {
    name: string
    model: string
    protocol: string
  }
  provenance: {
    source: 'real_provider_run'
    scenario: string
    verification: string
    redactions: string[]
  }
  diagnosis: Diagnosis
}

type UnknownRecord = Record<string, unknown>

export class ReplayError extends Error {
  readonly code: string

  constructor(message: string, code: string) {
    super(message)
    this.name = 'ReplayError'
    this.code = code
  }
}

function fail(path: string, message: string, code = 'invalid_replay') : never {
  throw new ReplayError(`${path}: ${message}`, code)
}

function record(value: unknown, path: string): UnknownRecord {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) fail(path, '必须是对象')
  return value as UnknownRecord
}

function string(value: unknown, path: string): string {
  if (typeof value !== 'string' || !value.trim()) fail(path, '必须是非空字符串')
  return value
}

function timestamp(value: unknown, path: string): string {
  const result = string(value, path)
  if (Number.isNaN(Date.parse(result))) fail(path, '必须是有效时间戳')
  return result
}

function number(value: unknown, path: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) fail(path, '必须是有限数值')
  return value
}

function validateEvidence(value: unknown, index: number): Evidence {
  const path = `diagnosis.evidence[${index}]`
  const item = record(value, path)
  const observationID = string(item.observation_id, `${path}.observation_id`)
  if (!/^obs_[A-Za-z0-9_-]+$/.test(observationID)) fail(`${path}.observation_id`, '格式无效')
  string(item.source_tool, `${path}.source_tool`)
  timestamp(item.captured_at, `${path}.captured_at`)
  string(item.description, `${path}.description`)
  if (!Object.hasOwn(item, 'data')) fail(`${path}.data`, '字段缺失')
  return item as Evidence
}

function validateStep(value: unknown, index: number): InvestigationStep {
  const path = `diagnosis.investigation[${index}]`
  const item = record(value, path)
  const sequence = number(item.sequence, `${path}.sequence`)
  if (!Number.isInteger(sequence) || sequence !== index + 1) fail(`${path}.sequence`, '必须按 1 开始连续递增')
  const agentStep = number(item.agent_step, `${path}.agent_step`)
  if (!Number.isInteger(agentStep) || agentStep < 1) fail(`${path}.agent_step`, '必须是正整数')
  string(item.tool_call_id, `${path}.tool_call_id`)
  string(item.tool, `${path}.tool`)
  string(item.status, `${path}.status`)
  timestamp(item.started_at, `${path}.started_at`)
  timestamp(item.completed_at, `${path}.completed_at`)
  if (item.arguments !== undefined) record(item.arguments, `${path}.arguments`)
  if (item.observation_id !== undefined) string(item.observation_id, `${path}.observation_id`)
  if (item.error !== undefined && typeof item.error !== 'string') fail(`${path}.error`, '必须是字符串')
  return item as InvestigationStep
}

function validateDiagnosis(value: unknown): Diagnosis {
  const diagnosis = record(value, 'diagnosis')
  string(diagnosis.id, 'diagnosis.id')
  string(diagnosis.investigation_id, 'diagnosis.investigation_id')
  timestamp(diagnosis.generated_at, 'diagnosis.generated_at')
  string(diagnosis.summary, 'diagnosis.summary')
  string(diagnosis.root_cause, 'diagnosis.root_cause')
  string(diagnosis.affected_component, 'diagnosis.affected_component')
  string(diagnosis.recommendation, 'diagnosis.recommendation')
  const confidence = number(diagnosis.confidence, 'diagnosis.confidence')
  if (confidence < 0 || confidence > 1) fail('diagnosis.confidence', '必须位于 0 到 1 之间')
  if (!Array.isArray(diagnosis.evidence) || diagnosis.evidence.length === 0) fail('diagnosis.evidence', '至少需要一条证据')
  if (!Array.isArray(diagnosis.investigation) || diagnosis.investigation.length === 0) fail('diagnosis.investigation', '至少需要一个调查步骤')

  const evidence = diagnosis.evidence.map(validateEvidence)
  const steps = diagnosis.investigation.map(validateStep)
  const evidenceByID = new Map<string, Evidence>()
  for (const item of evidence) {
    if (evidenceByID.has(item.observation_id)) fail('diagnosis.evidence', `重复 observation_id：${item.observation_id}`)
    evidenceByID.set(item.observation_id, item)
  }

  const observedByTimeline = new Set<string>()
  for (const step of steps) {
    if (!step.observation_id) continue
    if (step.status !== 'success') fail('diagnosis.investigation', `${step.observation_id} 不能来自失败步骤`)
    const linkedEvidence = evidenceByID.get(step.observation_id)
    if (!linkedEvidence) fail('diagnosis.investigation', `引用未知 observation_id：${step.observation_id}`)
    if (linkedEvidence.source_tool !== step.tool) fail('diagnosis.investigation', `${step.observation_id} 的 Tool 与 Evidence 不一致`)
    observedByTimeline.add(step.observation_id)
  }
  for (const observationID of evidenceByID.keys()) {
    if (!observedByTimeline.has(observationID)) fail('diagnosis.evidence', `${observationID} 未出现在成功时间线步骤中`)
  }

  const submit = steps.at(-1)
  if (!submit || submit.tool !== 'submit_diagnosis' || submit.status !== 'success') {
    fail('diagnosis.investigation', '最后一步必须是成功的 submit_diagnosis')
  }
  const submitArguments = record(submit.arguments, 'diagnosis.investigation.submit_diagnosis.arguments')
  if (!Array.isArray(submitArguments.evidence_refs) || submitArguments.evidence_refs.length === 0) {
    fail('diagnosis.investigation.submit_diagnosis.arguments.evidence_refs', '至少需要一个证据引用')
  }
  const submittedRefs = submitArguments.evidence_refs.map((item, index) => string(item, `evidence_refs[${index}]`))
  const submittedSet = new Set(submittedRefs)
  if (submittedSet.size !== submittedRefs.length) fail('evidence_refs', '存在重复引用')
  if (submittedSet.size !== evidenceByID.size || [...submittedSet].some((id) => !evidenceByID.has(id))) {
    fail('evidence_refs', '必须与物化 Evidence 的 observation_id 完全一致')
  }

  return diagnosis as Diagnosis
}

export function validateReplayArtifact(value: unknown): ReplayArtifact {
  const artifact = record(value, 'replay')
  if (artifact.schema_version !== 1) fail('schema_version', '仅支持版本 1')
  if (artifact.mode !== 'historical_replay') fail('mode', '必须是 historical_replay')
  string(artifact.case_id, 'case_id')
  string(artifact.case_name, 'case_name')
  timestamp(artifact.recorded_at, 'recorded_at')

  const provider = record(artifact.provider, 'provider')
  string(provider.name, 'provider.name')
  string(provider.model, 'provider.model')
  string(provider.protocol, 'provider.protocol')

  const provenance = record(artifact.provenance, 'provenance')
  if (provenance.source !== 'real_provider_run') fail('provenance.source', '必须标记为 real_provider_run')
  string(provenance.scenario, 'provenance.scenario')
  string(provenance.verification, 'provenance.verification')
  if (!Array.isArray(provenance.redactions) || provenance.redactions.some((item) => typeof item !== 'string')) {
    fail('provenance.redactions', '必须是字符串数组')
  }

  const diagnosis = validateDiagnosis(artifact.diagnosis)
  if (artifact.recorded_at !== diagnosis.generated_at) fail('recorded_at', '必须与 diagnosis.generated_at 一致')
  return { ...artifact, diagnosis } as ReplayArtifact
}

export function parseReplayJSON(json: string): ReplayArtifact {
  let value: unknown
  try {
    value = JSON.parse(json)
  } catch {
    throw new ReplayError('历史诊断文件不是有效 JSON', 'invalid_replay_json')
  }
  return validateReplayArtifact(value)
}

export async function loadReplayArtifact(
  fetcher: typeof fetch = fetch,
  url = DEFAULT_REPLAY_URL,
): Promise<ReplayArtifact> {
  let response: Response
  try {
    response = await fetcher(url, { cache: 'no-store' })
  } catch {
    throw new ReplayError('无法读取历史诊断文件', 'replay_load_failed')
  }
  if (!response.ok) throw new ReplayError(`历史诊断文件请求失败：HTTP ${response.status}`, 'replay_load_failed')
  return parseReplayJSON(await response.text())
}

export function selectDiagnosisForMode(
  mode: DiagnosisViewMode,
  liveDiagnosis: Diagnosis | null,
  replay: ReplayArtifact | null,
): Diagnosis | null {
  return mode === 'historical' ? replay?.diagnosis ?? null : liveDiagnosis
}

export function isLiveDiagnosisRecovered(
  mode: DiagnosisViewMode,
  liveDiagnosis: Diagnosis | null,
  activeLeaks?: number,
): boolean {
  return mode === 'live' && liveDiagnosis !== null && activeLeaks === 0
}

