import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { createWorkbenchRouter } from '../../app/router'
import { useRunStore } from '../runs/runStore'
import { queuedRun } from '../../test/runSupport'
import { json } from '../../test/support'
import ArtifactPanel from './ArtifactPanel.vue'
import type { Artifact, Run } from '../../lib/api/types'

const run: Run = { ...queuedRun, status: 'succeeded', finalized_at: '2026-09-14T08:00:00Z' }
const report: Artifact = { id: 'a1', run_id: 'r1', title: '河流报告', type: 'html', entry_path: 'report.html', is_primary: true, gateway_url: 'http://localhost:8081/artifacts/a1/report.html?x=1', created_at: run.created_at }
const mounted: ReturnType<typeof mount>[] = []
beforeEach(() => { vi.stubEnv('VITE_ARTIFACT_ORIGIN', 'http://localhost:8081') })
afterEach(() => { mounted.splice(0).forEach(w => w.unmount()); vi.unstubAllGlobals(); vi.unstubAllEnvs() })
async function panel(items: Run[] = [run], query = '') {
  const pinia = createPinia(); const runs = useRunStore(pinia); items.forEach(runs.upsert)
  const router = createWorkbenchRouter(createMemoryHistory()); await router.push(`/projects/p1/conversations/c1${query}`)
  const wrapper = mount(ArtifactPanel, { props: { conversationId: 'c1' }, global: { plugins: [pinia, router] } }); mounted.push(wrapper)
  await flushPromises(); return { wrapper, runs, router }
}
it('switches immutable artifacts and old Runs, with download and safe new-tab links', async () => {
  vi.stubGlobal('fetch', async (url: string) => json(url.includes('/r1/') ? [report, { ...report, id: 'a2', title: '基础数据', type: 'data', is_primary: false, gateway_url: 'http://localhost:8081/artifacts/a2/data.json' }] : []))
  const { wrapper } = await panel([run, { ...run, id: 'r2', created_at: '2026-09-14T09:00:00Z' }])
  expect(wrapper.text()).toContain('本次运行没有生成制品')
  await wrapper.get('select[aria-label="制品版本"]').setValue('r1'); await flushPromises()
  expect(wrapper.get('iframe').attributes('src')).toBe(report.gateway_url)
  expect(wrapper.get('a[aria-label="下载入口文件"]').attributes('href')).toBe(`${report.gateway_url}&download=1`)
  expect(wrapper.get('a[aria-label="新标签页打开制品"]').attributes()).toMatchObject({ target: '_blank', rel: 'noopener noreferrer', href: report.gateway_url })
  await wrapper.get('button[data-artifact-id="a2"]').trigger('click'); await flushPromises()
  expect(wrapper.get('iframe').attributes('sandbox')).toBe('')
  expect(wrapper.find('textarea, [contenteditable], button[aria-label*="编辑"]').exists()).toBe(false)
})
it('isolates late responses after conversation changes and ignores artifacts with the wrong run_id', async () => {
  let finish!: (response: Response) => void
  vi.stubGlobal('fetch', () => new Promise<Response>(resolve => { finish = resolve }))
  const { wrapper, runs, router } = await panel()
  expect(wrapper.text()).toContain('正在读取制品')
  await router.push('/projects/p1/conversations/c2?artifact=a1')
  await wrapper.setProps({ conversationId: 'c2' }); runs.clear()
  finish(json([report])); await flushPromises()
  expect(wrapper.find('iframe').exists()).toBe(false)
  vi.stubGlobal('fetch', async () => json([{ ...report, run_id: 'foreign' }]))
  runs.upsert({ ...run, id: 'r2', conversation_id: 'c2' }); await flushPromises()
  expect(wrapper.find('iframe').exists()).toBe(false)
})
it('reports retryable read errors and does not expose foreign download links', async () => {
  let fail = true
  vi.stubGlobal('fetch', async () => fail ? json({ message: '制品读取失败' }, 503) : json([{ ...report, gateway_url: 'https://evil.test/x' }]))
  const { wrapper } = await panel()
  expect(wrapper.get('[role="alert"]').text()).toContain('制品读取失败')
  fail = false; await wrapper.get('button[aria-label="重新读取制品"]').trigger('click'); await flushPromises()
  expect(wrapper.find('iframe, img, a').exists()).toBe(false)
  expect(wrapper.get('[role="alert"]').text()).toContain('地址')
})

it('keeps explicit selection while independent version loads finish out of order', async () => {
  const responses = new Map<string, (response: Response) => void>()
  const fetcher = vi.fn((url: string) => new Promise<Response>(resolve => responses.set(url, resolve)))
  vi.stubGlobal('fetch', fetcher)
  const { wrapper, router } = await panel([run, { ...run, id: 'r2', created_at: '2026-09-14T09:00:00Z' }], '?artifact=a1')
  responses.get('/api/v1/runs/r1/artifacts')!(json([report])); await flushPromises()
  expect(wrapper.get('iframe').attributes('src')).toBe(report.gateway_url)
  await router.replace({ query: { run: 'r1', artifact: 'a1' } }); await flushPromises()
  responses.get('/api/v1/runs/r2/artifacts')!(json([{ ...report, id: 'a2', run_id: 'r2', gateway_url: 'http://localhost:8081/artifacts/a2/index.html' }]))
  await flushPromises()
  expect(wrapper.get('iframe').attributes('src')).toBe(report.gateway_url)
  expect(wrapper.get('select').element.value).toBe('r1')
  expect(fetcher).toHaveBeenCalledTimes(2)
})

it.each(['html', 'markdown', 'image', 'data'] as const)('retains safe download and new-tab actions for %s resources', async type => {
  vi.stubGlobal('fetch', async () => json([{ ...report, type }]))
  const { wrapper } = await panel()
  expect(wrapper.get('a[aria-label="下载入口文件"]').attributes('href')).toBe(`${report.gateway_url}&download=1`)
  expect(wrapper.get('a[aria-label="新标签页打开制品"]').attributes('rel')).toBe('noopener noreferrer')
})

it.each(['2026-09-14T08:00:00.000Z', '2026-09-14T08:00:00.000900Z'])('uses latest server order when timestamps share a parsed millisecond: %s', async created_at => {
  vi.stubGlobal('fetch', async (url: string) => json([{ ...report, id: url.includes('/r1/') ? 'a1' : 'a2', run_id: url.includes('/r1/') ? 'r1' : 'r2' }]))
  const { wrapper } = await panel([{ ...run, created_at: '2026-09-14T08:00:00.000Z' }, { ...run, id: 'r2', created_at }])
  expect(wrapper.get('select').element.value).toBe('r2')
  expect(wrapper.get('[data-artifact-id="a2"]').attributes('aria-pressed')).toBe('true')
})
