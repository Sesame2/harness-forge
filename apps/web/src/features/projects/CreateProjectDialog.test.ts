import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import CreateProjectDialog from './CreateProjectDialog.vue'
import { browserGlobals, json, project } from '../../test/support'

beforeEach(browserGlobals)
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
it('offers only geo-analysis, rejects blank names, and emits the server project', async () => {
  const fetch = vi.fn().mockResolvedValue(json({ ...project, name: '服务端规范名称' }, 201))
  vi.stubGlobal('fetch', fetch)
  const wrapper = mount(CreateProjectDialog, { global: { plugins: [createPinia()] } })
  expect(wrapper.text()).toContain('geo-analysis')
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  await wrapper.get('input').setValue('  城市观察  ')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({ name: '城市观察', profile_id: 'geo-analysis' })
  expect(wrapper.emitted('created')?.[0]).toEqual([{ ...project, name: '服务端规范名称' }])
  wrapper.unmount()
})
it('keeps a failed project creation editable and exposes the backend message', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ code: 'bad_request', message: 'invalid profile', details: null, request_id: 'req' }, 400)))
  const wrapper = mount(CreateProjectDialog, { global: { plugins: [createPinia()] } })
  await wrapper.get('input').setValue('城市')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toContain('invalid profile')
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  wrapper.unmount()
})
