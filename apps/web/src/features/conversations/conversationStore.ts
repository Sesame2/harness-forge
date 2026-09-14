import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../../lib/api/client'
import type { Conversation } from '../../lib/api/types'

export const useConversationStore = defineStore('conversations', () => {
  const projectId = ref('')
  const items = ref<Conversation[]>([])
  const selected = ref<Conversation | null>(null)
  let selection = 0
  // Only mutations completed during the current read need to override its possibly older snapshot.
  let pendingChanges: Map<string, Conversation | null> | undefined
  function clear() {
    ++selection
    pendingChanges = undefined
    projectId.value = ''
    items.value = []
    selected.value = null
  }
  async function load(id: string, conversationId: string, signal: AbortSignal) {
    clear()
    projectId.value = id
    const version = selection
    const changes = new Map<string, Conversation | null>()
    pendingChanges = changes
    try {
      const result = await api.listConversations(id, signal)
      if (signal.aborted || version !== selection) return
      const current = conversationId ? await api.getConversation(conversationId, signal) : null
      if (signal.aborted || version !== selection) return
      if (current && current.project_id !== id) throw new Error('此会话不属于当前项目，请返回项目重新选择。')
      const merged = new Map(result.map(item => [item.id, item]))
      if (current) merged.set(current.id, current)
      for (const [changedId, item] of changes) {
        if (item) merged.set(changedId, item)
        else merged.delete(changedId)
      }
      items.value = [...merged.values()]
      selected.value = merged.get(conversationId) ?? null
      if (conversationId && !selected.value) throw new Error('会话不存在，可能已被删除。')
    } finally {
      if (pendingChanges === changes) pendingChanges = undefined
    }
  }
  function remember(conversation: Conversation) {
    if (projectId.value !== conversation.project_id) return
    pendingChanges?.set(conversation.id, conversation)
    items.value = [...items.value.filter(item => item.id !== conversation.id), conversation]
    if (selected.value?.id === conversation.id) selected.value = conversation
  }
  async function create(id: string) {
    const result = await api.createConversation(id)
    remember(result)
    return result
  }
  async function rename(id: string, title: string) {
    const result = await api.renameConversation(id, title.trim())
    remember(result)
  }
  async function remove(id: string) {
    const owner = projectId.value
    await api.deleteConversation(id)
    if (projectId.value !== owner) return
    pendingChanges?.set(id, null)
    items.value = items.value.filter(item => item.id !== id)
    if (selected.value?.id === id) selected.value = null
  }
  return { projectId, items, selected, clear, load, create, rename, remove, remember }
})
