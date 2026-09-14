import { afterEach, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ArtifactFrame from './ArtifactFrame.vue'
import type { Artifact } from '../api/types'

const artifact = { id: 'a1', run_id: 'r1', title: '河流报告', type: 'html', entry_path: 'report.html', is_primary: true, gateway_url: 'http://localhost:8081/artifacts/a1/report.html', created_at: '2026-09-14T08:00:00Z' } satisfies Artifact
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals() })
it.each(['html', 'markdown', 'data'] as const)('navigates directly to %s using a least-privilege iframe without fetching content', type => {
  vi.stubEnv('VITE_ARTIFACT_ORIGIN', 'http://localhost:8081')
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
  const wrapper = mount(ArtifactFrame, { props: { artifact: { ...artifact, type } } })
  expect(wrapper.get('iframe').attributes()).toMatchObject({ src: artifact.gateway_url, sandbox: type === 'html' ? 'allow-scripts' : '', title: artifact.title, referrerpolicy: 'no-referrer' })
  expect(wrapper.find('iframe').attributes('srcdoc')).toBeUndefined()
  expect(fetcher).not.toHaveBeenCalled()
  wrapper.unmount()
})
it('uses an image and refuses foreign resources for all rendering modes', () => {
  vi.stubEnv('VITE_ARTIFACT_ORIGIN', 'http://localhost:8081')
  const wrapper = mount(ArtifactFrame, { props: { artifact: { ...artifact, type: 'image' } } })
  expect(wrapper.get('img').attributes()).toMatchObject({ src: artifact.gateway_url, alt: artifact.title })
  expect(wrapper.find('iframe').exists()).toBe(false); wrapper.unmount()
  const unsafe = mount(ArtifactFrame, { props: { artifact: { ...artifact, gateway_url: 'https://evil.test/payload' } } })
  expect(unsafe.find('iframe, img, a').exists()).toBe(false)
  expect(unsafe.get('[role="alert"]').text()).toContain('地址'); unsafe.unmount()
})
