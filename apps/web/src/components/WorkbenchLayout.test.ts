import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { h, nextTick } from 'vue'
import entry from '../../index.html?raw'
import App from '../App.vue'
import { createWorkbenchRouter } from '../app/router'
import WorkbenchLayout from './WorkbenchLayout.vue'

const mounted: ReturnType<typeof mount>[] = []
let media: MediaQueryList

beforeEach(() => {
  // Node's experimental localStorage can shadow jsdom's implementation.
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
    get length() { return values.size },
  })
  media = Object.assign(new EventTarget(), {
    matches: false, media: '(max-width: 999px)', onchange: null,
    addListener: () => {}, removeListener: () => {},
  }) as MediaQueryList
  vi.stubGlobal('matchMedia', () => media)
  vi.stubGlobal('innerWidth', 1440)
})
afterEach(() => {
  mounted.splice(0).forEach(wrapper => wrapper.unmount())
  document.body.innerHTML = ''
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function workbench(path = '/', slots?: Record<string, (...args: any[]) => any>) {
  const router = createWorkbenchRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = slots
    ? mount(WorkbenchLayout, { attachTo: document.body, slots, global: { plugins: [createPinia(), router] } })
    : mount(App, { attachTo: document.body, global: { plugins: [createPinia(), router] } })
  mounted.push(wrapper)
  await flushPromises()
  return { wrapper, router }
}

describe('WorkbenchLayout', () => {
  it('names the product and declares Chinese at the document entry point', () => {
    expect(entry).toContain('lang="zh-CN"')
    expect(entry).toContain('<title>Harness Forge</title>')
  })

  it('starts with a 240px collapsible session pane, 440px chat, and artifact region', async () => {
    const { wrapper } = await workbench()
    const handles = wrapper.findAll('[role="separator"]')
    expect(handles.map(handle => handle.attributes('aria-valuenow'))).toEqual(['240', '440'])
    expect(wrapper.get('#artifact-pane').attributes('aria-label')).toBe('制品')
    expect(wrapper.get('[aria-label="折叠会话栏"]').attributes('aria-expanded')).toBe('true')
    await wrapper.get('[aria-label="折叠会话栏"]').trigger('click')
    expect(wrapper.get('#sidebar-pane').isVisible()).toBe(false)
    await wrapper.get('[aria-label="展开会话栏"]').trigger('click')
    expect(wrapper.get('#sidebar-pane').isVisible()).toBe(true)
  })

  it.each([
    ['/', '先建立一个项目', undefined, undefined],
    ['/projects/terrain', '选择或新建会话', 'terrain', undefined],
    ['/projects/terrain/conversations/session-7', '从一个问题开始', 'terrain', 'session-7'],
  ])('renders the context for %s without pretending business actions are connected', async (path, heading, project, conversation) => {
    const { wrapper, router } = await workbench(path)
    expect(wrapper.text()).toContain(heading)
    expect(router.currentRoute.value.params.projectId).toBe(project)
    expect(router.currentRoute.value.params.conversationId).toBe(conversation)
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Get started')
  })

  it.each(['/projects', '/projects/terrain/conversations', '/unknown/path'])('falls back safely for %s', async path => {
    const { wrapper, router } = await workbench(path)
    expect(router.currentRoute.value.path).toBe('/')
    expect(wrapper.text()).toContain('先建立一个项目')
  })

  it('exposes feature slots and keeps artifact selection in the URL, preserving other query values', async () => {
    const { wrapper, router } = await workbench('/projects/p1/conversations/c1?artifact=a1&filter=maps&filter=pdf', {
      project: ({ projectId }) => h('span', `Project ${projectId}`),
      sidebar: () => h('span', 'Session list'),
      chat: ({ conversationId }) => h('span', `Chat ${conversationId}`),
      artifact: ({ selectedArtifact, selectArtifact }) => h('button', { onClick: () => selectArtifact('a2') }, `Artifact ${selectedArtifact}`),
    })
    expect(wrapper.text()).toContain('Project p1')
    expect(wrapper.text()).toContain('Session list')
    expect(wrapper.text()).toContain('Chat c1')
    expect(wrapper.text()).toContain('Artifact a1')
    await wrapper.get('#artifact-pane button').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ artifact: 'a2', filter: ['maps', 'pdf'] })
    expect(router.currentRoute.value.params).toEqual({ projectId: 'p1', conversationId: 'c1' })
    const refreshed = await workbench(router.currentRoute.value.fullPath)
    expect(refreshed.router.currentRoute.value.query.artifact).toBe('a2')
    expect(localStorage.length).toBe(0)
  })

  it('persists only pane widths and restores clamped values', async () => {
    const first = await workbench('/projects/p1/conversations/c1')
    await first.wrapper.get('[aria-label="对话栏宽度"]').trigger('keydown', { key: 'ArrowRight' })
    expect(JSON.parse(localStorage.getItem('harness-forge:pane-widths')!)).toEqual({ sidebar: 240, chat: 456 })
    const second = await workbench()
    expect(second.wrapper.get('[aria-label="对话栏宽度"]').attributes('aria-valuenow')).toBe('456')
    localStorage.setItem('harness-forge:pane-widths', JSON.stringify({ sidebar: -20, chat: 99999 }))
    const third = await workbench()
    expect(third.wrapper.findAll('[role="separator"]').map(handle => handle.attributes('aria-valuenow'))).toEqual(['200', '640'])
  })

  it('clears an artifact selection without dropping the project, conversation, or other query keys', async () => {
    const { wrapper, router } = await workbench('/projects/p1/conversations/c1?artifact=a1&view=table', {
      artifact: ({ selectArtifact }) => h('button', { onClick: () => selectArtifact(null) }, '清除选择'),
    })
    await wrapper.get('#artifact-pane button').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/projects/p1/conversations/c1?view=table')
  })

  it.each(['not json', 'null', '{"sidebar":"broken","chat":null}'])('ignores invalid stored preferences: %s', async value => {
    localStorage.setItem('harness-forge:pane-widths', value)
    const { wrapper } = await workbench()
    expect(wrapper.findAll('[role="separator"]').map(handle => handle.attributes('aria-valuenow'))).toEqual(['240', '440'])
  })

  it('continues rendering and resizing when browser storage is unavailable', async () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('full') })
    const { wrapper } = await workbench()
    await wrapper.get('[aria-label="对话栏宽度"]').trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.get('[aria-label="对话栏宽度"]').attributes('aria-valuenow')).toBe('456')
  })

  it('uses accessible single-panel tabs on narrow screens and supports arrow navigation', async () => {
    Object.assign(media, { matches: true })
    const { wrapper } = await workbench('/projects/p1/conversations/c1')
    const tabs = wrapper.findAll('[role="tab"]')
    expect(tabs.map(tab => tab.text())).toEqual(['会话', '对话', '制品'])
    expect(tabs.map(tab => tab.attributes('aria-selected'))).toEqual(['false', 'true', 'false'])
    for (const tab of tabs) {
      const panel = wrapper.get(`#${tab.attributes('aria-controls')}`)
      expect(panel.attributes('role')).toBe('tabpanel')
      expect(panel.attributes('aria-labelledby')).toBe(tab.attributes('id'))
      expect(panel.attributes('tabindex')).toBe('0')
    }
    expect(wrapper.get('#sidebar-pane').isVisible()).toBe(false)
    expect(wrapper.get('#chat-pane').isVisible()).toBe(true)
    await tabs[1]!.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.get('#artifact-pane').isVisible()).toBe(true)
    expect(wrapper.get('#chat-pane').isVisible()).toBe(false)
    expect(tabs[2]!.attributes('aria-selected')).toBe('true')
    expect(document.activeElement?.id).toBe('artifact-tab')
    await tabs[0]!.trigger('click')
    expect(wrapper.get('#sidebar-pane').isVisible()).toBe(true)
    Object.assign(media, { matches: false })
    media.dispatchEvent(new Event('change'))
    await nextTick()
    expect(wrapper.get('#chat-pane').isVisible()).toBe(true)
    expect(wrapper.get('#artifact-pane').isVisible()).toBe(true)
  })

  it('limits desktop chat width to leave usable artifact space without overwriting the preference', async () => {
    vi.stubGlobal('innerWidth', 1024)
    localStorage.setItem('harness-forge:pane-widths', '{"sidebar":320,"chat":640}')
    const { wrapper } = await workbench()
    expect(Number(wrapper.get('[aria-label="对话栏宽度"]').attributes('aria-valuemax'))).toBeLessThanOrEqual(348)
    expect(Number(wrapper.get('[aria-label="对话栏宽度"]').attributes('aria-valuenow'))).toBeLessThanOrEqual(348)
    expect(JSON.parse(localStorage.getItem('harness-forge:pane-widths')!).chat).toBe(640)
  })
})
