import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import InputFiles from './InputFiles.vue'
import { FakeXHR, inputFile, json, project } from '../../test/support'

beforeEach(() => {
  FakeXHR.instances = []
  vi.stubGlobal('XMLHttpRequest', FakeXHR)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json([])))
})
afterEach(() => vi.unstubAllGlobals())
async function choose(wrapper: ReturnType<typeof mount>, file = new File(['x'], 'locations.csv', { type: 'text/csv' })) {
  const picker = wrapper.get('input[type="file"]')
  Object.defineProperty(picker.element, 'files', { configurable: true, value: [file] })
  await picker.trigger('change')
  await flushPromises()
}
it('uses catalog accept, real progress, server metadata, and bounded file details', async () => {
  const wrapper = mount(InputFiles, { props: { project } })
  await flushPromises()
  expect(wrapper.get('input').attributes('accept')).toContain('application/geo+json')
  await choose(wrapper)
  const xhr = FakeXHR.instances[0]!
  xhr.progress(42, 100)
  await flushPromises()
  expect(wrapper.get('progress').attributes('value')).toBe('42')
  xhr.respond(inputFile)
  await flushPromises()
  expect(wrapper.get('details').text()).toContain('locations.csv')
  expect(wrapper.text()).toContain('42 B')
  expect(wrapper.text()).toContain(inputFile.sha256_digest)
  wrapper.unmount()
})
it('cancels, shows failures, rejects unsupported inputs, and permits retry', async () => {
  const wrapper = mount(InputFiles, { props: { project } })
  await choose(wrapper)
  await wrapper.get('[aria-label="取消上传"]').trigger('click')
  await flushPromises()
  expect(FakeXHR.instances[0]!.aborted).toBe(true)
  expect(wrapper.text()).toContain('已取消')
  await choose(wrapper, new File(['x'], 'bad.exe', { type: 'application/octet-stream' }))
  expect(FakeXHR.instances).toHaveLength(1)
  expect(wrapper.get('[role="alert"]').text()).toContain('不支持')
  await choose(wrapper)
  FakeXHR.instances[1]!.respond({ code: 'bad_request', message: 'invalid data', details: null, request_id: 'r' }, 400)
  await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toContain('invalid data')
  await choose(wrapper, new File(['{}'], 'rivers.geojson'))
  expect((FakeXHR.instances[2]!.body?.get('file') as File).type).toBe('application/geo+json')
  wrapper.unmount()
  expect(FakeXHR.instances[2]!.aborted).toBe(true)
})
it('aborts reads and uploads on project change and ignores late old-project responses', async () => {
  let resolve!: (value: Response) => void
  const fetch = vi.fn().mockImplementationOnce(() => new Promise(r => { resolve = r })).mockResolvedValue(json([]))
  vi.stubGlobal('fetch', fetch)
  const wrapper = mount(InputFiles, { props: { project } })
  await choose(wrapper)
  await wrapper.setProps({ project: { ...project, id: 'p2' } })
  resolve(json([inputFile]))
  FakeXHR.instances[0]!.respond(inputFile)
  await flushPromises()
  expect(fetch.mock.calls[0]![1].signal.aborted).toBe(true)
  expect(FakeXHR.instances[0]!.aborted).toBe(true)
  expect(wrapper.text()).not.toContain('locations.csv')
  wrapper.unmount()
})
