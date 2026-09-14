import { vi } from 'vitest'
import type { Conversation, InputFile, Project } from '../lib/api/types'

export const project: Project = {
  id: 'p1', name: '城市观察', profile_id: 'geo-analysis', profile_version: '1',
  accepted_input_media_types: ['text/csv', 'application/geo+json'],
  created_at: '2026-09-14T08:00:00Z', updated_at: '2026-09-14T08:00:00Z',
}
export const conversation: Conversation = {
  id: 'c1', project_id: 'p1', title: '河流分析', active_sdk_session_id: null,
  created_at: '2026-09-01T08:00:00Z', updated_at: '2026-09-14T08:00:00Z',
}
export const inputFile: InputFile = {
  id: 'f1', project_id: 'p1', display_name: 'locations.csv', media_type: 'text/csv',
  size_bytes: 42, sha256_digest: 'abcd'.repeat(16), created_at: '2026-09-14T08:00:00Z',
}
export function json(value: unknown, status = 200) {
  return new Response(status === 204 ? null : JSON.stringify(value), { status })
}
export function browserGlobals() {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
    get length() { return values.size },
  })
  vi.stubGlobal('matchMedia', () => Object.assign(new EventTarget(), { matches: false }))
  vi.stubGlobal('innerWidth', 1440)
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true, value: function (this: HTMLDialogElement) { this.open = true },
  })
}

// Only the browser transport is substituted; production parsing and UI remain real.
export class FakeXHR {
  static instances: FakeXHR[] = []
  upload = new EventTarget()
  status = 0
  responseText = ''
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  onabort: (() => void) | null = null
  ontimeout: (() => void) | null = null
  body?: FormData
  method = ''
  url = ''
  aborted = false
  constructor() { FakeXHR.instances.push(this) }
  open(method: string, url: string) { this.method = method; this.url = url }
  send(body: FormData) { this.body = body }
  abort() { this.aborted = true; this.onabort?.() }
  progress(loaded: number, total: number, lengthComputable = true) {
    this.upload.dispatchEvent(new ProgressEvent('progress', { loaded, total, lengthComputable }))
  }
  respond(body: unknown, status = 201) {
    this.responseText = JSON.stringify(body); this.status = status; this.onload?.()
  }
}
