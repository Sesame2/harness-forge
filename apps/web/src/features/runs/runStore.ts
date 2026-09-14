import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { Run, RunEvent } from '../../lib/api/types'
import { isProductTerminal } from '../../lib/api/sse'

export const canCancel = (run: Run) => run.status === 'queued'
  || (run.status === 'running' && (run.phase === 'preparing' || run.phase === 'agent'))

export const useRunStore = defineStore('runs', () => {
  const items = ref<Run[]>([])
  const events = ref(new Map<string, RunEvent[]>())
  const cursors = ref(new Map<string, number>())
  const previews = ref(new Map<string, string>())
  const connectionErrors = ref(new Map<string, string>())
  function clear() {
    items.value = []; events.value = new Map(); cursors.value = new Map()
    previews.value = new Map(); connectionErrors.value = new Map()
  }
  function upsert(run: Run) {
    const index = items.value.findIndex(item => item.id === run.id)
    if (index === -1) items.value.push(run)
    else if (!items.value[index]!.finalized_at) items.value[index] = run
  }
  function apply(event: RunEvent, history = false) {
    if (event.sequence <= (cursors.value.get(event.run_id) ?? 0)) return
    const run = items.value.find(item => item.id === event.run_id)
    if (!run) return
    const list = events.value.get(run.id) ?? []
    list.push(event); events.value.set(run.id, list)
    const payload = event.payload
    if (event.type === 'assistant.delta' && typeof payload.text === 'string') {
      previews.value.set(run.id, (previews.value.get(run.id) ?? '') + payload.text)
    }
    if (event.type === 'assistant.message' || isProductTerminal(event.type)) previews.value.delete(run.id)
    // History describes the path, not a replacement for the newer REST snapshot.
    if (!history && event.type === 'phase.changed' && ['queued', 'running'].includes(run.status)
      && (payload.phase === 'preparing' || payload.phase === 'agent' || payload.phase === 'publishing')) {
      run.status = 'running'; run.phase = payload.phase
    }
    if (!history && event.type === 'agent.failed' && typeof payload.code === 'string' && typeof payload.message === 'string') {
      run.error = { code: payload.code, message: payload.message }
    }
    cursors.value.set(run.id, event.sequence)
  }
  return { items, events, cursors, previews, connectionErrors, clear, upsert, apply }
})
