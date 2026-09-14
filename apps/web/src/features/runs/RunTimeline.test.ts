import { afterEach, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import RunTimeline from './RunTimeline.vue'
import { useRunStore } from './runStore'
import { queuedRun, runEvent } from '../../test/runSupport'
import type { Run } from '../../lib/api/types'

const mounted: ReturnType<typeof mount>[] = []
afterEach(() => mounted.splice(0).forEach(w => w.unmount()))
function timeline(run: Run = queuedRun) {
  const pinia = createPinia(); setActivePinia(pinia)
  const runs = useRunStore(); runs.upsert({ ...run })
  const wrapper = mount(RunTimeline, { props: { runId: run.id }, global: { plugins: [pinia] } })
  mounted.push(wrapper)
  return { runs, wrapper }
}

it.each([
  ['queued', null, true], ['running', 'preparing', true], ['running', 'agent', true],
  ['running', 'publishing', false], ['running', null, false], ['succeeded', 'publishing', false],
  ['failed', 'agent', false], ['cancelled', null, false], ['interrupted', 'preparing', false],
] as const)('only offers cancellation for %s / %s when allowed', (status, phase, allowed) => {
  const { wrapper } = timeline({ ...queuedRun, status, phase })
  expect(wrapper.find('button[aria-label="取消 Run r1"]').exists()).toBe(allowed)
  if (phase === 'publishing' && status === 'running') expect(wrapper.text()).toContain('正在发布')
})

it('merges tool steps into native collapsed details, ignores unknown UI events, and keeps agent completion nonterminal', async () => {
  const { wrapper, runs } = timeline()
  runs.apply(runEvent(1, 'phase.changed', { phase: 'agent' }))
  runs.apply(runEvent(2, 'tool.started', { tool_call_id: 't1', name: 'read_file', input: { path: 'input.csv' } }))
  runs.apply(runEvent(3, 'tool.completed', { tool_call_id: 't1', name: 'read_file', outcome: 'succeeded', output: 'read 5 rows' }))
  runs.apply(runEvent(3, 'tool.completed', { tool_call_id: 't1', name: 'read_file', outcome: 'succeeded' }))
  runs.apply(runEvent(4, 'agent.completed'))
  runs.apply(runEvent(5, 'future.internal', { secret: 'do not display' }))
  await wrapper.vm.$nextTick()
  expect(wrapper.findAll('details')).toHaveLength(1)
  expect(wrapper.get('details').attributes('open')).toBeUndefined()
  expect(wrapper.get('summary').text()).toContain('read_file')
  expect(wrapper.get('details').text()).toContain('read 5 rows')
  expect(wrapper.text()).not.toContain('do not display')
  expect(runs.items[0]!.status).toBe('running')
  expect(runs.cursors.get('r1')).toBe(5)
})

it('escapes error messages, excludes diagnostic details, and supplies the Run ID when runtime errors lack a request ID', () => {
  const { wrapper } = timeline({ ...queuedRun, status: 'failed', error: {
    code: 'agent_failed', message: '<img src=x onerror=alert(1)> safe text', details: { token: 'SECRET' },
  } })
  expect(wrapper.get('[role="alert"]').text()).toContain('<img src=x onerror=alert(1)> safe text')
  expect(wrapper.get('[role="alert"]').text()).toContain('r1')
  expect(wrapper.find('img').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('SECRET')
})

it('restores the phase history without regressing the authoritative finished Run', async () => {
  const { runs, wrapper } = timeline({ ...queuedRun, status: 'succeeded', phase: 'publishing', finalized_at: '2026-09-14T09:00:00Z' })
  runs.apply(runEvent(1, 'phase.changed', { phase: 'preparing' }), true)
  runs.apply(runEvent(2, 'phase.changed', { phase: 'agent' }), true)
  runs.apply(runEvent(3, 'phase.changed', { phase: 'publishing' }), true)
  await wrapper.vm.$nextTick()
  expect(wrapper.get('[aria-label="运行阶段"]').text()).toMatch(/准备资料.*分析中.*正在发布/)
  expect(wrapper.text()).toContain('已完成')
  expect(runs.items[0]!.status).toBe('succeeded')
})

it('does not regress finalization when an older cancellation response arrives after the terminal event refresh', () => {
  const finalized = { ...queuedRun, status: 'cancelled' as const, finalized_at: '2026-09-14T09:00:00Z' }
  const { runs } = timeline(finalized)
  runs.upsert({ ...queuedRun, status: 'cancelled' })
  expect(runs.items[0]!.finalized_at).toBe(finalized.finalized_at)
})
