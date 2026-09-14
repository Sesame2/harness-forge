import type { Message, Run, RunEvent } from '../lib/api/types'

export const userMessage: Message = { id: 'm1', conversation_id: 'c1', role: 'user', content: '分析河流', created_at: '2026-09-14T08:00:00Z' }
export const queuedRun: Run = {
  id: 'r1', conversation_id: 'c1', trigger_message_id: 'm1', status: 'queued', phase: null, error: null,
  source_sdk_session_id: null, candidate_sdk_session_id: null, finalized_at: null,
  created_at: '2026-09-14T08:00:00Z', updated_at: '2026-09-14T08:00:00Z',
}
export function runEvent(sequence: number, type: string, payload: Record<string, unknown> = {}, runId = 'r1'): RunEvent {
  return { run_id: runId, sequence, type, payload, occurred_at: '2026-09-14T08:00:01Z' }
}
export const eventFrame = (event: RunEvent) => `id: ${event.sequence}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`
export function eventStream(events: RunEvent[]) {
  return new Response(new ReadableStream<Uint8Array>({ start(c) {
    c.enqueue(new TextEncoder().encode(events.map(eventFrame).join(''))); c.close()
  } }), { headers: { 'Content-Type': 'text/event-stream' } })
}
export function liveEvents() {
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({ start(c) { controller = c } })
  return { body, response: new Response(body), send: (event: RunEvent) => controller.enqueue(new TextEncoder().encode(eventFrame(event))) }
}
