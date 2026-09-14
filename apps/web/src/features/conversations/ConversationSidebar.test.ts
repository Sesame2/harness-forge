import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { createWorkbenchRouter } from '../../app/router'
import ConversationSidebar from './ConversationSidebar.vue'
import { useConversationStore } from './conversationStore'
import { conversation, json } from '../../test/support'

beforeEach(() => { setActivePinia(createPinia()); vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-14T12:00:00Z')) })
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
async function sidebar() {
  const router = createWorkbenchRouter(createMemoryHistory())
  await router.push('/projects/p1/conversations/c1')
  const store = useConversationStore()
  store.projectId = 'p1'
  store.items = [conversation, { ...conversation, id: 'c2', title: '旧地图', updated_at: '2026-09-13T08:00:00Z' },
    { ...conversation, id: 'c3', title: '历史', updated_at: '2026-08-01T08:00:00Z' }]
  const wrapper = mount(ConversationSidebar, { props: { projectId: 'p1', conversationId: 'c1' }, global: { plugins: [router] } })
  return { wrapper, router, store }
}
it('groups by updated_at, filters locally and switches the conversation URL', async () => {
  const { wrapper, router } = await sidebar()
  expect(wrapper.findAll('h3').map(x => x.text())).toEqual(['今天', '昨天', '更早'])
  await wrapper.get('input[type="search"]').setValue('地图')
  expect(wrapper.text()).not.toContain('河流分析')
  await wrapper.get('a').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p1/conversations/c2')
  wrapper.unmount()
})
it.each(['', ' \t '])('displays and filters an unnamed server conversation without persisting the fallback: %j', async title => {
  const fetch = vi.fn().mockResolvedValue(json({ ...conversation, id: 'c4', title }, 201))
  vi.stubGlobal('fetch', fetch)
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
  const { wrapper, router, store } = await sidebar()
  await wrapper.get('[aria-label="新建会话"]').trigger('click'); await flushPromises()
  expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({})
  expect(store.items.find(item => item.id === 'c4')?.title).toBe(title)
  expect(wrapper.get('a[href="/projects/p1/conversations/c4"]').text()).toBe('新会话')
  await wrapper.get('input[type="search"]').setValue('新会话')
  expect(wrapper.findAll('a')).toHaveLength(1)
  await router.push('/projects/p1')
  await wrapper.get('a').trigger('click'); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p1/conversations/c4')
  await wrapper.get('[aria-label="重命名 新会话"]').trigger('click')
  expect(wrapper.get<HTMLInputElement>('input[aria-label="会话标题"]').element.value).toBe(title)
  await wrapper.get('form button[type="button"]').trigger('click')
  await wrapper.get('[aria-label="删除 新会话"]').trigger('click')
  expect(confirm).toHaveBeenCalledWith('删除会话「新会话」？删除后将不再显示在项目中。')
  expect(fetch).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})
it('creates from the server result, renames, confirms logical delete, and navigates back', async () => {
  const fetch = vi.fn().mockResolvedValueOnce(json({ ...conversation, id: 'c4', title: '新会话' }, 201))
    .mockResolvedValueOnce(json({ ...conversation, title: '新标题' })).mockResolvedValueOnce(json(null, 204))
  vi.stubGlobal('fetch', fetch)
  const { wrapper, router, store } = await sidebar()
  await wrapper.get('[aria-label="新建会话"]').trigger('click'); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p1/conversations/c4')
  await router.push('/projects/p1/conversations/c1')
  await wrapper.get('[aria-label="重命名 河流分析"]').trigger('click')
  await wrapper.get('input[aria-label="会话标题"]').setValue('新标题')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(store.items.find(x => x.id === 'c1')?.title).toBe('新标题')
  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
  await wrapper.get('[aria-label="删除 新标题"]').trigger('click')
  expect(fetch).toHaveBeenCalledTimes(2)
  await wrapper.get('[aria-label="删除 新标题"]').trigger('click'); await flushPromises()
  expect(confirm).toHaveBeenCalledTimes(2)
  expect(fetch.mock.calls[2]![1].method).toBe('DELETE')
  expect(store.items.some(x => x.id === 'c1')).toBe(false)
  expect(router.currentRoute.value.path).toBe('/projects/p1')
  wrapper.unmount()
})
it('keeps an active Run conversation and explains a 409 conflict', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ code: 'conflict', message: 'resource conflict', details: null, request_id: 'r' }, 409)))
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const { wrapper, store } = await sidebar()
  await wrapper.get('[aria-label="删除 河流分析"]').trigger('click'); await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toMatch(/取消.*等待.*Run/)
  expect(store.items).toHaveLength(3)
  wrapper.unmount()
})
it('isolates a slow old-project list and rejects a conversation owned by another project', async () => {
  let resolve!: (response: Response) => void
  vi.stubGlobal('fetch', vi.fn().mockImplementationOnce(() => new Promise(r => { resolve = r })).mockResolvedValue(json([])))
  const store = useConversationStore()
  const controller = new AbortController()
  const first = store.load('p1', '', controller.signal)
  controller.abort()
  await store.load('p2', '', new AbortController().signal)
  resolve(json([conversation])); await first
  expect(store.projectId).toBe('p2'); expect(store.items).toEqual([])
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(json([])).mockResolvedValueOnce(json(conversation)))
  await expect(store.load('p2', 'c1', new AbortController().signal)).rejects.toThrow('不属于')
})

it('does not navigate after a pending creation when the user has moved to another project', async () => {
  let resolve!: (value: Response) => void
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(r => { resolve = r })))
  const { wrapper, router, store } = await sidebar()
  await wrapper.get('[aria-label="新建会话"]').trigger('click')
  await router.push('/projects/p2')
  store.clear(); store.projectId = 'p2'
  resolve(json({ ...conversation, id: 'c4' }, 201)); await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/p2')
  expect(store.items).toEqual([])
  wrapper.unmount()
})
