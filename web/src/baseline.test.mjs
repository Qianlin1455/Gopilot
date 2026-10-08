import assert from 'node:assert/strict'
import test from 'node:test'

import { createBaselineState, observeBaseline } from './baseline.ts'

const observe = (state, goroutines, activeLeaks = 0, serviceStartedAt = 'service-a') => observeBaseline(state, {
  goroutines,
  activeLeaks,
  serviceStartedAt,
})

test('requires three stable fault-free samples before establishing a baseline', () => {
  let state = createBaselineState()
  state = observe(state, 7)
  assert.equal(state.value, null)
  state = observe(state, 6)
  assert.equal(state.value, null)
  state = observe(state, 7)
  assert.equal(state.value, 6)
})

test('never establishes a baseline from samples with active leaks', () => {
  let state = createBaselineState()
  state = observe(state, 31, 25)
  state = observe(state, 32, 25)
  state = observe(state, 31, 25)
  assert.equal(state.value, null)
  assert.deepEqual(state.candidates, [])
})

test('freezes an established baseline while a leak is active', () => {
  let state = createBaselineState()
  state = observe(state, 6)
  state = observe(state, 7)
  state = observe(state, 6)
  state = observe(state, 31, 25)
  state = observe(state, 32, 25)
  assert.equal(state.value, 6)
})

test('waits through reset transients before establishing a missing baseline', () => {
  let state = createBaselineState()
  state = observe(state, 31, 25)
  state = observe(state, 30)
  state = observe(state, 7)
  state = observe(state, 6)
  assert.equal(state.value, null)
  state = observe(state, 7)
  assert.equal(state.value, 6)
})

test('discards a baseline when the service instance changes', () => {
  let state = createBaselineState()
  state = observe(state, 6)
  state = observe(state, 7)
  state = observe(state, 6)
  assert.equal(state.value, 6)

  state = observe(state, 31, 25, 'service-b')
  assert.equal(state.value, null)
  assert.deepEqual(state.candidates, [])
})
