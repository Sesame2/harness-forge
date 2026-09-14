import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import App from '../App.vue'
import { createWorkbenchRouter } from './router'
import { browserGlobals, conversation, FakeXHR, json, project } from '../test/support'

const mounted: ReturnType<typeof mount>[] = []
beforeEach(browserGlobals)
afterEach(() => { mounted.splice(0).forEach(w => w.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals() })
async function app(path = '/') {
  const router = createWorkbenchRouter(createMemoryHistory())
  await router.push(path)
  const wrapper = mount(App, { global: { plugins: [createPinia(), router] } })
  mounted.push(wrapper)
  await flushPromises()
  return { wrapper, router }
}
it('creates an empty-system geo project, mounts actual project/input/sidebar actions, then creates a conversation', async () => {
  const unnamedConversation = { ...conversation, title: '' }
  const fetch = vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith('/projects') && options?.method === 'POST') return json(project, 201)
    if (url.endsWith('/projects')) return json([])
    if (url.endsWith('/projects/p1')) return json(project)
    if (url.endsWith('/conversations') && options?.method === 'POST') return json(unnamedConversation, 201)
    if (url.endsWith('/conversations/c1')) return json(unnamedConversation)
    return json([])
  })
  vi.stubGlobal('fetch', fetch)
  const { wrapper, router } = await app()
  await wrapper.get('[aria-label="新建项目"]').trigger('click')
  await wrapper.get('dialog input').setValue('城市观察')
  await wrapper.get('dialog form').trigger('submit'); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p1')
  expect(wrapper.get<HTMLSelectElement>('.project-context select').element.value).toBe('p1')
  expect(wrapper.get('.project-context input[type="file"]').attributes('accept')).toContain('text/csv')
  await wrapper.get('#sidebar-pane [aria-label="新建会话"]').trigger('click'); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p1/conversations/c1')
  expect(wrapper.get('#sidebar-pane a[href="/projects/p1/conversations/c1"]').text()).toBe('新会话')
  expect(localStorage.length).toBe(0)
})
it('switches projects and never displays a late response or previous project conversations', async () => {
  let resolve!: (value: Response) => void
  const second = { ...project, id: 'p2', name: '另一项目' }
  const fetch = vi.fn(async (url: string) => {
    if (url === '/api/v1/projects') return json([project, second])
    if (url === '/api/v1/projects/p1') return new Promise<Response>(r => { resolve = r })
    if (url === '/api/v1/projects/p2') return json(second)
    return json([])
  })
  vi.stubGlobal('fetch', fetch)
  const { wrapper, router } = await app('/projects/p1')
  await wrapper.get('select[aria-label="选择项目"]').setValue('p2'); await flushPromises()
  resolve(json(project)); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p2')
  expect(wrapper.get('select').element.value).toBe('p2')
  expect(wrapper.get('#sidebar-pane').text()).not.toContain('河流分析')
  expect(wrapper.get('.project-context').text()).toContain('项目资料')
})
it.each(['missing-project', 'foreign-conversation'])('explains an invalid route (%s) without showing stale business context', async kind => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url === '/api/v1/projects') return json([project])
    if (url === '/api/v1/projects/p1') return kind === 'missing-project'
      ? json({ code: 'not_found', message: 'not found', details: null, request_id: 'r' }, 404) : json(project)
    if (url === '/api/v1/conversations/c1') return json({ ...conversation, project_id: 'other' })
    return json([])
  }))
  const { wrapper } = await app('/projects/p1/conversations/c1')
  expect(wrapper.get('#chat-pane [role="alert"]').text()).toMatch(/不存在|不属于/)
  expect(wrapper.get('#sidebar-pane').text()).not.toContain('河流分析')
  expect(wrapper.get('#chat-pane a').attributes('href')).toBe(kind === 'missing-project' ? '/' : '/projects/p1')
})

it('does not reload project context or abort upload when only artifact query changes', async () => {
  FakeXHR.instances = []
  vi.stubGlobal('XMLHttpRequest', FakeXHR)
  const fetch = vi.fn(async (url: string) => url === '/api/v1/projects' ? json([project])
    : url === '/api/v1/projects/p1' ? json(project) : json([]))
  vi.stubGlobal('fetch', fetch)
  const { wrapper, router } = await app('/projects/p1')
  const picker = wrapper.get('input[type="file"]')
  Object.defineProperty(picker.element, 'files', { value: [new File(['x'], 'x.csv', { type: 'text/csv' })] })
  await picker.trigger('change')
  const callCount = fetch.mock.calls.length
  await router.replace({ query: { artifact: 'a1' } }); await flushPromises()
  expect(FakeXHR.instances[0]!.aborted).toBe(false)
  expect(fetch).toHaveBeenCalledTimes(callCount)
})

it('keeps late project creation in the list without hijacking a newer route', async () => {
  let resolve!: (value: Response) => void
  vi.stubGlobal('fetch', vi.fn(async (url: string, options?: RequestInit) => {
    if (url === '/api/v1/projects' && options?.method === 'POST') return new Promise<Response>(r => { resolve = r })
    if (url === '/api/v1/projects') return json([project])
    if (url === '/api/v1/projects/p1') return json(project)
    return json([])
  }))
  const { wrapper, router } = await app()
  await wrapper.get('[aria-label="新建项目"]').trigger('click')
  await wrapper.get('dialog input').setValue('另一项目')
  await wrapper.get('dialog form').trigger('submit')
  await router.push('/projects/p1'); await flushPromises()
  resolve(json({ ...project, id: 'p2', name: '另一项目' }, 201)); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p1')
  expect(wrapper.get('select').text()).toContain('另一项目')
  expect(wrapper.find('dialog').exists()).toBe(false)
})

it('explains malformed route IDs rather than presenting backend invalid request alone', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => url === '/api/v1/projects' ? json([])
    : json({ code: 'bad_request', message: 'invalid request', details: null, request_id: 'r' }, 400)))
  const { wrapper } = await app('/projects/not-an-id')
  expect(wrapper.get('#chat-pane [role="alert"]').text()).toContain('链接无效')
})
