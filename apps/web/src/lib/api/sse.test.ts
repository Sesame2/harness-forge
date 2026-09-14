import { afterEach, expect, it, vi } from 'vitest'
import { streamRunEvents } from './sse'
import { runEvent, eventFrame, eventStream } from '../../test/runSupport'

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

it('reconnects after durable 1–3 with Last-Event-ID, deduplicates, and waits past agent completion', async () => {
  vi.useFakeTimers()
  const cursors = new Map<string, number>()
  const seen: number[] = []
  const fetcher = vi.fn().mockResolvedValueOnce(eventStream([
    runEvent(1, 'assistant.delta', { text: 'a' }), runEvent(2, 'agent.completed'), runEvent(3, 'future.event'),
  ])).mockResolvedValueOnce(eventStream([runEvent(3, 'future.event'), runEvent(4, 'agent.failed'), runEvent(5, 'run.succeeded')]))
  vi.stubGlobal('fetch', fetcher)
  const done = streamRunEvents('r1', { signal: new AbortController().signal, cursors, onEvent: e => { seen.push(e.sequence) } })
  await vi.advanceTimersByTimeAsync(250)
  await done
  expect(seen).toEqual([1, 2, 3, 4, 5])
  expect(new Headers(fetcher.mock.calls[1]![1].headers).get('Last-Event-ID')).toBe('3')
  expect(cursors.get('r1')).toBe(5)
  expect(fetcher).toHaveBeenCalledTimes(2)
})

it.each(['run.succeeded', 'run.failed', 'run.cancelled', 'run.interrupted'])('closes the reader at Go product terminal %s, even when the server leaves it open', async type => {
  const cancel = vi.fn()
  const body = new ReadableStream<Uint8Array>({ start(c) { c.enqueue(new TextEncoder().encode(eventFrame(runEvent(1, type)))) }, cancel })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body)))
  await streamRunEvents('r1', { signal: new AbortController().signal, cursors: new Map(), onEvent: () => {} })
  expect(cancel).toHaveBeenCalledOnce()
  expect(body.locked).toBe(false)
})

it('decodes one-byte UTF-8 chunks, comments, CRLF and multiline data without conflating run cursors', async () => {
  const payload = JSON.stringify(runEvent(8, 'assistant.delta', { text: '城市\n河流' })).replace(',"payload"', ',\r\ndata: "payload"')
  const text = `: keepalive\r\nid: 8\r\nevent: assistant.delta\r\ndata: ${payload}\r\n\r\n${eventFrame(runEvent(9, 'run.cancelled')).replaceAll('\n', '\r\n')}`
  const bytes = new TextEncoder().encode(text)
  const body = new ReadableStream<Uint8Array>({ start(c) { for (const byte of bytes) c.enqueue(Uint8Array.of(byte)); c.close() } })
  const fetcher = vi.fn().mockResolvedValue(new Response(body))
  vi.stubGlobal('fetch', fetcher)
  const cursors = new Map([['r1', 7], ['r2', 42]])
  const seen: string[] = []
  await streamRunEvents('r1', { signal: new AbortController().signal, cursors, onEvent: e => { if (typeof e.payload.text === 'string') seen.push(e.payload.text) } })
  expect(seen).toEqual(['城市\n河流'])
  expect(cursors.get('r2')).toBe(42)
  expect(new Headers(fetcher.mock.calls[0]![1].headers).get('Last-Event-ID')).toBe('7')
})

it('uses capped exponential backoff and removes its wait on abort', async () => {
  vi.useFakeTimers()
  const controller = new AbortController()
  const fetcher = vi.fn().mockRejectedValue(new Error('disconnected'))
  vi.stubGlobal('fetch', fetcher)
  const done = streamRunEvents('r1', { signal: controller.signal, cursors: new Map(), onEvent: () => {} })
  await vi.advanceTimersByTimeAsync(0)
  let calls = 1
  for (const delay of [250, 500, 1000, 2000, 4000, 5000, 5000]) {
    await vi.advanceTimersByTimeAsync(delay - 1)
    expect(fetcher).toHaveBeenCalledTimes(calls)
    await vi.advanceTimersByTimeAsync(1)
    expect(fetcher).toHaveBeenCalledTimes(++calls)
  }
  controller.abort(); await done
  expect(vi.getTimerCount()).toBe(0)
})

it('aborts a blocked reader without cancelling any product Run or retaining listeners', async () => {
  const controller = new AbortController()
  const remove = vi.spyOn(controller.signal, 'removeEventListener')
  const cancel = vi.fn()
  const body = new ReadableStream<Uint8Array>({ cancel })
  const fetcher = vi.fn().mockResolvedValue(new Response(body))
  vi.stubGlobal('fetch', fetcher)
  const done = streamRunEvents('r1', { signal: controller.signal, cursors: new Map(), onEvent: () => {} })
  await Promise.resolve(); await Promise.resolve()
  controller.abort(); await done
  expect(cancel).toHaveBeenCalledOnce()
  expect(body.locked).toBe(false)
  expect(remove).toHaveBeenCalledWith('abort', expect.any(Function))
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(fetcher.mock.calls[0]![0]).toBe('/api/v1/runs/r1/events/stream')
})

it('does not commit a cursor when event handling fails, and retries that event', async () => {
  vi.useFakeTimers()
  const event = runEvent(1, 'run.failed')
  vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => eventStream([event])))
  const cursors = new Map<string, number>()
  let attempts = 0
  const done = streamRunEvents('r1', { signal: new AbortController().signal, cursors, onEvent: () => {
    if (++attempts === 1) throw new Error('authoritative refresh failed')
  } })
  await vi.advanceTimersByTimeAsync(0)
  expect(cursors.get('r1')).toBeUndefined()
  await vi.advanceTimersByTimeAsync(250); await done
  expect(attempts).toBe(2)
  expect(cursors.get('r1')).toBe(1)
})
