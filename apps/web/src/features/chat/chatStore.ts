import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, ApiError, errorMessage } from '../../lib/api/client'
import { isProductTerminal, streamRunEvents } from '../../lib/api/sse'
import type { Message, Run } from '../../lib/api/types'
import { useConversationStore } from '../conversations/conversationStore'
import { canCancel, useRunStore } from '../runs/runStore'

function describeError(error: unknown, fallbackId: string) {
  return `${errorMessage(error)} 关联 ID：${error instanceof ApiError && error.request_id ? error.request_id : fallbackId}`
}

export const useChatStore = defineStore('chat', () => {
  const conversationId = ref('')
  const messages = ref<Message[]>([])
  const loading = ref(false)
  const ready = ref(false)
  const pending = ref(false)
  const error = ref('')
  const cancelling = ref(new Set<string>())
  const runs = useRunStore()
  const conversations = useConversationStore()
  let controller: AbortController | undefined

  function clear() {
    controller?.abort(); controller = undefined
    conversationId.value = ''; messages.value = []; loading.value = false; ready.value = false
    pending.value = false; error.value = ''; cancelling.value = new Set(); runs.clear()
  }
  function mergeMessages(result: Message[]) {
    const merged = new Map(messages.value.map(message => [message.id, message]))
    for (const message of result) merged.set(message.id, message)
    messages.value = [...merged.values()].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
  }
  async function refreshMessages(id: string, signal: AbortSignal) {
    const result = await api.listMessages(id, signal)
    if (!signal.aborted) mergeMessages(result)
  }
  function subscribe(run: Run, signal: AbortSignal) {
    if (run.finalized_at && runs.events.get(run.id)?.some(event => isProductTerminal(event.type))) return
    const id = run.conversation_id
    void streamRunEvents(run.id, {
      signal, cursors: runs.cursors,
      onEvent: async event => {
        if (signal.aborted) return
        if (event.type === 'assistant.message') {
          // The event has no Message ID. Close the preview and read canonical IDs,
          // rather than guessing associations or deduplicating identical text.
          runs.previews.delete(run.id)
          await refreshMessages(id, signal)
        }
        if (isProductTerminal(event.type)) {
          const [current] = await Promise.all([
            api.getRun(run.id, signal), refreshMessages(id, signal), conversations.refresh(id, signal),
          ])
          if (signal.aborted) return
          runs.upsert(current)
        }
        if (signal.aborted) return
        runs.apply(event)
        runs.connectionErrors.delete(run.id)
      },
      onRetry: () => {
        if (!signal.aborted) runs.connectionErrors.set(run.id, `连接中断，正在重连。关联 ID：${run.id}`)
      },
    })
  }
  async function load(id: string) {
    clear()
    conversationId.value = id
    const signal = (controller = new AbortController()).signal
    loading.value = true
    try {
      const [history, currentRuns] = await Promise.all([api.listMessages(id, signal), api.listRuns(id, signal)])
      if (signal.aborted) return
      messages.value = history
      currentRuns.forEach(runs.upsert)
      await Promise.all(currentRuns.map(async run => {
        const events = await api.listRunEvents(run.id, 0, signal)
        if (signal.aborted) return
        for (const event of events) runs.apply(event, true)
        // Active state may advance between the Run and event snapshots; finalized
        // history is immutable and does not need another resource read.
        if (!run.finalized_at) {
          const current = await api.getRun(run.id, signal)
          if (!signal.aborted) runs.upsert(current)
        }
      }))
      if (signal.aborted) return
      // Cover assistant messages committed after the first history snapshot.
      await refreshMessages(id, signal)
      if (signal.aborted) return
      ready.value = true
      runs.items.forEach(run => subscribe(run, signal))
    } catch (cause) {
      if (!signal.aborted) error.value = describeError(cause, id)
    } finally {
      if (!signal.aborted) loading.value = false
    }
  }
  async function submit(content: string) {
    const signal = controller?.signal
    const id = conversationId.value
    if (!signal || signal.aborted || !id || !content.trim() || pending.value || !ready.value) return false
    pending.value = true; error.value = ''
    try {
      const result = await api.submitMessage(id, content.trim(), signal)
      if (signal.aborted) return false
      mergeMessages([result.message]); runs.upsert(result.run)
      subscribe(result.run, signal)
      // The accepted message must not wait for sidebar metadata to refresh.
      void conversations.refresh(id, signal).catch(cause => {
        if (!signal.aborted) error.value = describeError(cause, id)
      })
      return true
    } catch (cause) {
      if (!signal.aborted) error.value = describeError(cause, id)
      return false
    } finally {
      if (!signal.aborted) pending.value = false
    }
  }
  async function cancel(runId: string) {
    const signal = controller?.signal
    const run = runs.items.find(item => item.id === runId)
    if (!signal || signal.aborted || !run || !canCancel(run) || cancelling.value.has(runId)) return
    cancelling.value.add(runId); error.value = ''
    try {
      const result = await api.cancelRun(runId, signal)
      if (!signal.aborted) runs.upsert(result)
    } catch (cause) {
      if (!signal.aborted) {
        error.value = describeError(cause, runId)
        // A phase transition can legitimately win the cancellation race.
        try { const current = await api.getRun(runId, signal); if (!signal.aborted) runs.upsert(current) } catch { /* Keep the original actionable error. */ }
      }
    } finally {
      if (!signal.aborted) cancelling.value.delete(runId)
    }
  }
  return { conversationId, messages, loading, ready, pending, error, cancelling, clear, load, submit, cancel }
})
