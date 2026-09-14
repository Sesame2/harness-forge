import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from './client'
import { uploadInput, normalizeInputFile } from './upload'
import { FakeXHR, inputFile, json, project } from '../../test/support'

afterEach(() => vi.unstubAllGlobals())
describe('API transports', () => {
  it('parses ordinary arrays, passes AbortSignal, and accepts an empty 204', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(json([project])).mockResolvedValueOnce(json(null, 204))
    vi.stubGlobal('fetch', fetch)
    const signal = new AbortController().signal
    expect(await api.listProjects(signal)).toEqual([project])
    expect(fetch.mock.calls[0]![1].signal).toBe(signal)
    await expect(api.deleteConversation('c1')).resolves.toBeUndefined()
    expect(fetch.mock.calls[1]![0]).toBe('/api/v1/conversations/c1')
  })
  it('preserves the direct backend error envelope for JSON and upload errors', async () => {
    const error = { code: 'conflict', message: 'resource conflict', details: { run: 'r1' }, request_id: 'req1' }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(error, 409)))
    await expect(api.createProject('城市', 'geo-analysis')).rejects.toMatchObject({ status: 409, ...error })
    const xhr = new FakeXHR()
    const result = uploadInput('p1', new File(['x'], 'x.csv'), { createXHR: () => xhr })
    xhr.respond(error, 409)
    await expect(result).rejects.toMatchObject({ status: 409, ...error })
  })
  it('uses native upload progress and waits for the server even at 100%', async () => {
    const xhr = new FakeXHR()
    const onProgress = vi.fn()
    const result = uploadInput('p1', new File(['x'], 'x.csv'), { createXHR: () => xhr, onProgress })
    expect(xhr.url).toBe('/api/v1/projects/p1/inputs')
    expect(xhr.body?.get('file')).toBeInstanceOf(File)
    expect(onProgress).not.toHaveBeenCalled()
    xhr.progress(2, 8)
    expect(onProgress).toHaveBeenLastCalledWith(25)
    xhr.progress(8, 8, false)
    expect(onProgress).toHaveBeenLastCalledWith(null)
    xhr.respond(inputFile)
    await expect(result).resolves.toEqual(inputFile)
  })
  it('aborts native upload, including a signal that was cancelled before send', async () => {
    for (const preAborted of [false, true]) {
      const xhr = new FakeXHR()
      const controller = new AbortController()
      if (preAborted) controller.abort()
      const result = uploadInput('p1', new File(['x'], 'x.csv'), { createXHR: () => xhr, signal: controller.signal })
      controller.abort()
      await expect(result).rejects.toMatchObject({ name: 'AbortError' })
      expect(preAborted ? xhr.body : xhr.aborted).toBe(preAborted ? undefined : true)
    }
  })
  it('reports malformed responses and network failure instead of hanging', async () => {
    const xhr = new FakeXHR()
    const result = uploadInput('p1', new File(['x'], 'x.csv'), { createXHR: () => xhr })
    xhr.onerror?.()
    await expect(result).rejects.toThrow('网络')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('upstream unavailable', { status: 502 })))
    await expect(api.listProjects()).rejects.toBeInstanceOf(ApiError)
  })
  it('normalizes missing CSV/GeoJSON MIME only when allowed by the project catalog', () => {
    const file = new File(['{}'], 'RIVERS.GEOJSON')
    expect(normalizeInputFile(file, project.accepted_input_media_types).type).toBe('application/geo+json')
    expect(normalizeInputFile(file, ['text/csv']).type).toBe('')
    expect(normalizeInputFile(new File(['x'], 'x.csv'), ['text/csv']).type).toBe('text/csv')
    expect(normalizeInputFile(new File(['x'], 'x.json'), project.accepted_input_media_types).type).toBe('')
    expect(normalizeInputFile(new File(['x'], 'x.csv', { type: 'text/html' }), ['text/csv']).type).toBe('text/html')
  })
})
