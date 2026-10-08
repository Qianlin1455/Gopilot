export const BASELINE_SAMPLE_COUNT = 3
export const BASELINE_MAX_SPREAD = 2

export type BaselineState = {
  value: number | null
  candidates: number[]
  serviceStartedAt: string | null
}

export type BaselineObservation = {
  goroutines: number
  activeLeaks: number
  serviceStartedAt?: string
}

export function createBaselineState(serviceStartedAt: string | null = null): BaselineState {
  return {
    value: null,
    candidates: [],
    serviceStartedAt,
  }
}

export function observeBaseline(current: BaselineState, observation: BaselineObservation): BaselineState {
  const observedServiceStart = observation.serviceStartedAt ?? current.serviceStartedAt
  const serviceRestarted = Boolean(
    current.serviceStartedAt
    && observedServiceStart
    && current.serviceStartedAt !== observedServiceStart,
  )
  const state = serviceRestarted
    ? createBaselineState(observedServiceStart)
    : { ...current, serviceStartedAt: observedServiceStart }

  if (observation.activeLeaks > 0) {
    return { ...state, candidates: [] }
  }

  const candidates = [...state.candidates, observation.goroutines].slice(-BASELINE_SAMPLE_COUNT)
  if (candidates.length < BASELINE_SAMPLE_COUNT) {
    return { ...state, candidates }
  }

  const minimum = Math.min(...candidates)
  const maximum = Math.max(...candidates)
  if (maximum - minimum > BASELINE_MAX_SPREAD) {
    return { ...state, candidates: candidates.slice(1) }
  }

  return {
    ...state,
    value: state.value === null ? minimum : Math.min(state.value, minimum),
    candidates: candidates.slice(1),
  }
}
