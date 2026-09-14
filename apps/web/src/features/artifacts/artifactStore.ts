import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, errorMessage } from '../../lib/api/client'
import type { Artifact, Run } from '../../lib/api/types'

export const useArtifactStore = defineStore('artifacts', () => {
  const conversationId = ref('')
  const byRun = ref(new Map<string, Artifact[]>())
  const pending = ref(new Set<string>())
  const errors = ref(new Map<string, string>())
  let controller = new AbortController()
  function clear(id = '') {
    controller.abort(); controller = new AbortController()
    conversationId.value = id; byRun.value = new Map(); pending.value = new Set(); errors.value = new Map()
  }
  async function load(run: Run) {
    if (run.conversation_id !== conversationId.value || run.status !== 'succeeded'
      || byRun.value.has(run.id) || pending.value.has(run.id)) return
    const signal = controller.signal
    pending.value.add(run.id); errors.value.delete(run.id)
    try {
      const artifacts = await api.listArtifacts(run.id, signal)
      if (!signal.aborted) byRun.value.set(run.id, artifacts.filter(artifact => artifact.run_id === run.id))
    } catch (cause) {
      if (!signal.aborted) errors.value.set(run.id, errorMessage(cause))
    } finally {
      if (!signal.aborted) pending.value.delete(run.id)
    }
  }
  return { conversationId, byRun, pending, errors, clear, load }
})
