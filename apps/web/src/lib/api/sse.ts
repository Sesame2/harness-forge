import { decodeResponse, runEventsPath } from './client'
import type { RunEvent } from './types'

export const isProductTerminal = (type: string) => ['run.succeeded', 'run.failed', 'run.cancelled', 'run.interrupted'].includes(type)

function wait(delay: number, signal: AbortSignal) {
  return new Promise<void>(resolve => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve() }
    const timer = setTimeout(finish, delay)
    signal.addEventListener('abort', finish, { once: true })
    if (signal.aborted) finish()
  })
}

export async function streamRunEvents(runId: string, options: {
  signal: AbortSignal
  cursors: Map<string, number>
  onEvent: (event: RunEvent) => void | Promise<void>
  onRetry?: (error: unknown) => void
}) {
  const { signal, cursors, onEvent, onRetry } = options
  let delay = 250
  while (!signal.aborted) {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    const abortReader = () => { void reader?.cancel().catch(() => {}) }
    try {
      const response = await fetch(`/api/v1${runEventsPath(runId)}/stream`, {
        signal, headers: { Accept: 'text/event-stream', 'Last-Event-ID': String(cursors.get(runId) ?? 0) },
      })
      if (signal.aborted) { await response.body?.cancel(); return }
      if (!response.ok) decodeResponse(response.status, await response.text())
      if (!response.body) throw new Error('事件连接没有响应正文')
      reader = response.body.getReader()
      signal.addEventListener('abort', abortReader, { once: true })
      const decoder = new TextDecoder()
      let buffer = '', id = '', data: string[] = []
      while (!signal.aborted) {
        const { value, done } = await reader.read()
        buffer += decoder.decode(value, { stream: !done })
        let boundary: number
        while ((boundary = buffer.search(/[\r\n]/)) !== -1) {
          // A CR at the chunk boundary may be the first half of CRLF.
          if (!done && buffer[boundary] === '\r' && boundary === buffer.length - 1) break
          const line = buffer.slice(0, boundary)
          buffer = buffer.slice(boundary + (buffer.slice(boundary, boundary + 2) === '\r\n' ? 2 : 1))
          if (signal.aborted) return
          if (line === '') {
            if (data.length) {
              const event = JSON.parse(data.join('\n')) as RunEvent
              if (!/^\d+$/.test(id) || !event || event.run_id !== runId || !Number.isSafeInteger(event.sequence)
                || event.sequence !== Number(id) || typeof event.type !== 'string'
                || !event.payload || typeof event.payload !== 'object' || Array.isArray(event.payload)) {
                throw new Error('事件格式无效')
              }
              if (event.sequence > (cursors.get(runId) ?? 0)) {
                // A failed authoritative refresh must replay the same durable event.
                await onEvent(event)
                if (signal.aborted) return
                cursors.set(runId, event.sequence)
                delay = 250
                if (isProductTerminal(event.type)) return
              }
            }
            id = ''; data = []
          } else if (!line.startsWith(':')) {
            const colon = line.indexOf(':')
            const field = colon === -1 ? line : line.slice(0, colon)
            const text = colon === -1 ? '' : line.slice(colon + 1).replace(/^ /, '')
            if (field === 'id') id = text
            if (field === 'data') data.push(text)
          }
        }
        if (done) break
      }
      if (!signal.aborted) onRetry?.(new Error('事件连接已断开，正在重连。'))
    } catch (error) {
      if (!signal.aborted) onRetry?.(error)
    } finally {
      signal.removeEventListener('abort', abortReader)
      if (reader) {
        await reader.cancel().catch(() => {})
        reader.releaseLock()
      }
    }
    if (!signal.aborted) {
      await wait(delay, signal)
      delay = Math.min(delay * 2, 5000)
    }
  }
}
