import assert from 'node:assert/strict'
import test from 'node:test'

import {
  isLiveDiagnosisRecovered,
  loadReplayArtifact,
  parseReplayJSON,
  selectDiagnosisForMode,
  validateReplayArtifact,
} from './replay.ts'

const timestamp = '2026-10-08T08:22:50.000Z'

function validReplay() {
  return {
    schema_version: 1,
    mode: 'historical_replay',
    case_id: 'goroutine-leak-real',
    case_name: '真实 Goroutine 泄漏',
    recorded_at: timestamp,
    provider: { name: 'DeepSeek', model: 'deepseek-flash', protocol: 'OpenAI-compatible Chat Completions' },
    provenance: {
      source: 'real_provider_run',
      scenario: '注入 25 个受控 Goroutine。',
      verification: '已验证 observation 引用。',
      redactions: ['API 凭据未记录。'],
    },
    diagnosis: {
      id: 'diag_real',
      investigation_id: 'inv_real',
      generated_at: timestamp,
      summary: '发现 Goroutine 泄漏。',
      root_cause: 'LeakController.waitForever 永久阻塞。',
      confidence: 0.95,
      affected_component: 'LeakController.waitForever',
      recommendation: '增加退出信号。',
      evidence: [
        { observation_id: 'obs_status', source_tool: 'get_service_status', captured_at: timestamp, description: '25 active leaks', data: {} },
        { observation_id: 'obs_profile', source_tool: 'get_goroutine_profile', captured_at: timestamp, description: '25 matching stacks', data: {} },
      ],
      investigation: [
        { sequence: 1, agent_step: 1, tool_call_id: 'call_status', tool: 'get_service_status', arguments: {}, status: 'success', observation_id: 'obs_status', started_at: timestamp, completed_at: timestamp },
        { sequence: 2, agent_step: 1, tool_call_id: 'call_profile', tool: 'get_goroutine_profile', arguments: { top_n: 10 }, status: 'success', observation_id: 'obs_profile', started_at: timestamp, completed_at: timestamp },
        { sequence: 3, agent_step: 2, tool_call_id: 'call_submit', tool: 'submit_diagnosis', arguments: { evidence_refs: ['obs_status', 'obs_profile'] }, status: 'success', started_at: timestamp, completed_at: timestamp },
      ],
    },
  }
}

test('accepts a complete real replay envelope', () => {
  const replay = validateReplayArtifact(validReplay())
  assert.equal(replay.diagnosis.investigation.length, 3)
  assert.equal(replay.diagnosis.evidence.length, 2)
})

test('rejects malformed JSON', () => {
  assert.throws(() => parseReplayJSON('{broken'), (error) => error.code === 'invalid_replay_json')
})

test('rejects a missing required field', () => {
  const replay = validReplay()
  delete replay.diagnosis.root_cause
  assert.throws(() => validateReplayArtifact(replay), /diagnosis\.root_cause/)
})

test('rejects an unknown observation ID in submit_diagnosis', () => {
  const replay = validReplay()
  replay.diagnosis.investigation.at(-1).arguments.evidence_refs[1] = 'obs_unknown'
  assert.throws(() => validateReplayArtifact(replay), /evidence_refs/)
})

test('rejects timeline and Evidence tool mismatches', () => {
  const replay = validReplay()
  replay.diagnosis.evidence[1].source_tool = 'get_runtime_metrics'
  assert.throws(() => validateReplayArtifact(replay), /Tool 与 Evidence 不一致/)
})

test('reports historical record load failures', async () => {
  const fetcher = async () => new Response('missing', { status: 404 })
  await assert.rejects(loadReplayArtifact(fetcher), (error) => error.code === 'replay_load_failed')
})

test('loads replay without calling a diagnosis or requiring an API key', async () => {
  const requests = []
  const fetcher = async (url) => {
    requests.push(url)
    return new Response(JSON.stringify(validReplay()), { status: 200 })
  }
  const replay = await loadReplayArtifact(fetcher)
  assert.equal(replay.diagnosis.id, 'diag_real')
  assert.deepEqual(requests, ['/replays/goroutine-leak.json'])
})

test('keeps historical and live diagnosis state isolated', () => {
  const replay = validateReplayArtifact(validReplay())
  const live = { ...replay.diagnosis, id: 'diag_live' }
  assert.equal(selectDiagnosisForMode('historical', live, replay)?.id, 'diag_real')
  assert.equal(selectDiagnosisForMode('live', live, replay)?.id, 'diag_live')
  assert.equal(isLiveDiagnosisRecovered('historical', live, 0), false)
  assert.equal(isLiveDiagnosisRecovered('live', live, 0), true)
})

