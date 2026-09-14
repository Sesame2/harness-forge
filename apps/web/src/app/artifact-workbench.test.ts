import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import App from '../App.vue'
import { createWorkbenchRouter } from './router'
import { browserGlobals, conversation, json, project } from '../test/support'
import { queuedRun, runEvent } from '../test/runSupport'
import { useRunStore } from '../features/runs/runStore'
import type { Artifact, Run } from '../lib/api/types'

const oldRun: Run = { ...queuedRun, id: 'old', status: 'succeeded', finalized_at: '2026-09-14T08:00:00Z' }
const newRun: Run = { ...oldRun, id: 'new', created_at: '2026-09-14T09:00:00Z' }
const artifact = (id: string, run_id: string, is_primary = true): Artifact => ({ id, run_id, is_primary, title: `河流报告 ${id}`, type: 'html', entry_path: 'report/index.html', gateway_url: `http://localhost:8081/artifacts/${id}/report/index.html`, created_at: '2026-09-14T08:00:00Z' })
const mounted: ReturnType<typeof mount>[] = []
beforeEach(() => { browserGlobals(); vi.stubEnv('VITE_ARTIFACT_ORIGIN', 'http://localhost:8081') })
afterEach(() => { mounted.splice(0).forEach(w => w.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.unstubAllEnvs() })
function backend() {
  const state = { runs: [oldRun, newRun], artifacts: new Map([['old', [artifact('a-old', 'old')]], ['new', [artifact('a-secondary', 'new', false), artifact('a-new', 'new')]]]) }
  const fetcher = vi.fn(async (url: string) => {
    if (url === '/api/v1/projects') return json([project])
    if (url === '/api/v1/projects/p1') return json(project)
    if (url === '/api/v1/projects/p1/conversations') return json([conversation, { ...conversation, id: 'c2' }])
    if (url === '/api/v1/conversations/c1') return json(conversation)
    if (url === '/api/v1/conversations/c2') return json({ ...conversation, id: 'c2' })
    if (url.endsWith('/messages') || url.endsWith('/inputs') || url === '/api/v1/conversations/c2/runs') return json([])
    if (url === '/api/v1/conversations/c1/runs') return json(state.runs)
    for (const run of state.runs) {
      if (url === `/api/v1/runs/${run.id}/artifacts`) return json(state.artifacts.get(run.id) ?? [])
      if (url === `/api/v1/runs/${run.id}/events?after_sequence=0`) return json([runEvent(1, `run.${run.status}`, {}, run.id)])
    }
    return json({ message: `unexpected ${url}` }, 404)
  })
  vi.stubGlobal('fetch', fetcher)
  return { state, fetcher }
}
async function app(query = '') {
  const pinia = createPinia()
  const router = createWorkbenchRouter(createMemoryHistory())
  await router.push(`/projects/p1/conversations/c1${query}`)
  const wrapper = mount(App, { global: { plugins: [pinia, router] } }); mounted.push(wrapper)
  await flushPromises()
  return { wrapper, router, pinia }
}

it('reconstructs versions from canonical Runs and Artifact resources on reload and defaults to the newest primary', async () => {
  const { fetcher } = backend()
  const { wrapper, router } = await app()
  expect(wrapper.get('#artifact-pane iframe').attributes('src')).toBe(artifact('a-new', 'new').gateway_url)
  const paths = fetcher.mock.calls.map(([url]) => url)
  expect(paths.indexOf('/api/v1/conversations/c1/runs')).toBeLessThan(paths.indexOf('/api/v1/runs/new/artifacts'))
  expect(paths.filter(url => url.endsWith('/artifacts'))).toHaveLength(2)
  const count = fetcher.mock.calls.length
  await router.replace({ query: { artifact: 'a-old' } }); await flushPromises()
  expect(wrapper.get('#artifact-pane iframe').attributes('src')).toBe(artifact('a-old', 'old').gateway_url)
  expect(fetcher).toHaveBeenCalledTimes(count)
  const refreshed = await app('?artifact=a-secondary')
  expect(refreshed.wrapper.get('#artifact-pane iframe').attributes('src')).toBe(artifact('a-secondary', 'new').gateway_url)
  await router.push('/projects/p1/conversations/c2?artifact=a-old'); await flushPromises()
  expect(wrapper.find('#artifact-pane iframe').exists()).toBe(false)
  expect(wrapper.get('#artifact-pane').text()).toContain('尚无制品')
})

it('keeps a previous success after failed Runs and adds newly succeeded live Runs through the existing RunStore', async () => {
  const { state, fetcher } = backend()
  state.runs.push({ ...newRun, id: 'failed', status: 'failed', created_at: '2026-09-14T10:00:00Z' })
  const { wrapper, pinia } = await app()
  expect(wrapper.get('#artifact-pane iframe').attributes('src')).toContain('/a-new/')
  expect(fetcher.mock.calls.some(([url]) => url === '/api/v1/runs/failed/artifacts')).toBe(false)
  const live = { ...newRun, id: 'live', created_at: '2026-09-14T11:00:00Z' }
  state.runs.push(live); state.artifacts.set('live', [artifact('a-live', 'live')])
  useRunStore(pinia).upsert(live); await flushPromises()
  expect(wrapper.get('#artifact-pane iframe').attributes('src')).toContain('/a-live/')
  expect(fetcher.mock.calls.filter(([url]) => url === '/api/v1/runs/live/artifacts')).toHaveLength(1)
})
