import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import App from '../App.vue'
import ChatPanel from '../features/chat/ChatPanel.vue'
import Composer from '../features/chat/Composer.vue'
import RunTimeline from '../features/runs/RunTimeline.vue'
import { useConversationStore } from '../features/conversations/conversationStore'
import { createWorkbenchRouter } from './router'
import { browserGlobals, conversation, json, project } from '../test/support'
import { liveEvents, queuedRun, runEvent, userMessage } from '../test/runSupport'
import type { Message, Run, RunEvent } from '../lib/api/types'

const mounted: ReturnType<typeof mount>[] = []
beforeEach(browserGlobals)
afterEach(() => { mounted.splice(0).forEach(w => w.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals() })
function backend() {
  const state = {
    messages: [] as Message[], runs: [] as Run[], history: new Map<string, RunEvent[]>(),
    streams: new Map<string, ReturnType<typeof liveEvents>>(), title: '',
  }
  const fetcher = vi.fn(async (url: string, options?: RequestInit): Promise<Response> => {
    if (url === '/api/v1/projects') return json([project])
    if (url === '/api/v1/projects/p1') return json(project)
    if (url === '/api/v1/projects/p1/conversations') return json([{ ...conversation, title: state.title }, { ...conversation, id: 'c2', title: '独立会话' }])
    if (url === '/api/v1/conversations/c1') return json({ ...conversation, title: state.title })
    if (url === '/api/v1/conversations/c2') return json({ ...conversation, id: 'c2', title: '独立会话' })
    if (url === '/api/v1/conversations/c1/messages') {
      if (options?.method === 'POST') {
        const message = { ...userMessage, id: `m${state.messages.length + 1}`, content: JSON.parse(String(options.body)).content }
        const run = { ...queuedRun, id: `r${state.runs.length + 1}`, trigger_message_id: message.id }
        state.messages.push(message); state.runs.push(run); state.title ||= message.content
        return json({ message, run }, 201)
      }
      return json(state.messages)
    }
    if (url === '/api/v1/conversations/c1/runs') return json(state.runs)
    for (const run of state.runs) {
      if (url === `/api/v1/runs/${run.id}`) return json(run)
      if (url === `/api/v1/runs/${run.id}/events?after_sequence=0`) return json(state.history.get(run.id) ?? [])
      if (url === `/api/v1/runs/${run.id}/events/stream`) {
        const stream = liveEvents(); state.streams.set(run.id, stream); return stream.response
      }
      if (url === `/api/v1/runs/${run.id}/cancel`) {
        run.status = 'cancelled'; run.finalized_at = '2026-09-14T09:00:00Z'; return json(run, 202)
      }
    }
    return json([])
  })
  vi.stubGlobal('fetch', fetcher)
  return { state, fetcher }
}
async function app(path = '/projects/p1/conversations/c1') {
  const router = createWorkbenchRouter(createMemoryHistory())
  await router.push(path)
  const wrapper = mount(App, { global: { plugins: [createPinia(), router] } })
  mounted.push(wrapper); await flushPromises()
  return { wrapper, router }
}
async function send(wrapper: ReturnType<typeof mount>, text = '分析河流') {
  await wrapper.get('textarea[aria-label="消息内容"]').setValue(text)
  await wrapper.get('form[aria-label="发送消息"]').trigger('submit')
  await flushPromises()
}

it('mounts real chat/composer/timeline in the middle pane and shows each accepted message and queued Run immediately', async () => {
  const { state, fetcher } = backend()
  const { wrapper } = await app()
  expect(wrapper.findComponent(ChatPanel).exists()).toBe(true)
  expect(wrapper.findComponent(Composer).exists()).toBe(true)
  expect(wrapper.get('#chat-pane').find('textarea').exists()).toBe(true)
  await send(wrapper)
  expect(wrapper.get('#chat-pane').text()).toContain('分析河流')
  expect(wrapper.findComponent(RunTimeline).exists()).toBe(true)
  expect(wrapper.get('[data-run-id="r1"]').text()).toContain('排队中')
  expect(wrapper.get('#sidebar-pane a[href$="/c1"]').text()).toBe('分析河流')
  expect(wrapper.get('textarea').element.value).toBe('')
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  state.streams.get('r1')!.send(runEvent(1, 'phase.changed', { phase: 'agent' })); await flushPromises()
  await send(wrapper, '再分析一次')
  expect(wrapper.findAll('[data-run-id]')).toHaveLength(2)
  expect(wrapper.get('[data-run-id="r2"]').text()).toContain('排队中')
  expect(fetcher.mock.calls.filter(([url, init]) => url.endsWith('/messages') && init?.method === 'POST')).toHaveLength(2)
  expect(localStorage.length).toBe(0)
})

it.each(['submit', 'terminal'])('keeps a completed sidebar rename when an older %s metadata refresh arrives last', async trigger => {
  const { state, fetcher } = backend()
  state.title = '原有标题'
  const original = fetcher.getMockImplementation()!
  const renamed = { ...conversation, title: '用户新标题', updated_at: '2026-09-14T10:00:00Z' }
  let holdNextRead = false
  let finishRead!: () => void
  fetcher.mockImplementation(async (url, init) => {
    if (url === '/api/v1/conversations/c1' && init?.method === 'PATCH') {
      state.title = renamed.title
      return json(renamed)
    }
    if (url === '/api/v1/conversations/c1' && holdNextRead) {
      holdNextRead = false
      const oldSnapshot = { ...conversation, title: state.title }
      return new Promise<Response>(resolve => { finishRead = () => resolve(json(oldSnapshot)) })
    }
    return original(url, init)
  })
  const { wrapper } = await app()
  if (trigger === 'terminal') await send(wrapper)
  holdNextRead = true
  if (trigger === 'submit') await send(wrapper)
  else {
    state.runs[0] = { ...state.runs[0]!, status: 'succeeded', finalized_at: '2026-09-14T09:00:00Z' }
    state.streams.get('r1')!.send(runEvent(1, 'run.succeeded')); await flushPromises()
  }
  expect(finishRead).toBeTypeOf('function')
  await wrapper.get('[aria-label="重命名 原有标题"]').trigger('click')
  await wrapper.get('input[aria-label="会话标题"]').setValue(renamed.title)
  await wrapper.get('#sidebar-pane form').trigger('submit'); await flushPromises()
  expect(wrapper.get('#sidebar-pane a[href$="/c1"]').text()).toBe(renamed.title)
  finishRead(); await flushPromises()
  expect(wrapper.get('#sidebar-pane a[href$="/c1"]').text()).toBe(renamed.title)
  expect(useConversationStore().selected).toEqual(renamed)
})

it('merges deltas, closes them with canonical replies, and preserves identical text blocks across refresh', async () => {
  const { state } = backend()
  const first = await app(); await send(first.wrapper)
  const stream = state.streams.get('r1')!
  stream.send(runEvent(1, 'assistant.delta', { text: '同' }))
  stream.send(runEvent(2, 'assistant.delta', { text: '一回复' })); await flushPromises()
  expect(first.wrapper.get('[data-preview-run="r1"]').text()).toContain('同一回复')
  const assistant = { ...userMessage, role: 'assistant' as const, content: '同一回复' }
  state.messages.push({ ...assistant, id: 'a1' })
  stream.send(runEvent(3, 'assistant.message', { text: '同一回复' })); await flushPromises()
  expect(first.wrapper.find('[data-preview-run]').exists()).toBe(false)
  expect(first.wrapper.findAll('[data-message-role="assistant"]')).toHaveLength(1)
  stream.send(runEvent(4, 'assistant.delta', { text: '同一' })); await flushPromises()
  state.messages.push({ ...assistant, id: 'a2' })
  stream.send(runEvent(5, 'assistant.message', { text: '同一回复' })); await flushPromises()
  expect(first.wrapper.findAll('[data-message-role="assistant"]')).toHaveLength(2)
  state.history.set('r1', [runEvent(1, 'assistant.delta', { text: '同' }), runEvent(2, 'assistant.delta', { text: '一回复' }), runEvent(3, 'assistant.message', { text: '同一回复' }), runEvent(4, 'assistant.delta', { text: '同一' }), runEvent(5, 'assistant.message', { text: '同一回复' })])
  first.wrapper.unmount(); mounted.splice(mounted.indexOf(first.wrapper), 1)
  const refreshed = await app()
  expect(refreshed.wrapper.findAll('[data-message-role="assistant"]')).toHaveLength(2)
  expect(refreshed.wrapper.find('[data-preview-run]').exists()).toBe(false)
})

it('does not duplicate a canonical reply when its closing event is still in the replay tail', async () => {
  const { state, fetcher } = backend()
  state.messages = [userMessage]; state.runs = [{ ...queuedRun, status: 'running', phase: 'agent' }]
  const original = fetcher.getMockImplementation()!
  fetcher.mockImplementation(async (url, init) => {
    if (url === '/api/v1/runs/r1/events?after_sequence=0') {
      // This assistant message commits after the event query snapshot, before
      // the canonical message refresh. Neither resource contains a shared ID.
      state.messages.push({ ...userMessage, id: 'a1', role: 'assistant', content: '完整回复' })
      return json([runEvent(1, 'assistant.delta', { text: '完整' })])
    }
    return original(url, init)
  })
  const { wrapper } = await app()
  expect(wrapper.findAll('[data-message-role="assistant"]')).toHaveLength(1)
  expect(wrapper.find('[data-preview-run]').exists()).toBe(false)
  state.streams.get('r1')!.send(runEvent(2, 'assistant.delta', { text: '回复' })); await flushPromises()
  expect(wrapper.find('[data-preview-run]').exists()).toBe(false)
  state.streams.get('r1')!.send(runEvent(3, 'assistant.message', { text: '完整回复' })); await flushPromises()
  state.streams.get('r1')!.send(runEvent(4, 'assistant.delta', { text: '下一段' })); await flushPromises()
  expect(wrapper.get('[data-preview-run]').text()).toContain('下一段')
  expect(wrapper.findAll('[data-message-role="assistant"]')).toHaveLength(1)
})

it('restores every Run history independently and subscribes nonfinalized outcomes, not inferred message Run IDs', async () => {
  const { state, fetcher } = backend()
  state.messages = [userMessage]
  state.runs = [
    { ...queuedRun, id: 'old', status: 'succeeded', phase: 'publishing', finalized_at: '2026-09-14T08:30:00Z' },
    { ...queuedRun, id: 'current', status: 'failed', phase: 'agent' },
    { ...queuedRun, id: 'next' },
  ]
  state.history.set('old', [runEvent(7, 'tool.started', { tool_call_id: 't', name: 'historical_tool', input: {} }, 'old'), runEvent(8, 'run.succeeded', {}, 'old')])
  state.history.set('current', [runEvent(2, 'agent.failed', { code: 'unavailable', message: 'safe failure', retryable: true }, 'current')])
  state.history.set('next', [runEvent(4, 'future.event', {}, 'next')])
  const { wrapper } = await app()
  expect(wrapper.findAllComponents(RunTimeline)).toHaveLength(3)
  expect(wrapper.get('[data-run-id="old"]').text()).toContain('historical_tool')
  expect(state.streams.has('old')).toBe(false)
  expect([...state.streams.keys()]).toEqual(['current', 'next'])
  for (const [id, cursor] of [['current', '2'], ['next', '4']]) {
    const call = fetcher.mock.calls.find(([url]) => url === `/api/v1/runs/${id}/events/stream`)!
    expect(new Headers(call[1]?.headers).get('Last-Event-ID')).toBe(cursor)
  }
  const paths = fetcher.mock.calls.map(([url]) => url)
  expect(paths.indexOf('/api/v1/conversations/c1/messages')).toBeLessThan(paths.indexOf('/api/v1/runs/old/events?after_sequence=0'))
  expect(paths.indexOf('/api/v1/conversations/c1/runs')).toBeLessThan(paths.indexOf('/api/v1/runs/old/events?after_sequence=0'))
  expect(paths.filter(path => path.includes('/events?'))).toHaveLength(3)
})

it('refreshes an active Run snapshot that became older than its fetched event history', async () => {
  const { state, fetcher } = backend()
  state.messages = [userMessage]; state.runs = [{ ...queuedRun }]
  const original = fetcher.getMockImplementation()!
  fetcher.mockImplementation(async (url, init) => {
    if (url === '/api/v1/runs/r1/events?after_sequence=0') {
      state.runs[0] = { ...queuedRun, status: 'running', phase: 'agent' }
      return json([runEvent(1, 'phase.changed', { phase: 'agent' })])
    }
    return original(url, init)
  })
  const { wrapper } = await app()
  expect(wrapper.get('[data-run-id="r1"] [role="status"]').text()).toBe('执行中 · 分析中')
  expect(fetcher.mock.calls.some(([url]) => url === '/api/v1/runs/r1')).toBe(true)
})

it('orders valid whole-second and fractional-second message timestamps chronologically', async () => {
  const { state } = backend()
  state.messages = [userMessage, { ...userMessage, id: 'a1', role: 'assistant', content: '回答', created_at: '2026-09-14T08:00:00.100Z' }]
  const { wrapper } = await app()
  expect(wrapper.findAll('[data-message-id]').map(w => w.attributes('data-message-id'))).toEqual(['m1', 'a1'])
})

it('replays the missing tail when finalization commits between history and the newer Run snapshot', async () => {
  const { state, fetcher } = backend()
  state.messages = [userMessage]; state.runs = [{ ...queuedRun }]
  const original = fetcher.getMockImplementation()!
  fetcher.mockImplementation(async (url, init) => {
    if (url === '/api/v1/runs/r1/events?after_sequence=0') {
      state.runs[0] = { ...queuedRun, status: 'succeeded', phase: 'publishing', finalized_at: '2026-09-14T09:00:00Z' }
      return json([runEvent(1, 'phase.changed', { phase: 'agent' })])
    }
    return original(url, init)
  })
  const { wrapper } = await app()
  expect(state.streams.has('r1')).toBe(true)
  state.streams.get('r1')!.send(runEvent(2, 'phase.changed', { phase: 'publishing' }))
  state.streams.get('r1')!.send(runEvent(3, 'run.succeeded')); await flushPromises()
  expect(wrapper.get('[aria-label="运行阶段"]').text()).toContain('正在发布')
  expect(state.streams.get('r1')!.body.locked).toBe(false)
})

it('refreshes authoritative terminal errors from empty terminal payloads and cancels only on explicit click', async () => {
  const { state, fetcher } = backend()
  const { wrapper } = await app(); await send(wrapper)
  state.runs[0] = { ...state.runs[0]!, status: 'failed', error: { code: 'failed', message: '分析未完成', request_id: 'request-123', details: { secret: 'do not show' } }, finalized_at: '2026-09-14T09:00:00Z' }
  state.streams.get('r1')!.send(runEvent(1, 'run.failed')); await flushPromises()
  expect(wrapper.get('[data-run-id="r1"] [role="alert"]').text()).toContain('分析未完成')
  expect(wrapper.get('[data-run-id="r1"] [role="alert"]').text()).toContain('request-123')
  expect(wrapper.text()).not.toContain('do not show')
  expect(fetcher.mock.calls.some(([url]) => url.endsWith('/cancel'))).toBe(false)
  await send(wrapper, '再试一次')
  await wrapper.get('[aria-label="取消 Run r2"]').trigger('click'); await flushPromises()
  expect(wrapper.get('[data-run-id="r2"]').text()).toContain('已取消')
  expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/cancel'))).toHaveLength(1)
})

it('query navigation keeps the stream alive, conversation navigation aborts browser streams only and isolates new history', async () => {
  const { state, fetcher } = backend()
  const { wrapper, router } = await app(); await send(wrapper)
  const streamCall = fetcher.mock.calls.find(([url]) => url.endsWith('/stream'))!
  const signal = streamCall[1]!.signal!
  const count = fetcher.mock.calls.length
  await router.replace({ query: { artifact: 'a1' } }); await flushPromises()
  expect(signal.aborted).toBe(false)
  expect(fetcher).toHaveBeenCalledTimes(count)
  await router.push('/projects/p1/conversations/c2'); await flushPromises()
  expect(signal.aborted).toBe(true)
  expect(state.streams.get('r1')!.body.locked).toBe(false)
  expect(wrapper.find('[data-run-id="r1"]').exists()).toBe(false)
  expect(wrapper.find('[data-message-id="m1"]').exists()).toBe(false)
  expect(fetcher.mock.calls.some(([url]) => url.endsWith('/cancel'))).toBe(false)
})

it('does not let an old conversation submission overwrite the current route', async () => {
  const { fetcher } = backend()
  const original = fetcher.getMockImplementation()!
  let finish!: (value: Response) => void
  fetcher.mockImplementation(async (url, init) => url === '/api/v1/conversations/c1/messages' && init?.method === 'POST'
    ? new Promise<Response>(resolve => { finish = resolve }) : original(url, init))
  const { wrapper, router } = await app()
  await wrapper.get('textarea').setValue('旧请求')
  await wrapper.get('form[aria-label="发送消息"]').trigger('submit'); await flushPromises()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  await router.push('/projects/p1/conversations/c2'); await flushPromises()
  finish(json({ message: { ...userMessage, content: '旧请求' }, run: queuedRun }, 201)); await flushPromises()
  expect(wrapper.get('#chat-pane').text()).not.toContain('旧请求')
  expect(wrapper.find('[data-run-id]').exists()).toBe(false)
  expect(wrapper.get('textarea').attributes('disabled')).toBeUndefined()
})

it('keeps the composer unavailable after a failed history load until the context is successfully retried', async () => {
  const { fetcher } = backend()
  const original = fetcher.getMockImplementation()!
  let unavailable = true
  fetcher.mockImplementation(async (url, init) => unavailable && url === '/api/v1/conversations/c1/messages'
    ? json({ code: 'unavailable', message: '历史暂时不可用', details: null, request_id: 'history-req' }, 503) : original(url, init))
  const { wrapper } = await app()
  expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
  expect(wrapper.get('#chat-pane [role="alert"]').text()).toContain('history-req')
  unavailable = false
  await wrapper.get('#chat-pane .chat-notice button').trigger('click'); await flushPromises()
  expect(wrapper.get('textarea').attributes('disabled')).toBeUndefined()
  await send(wrapper)
  expect(wrapper.find('[data-message-id="m1"]').exists()).toBe(true)
})

it('discards a late old history snapshot even when its transport ignores AbortSignal', async () => {
  const { fetcher } = backend()
  const original = fetcher.getMockImplementation()!
  let finish!: (value: Response) => void
  fetcher.mockImplementation(async (url, init) => url === '/api/v1/conversations/c1/messages'
    ? new Promise<Response>(resolve => { finish = resolve }) : original(url, init))
  const { wrapper, router } = await app()
  expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
  await router.push('/projects/p1/conversations/c2'); await flushPromises()
  finish(json([{ ...userMessage, content: '过期历史' }])); await flushPromises()
  expect(wrapper.get('#chat-pane').text()).not.toContain('过期历史')
  expect(wrapper.get('textarea').attributes('disabled')).toBeUndefined()
})

it('shows accepted messages before slow sidebar metadata and keeps the draft during a pending POST', async () => {
  const { fetcher } = backend()
  const original = fetcher.getMockImplementation()!
  let finishPost!: (value: Response) => void
  let accepted = false
  fetcher.mockImplementation(async (url, init) => {
    if (init?.method === 'POST' && url.endsWith('/messages')) return new Promise<Response>(resolve => { finishPost = resolve })
    if (accepted && url === '/api/v1/conversations/c1') return new Promise<Response>(() => {})
    return original(url, init)
  })
  const { wrapper } = await app()
  await wrapper.get('textarea').setValue('分析河流')
  await wrapper.get('form[aria-label="发送消息"]').trigger('submit'); await flushPromises()
  expect(wrapper.find('[data-message-id="m1"]').exists()).toBe(false)
  expect(wrapper.get('textarea').element.value).toBe('分析河流')
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  accepted = true; finishPost(json({ message: userMessage, run: queuedRun }, 201)); await flushPromises()
  expect(wrapper.find('[data-message-id="m1"]').exists()).toBe(true)
  expect(wrapper.get('[data-run-id="r1"]').text()).toContain('排队中')
  expect(wrapper.get('textarea').element.value).toBe('')
})

it('shows safe submission errors with correlation and retains the draft for retry', async () => {
  const { fetcher } = backend()
  const original = fetcher.getMockImplementation()!
  fetcher.mockImplementation(async (url, init) => init?.method === 'POST'
    ? json({ code: 'unavailable', message: '<script>safe error</script>', request_id: 'req-send', details: { secret: 'TOKEN' } }, 503) : original(url, init))
  const { wrapper } = await app(); await send(wrapper)
  expect(wrapper.get('#chat-pane [role="alert"]').text()).toContain('req-send')
  expect(wrapper.get('#chat-pane [role="alert"]').text()).toContain('<script>safe error</script>')
  expect(wrapper.find('#chat-pane script').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('TOKEN')
  expect(wrapper.get('textarea').element.value).toBe('分析河流')
})
