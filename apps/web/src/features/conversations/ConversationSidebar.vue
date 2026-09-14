<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { errorMessage } from '../../lib/api/client'
import type { Conversation } from '../../lib/api/types'
import { useConversationStore } from './conversationStore'

const props = defineProps<{ projectId: string; conversationId: string }>()
const store = useConversationStore()
const router = useRouter()
const route = useRoute()
const keyword = ref('')
const editing = ref('')
const title = ref('')
const busy = ref(false)
const error = ref('')
let alive = true
onBeforeUnmount(() => { alive = false })
// Display only: an empty persisted title enables server-side naming on the first message.
function displayTitle(item: Conversation) { return item.title.trim() || '新会话' }
const groups = computed(() => {
  const today = new Date(); today.setHours(0, 0, 0, 0)
  const yesterday = new Date(today); yesterday.setDate(yesterday.getDate() - 1)
  const week = new Date(today); week.setDate(week.getDate() - 6)
  const labels = ['今天', '昨天', '最近七天', '更早']
  const groups = labels.map(label => ({ label, items: [] as Conversation[] }))
  const items = store.items.filter(item => item.project_id === props.projectId && displayTitle(item).toLocaleLowerCase().includes(keyword.value.trim().toLocaleLowerCase()))
    .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
  for (const item of items) {
    const updated = Date.parse(item.updated_at)
    const index = updated >= +today ? 0 : updated >= +yesterday ? 1 : updated >= +week ? 2 : 3
    groups[index]!.items.push(item)
  }
  return groups.filter(group => group.items.length)
})
async function create() {
  if (busy.value) return
  busy.value = true; error.value = ''
  const path = route.fullPath
  try {
    const result = await store.create(props.projectId)
    if (alive && route.fullPath === path) await router.push({ name: 'conversation', params: { projectId: result.project_id, conversationId: result.id } })
  } catch (cause) { if (alive) error.value = errorMessage(cause) }
  finally { if (alive) busy.value = false }
}
function edit(item: Conversation) { editing.value = item.id; title.value = item.title }
async function rename() {
  if (busy.value || !title.value.trim()) return
  busy.value = true; error.value = ''
  try { await store.rename(editing.value, title.value); if (alive) editing.value = '' }
  catch (cause) { if (alive) error.value = errorMessage(cause) }
  finally { if (alive) busy.value = false }
}
async function remove(item: Conversation) {
  if (busy.value || !window.confirm(`删除会话「${displayTitle(item)}」？删除后将不再显示在项目中。`)) return
  busy.value = true; error.value = ''
  try {
    await store.remove(item.id)
    if (alive && route.params.conversationId === item.id) await router.push({ name: 'project', params: { projectId: props.projectId } })
  } catch (cause) { if (alive) error.value = errorMessage(cause) }
  finally { if (alive) busy.value = false }
}
</script>

<template>
  <nav class="conversation-sidebar" aria-label="项目会话">
    <button type="button" class="primary-button new-conversation" aria-label="新建会话" :disabled="busy" @click="create">＋ 新建会话</button>
    <label class="search-label">查找会话<input v-model="keyword" type="search" placeholder="按标题筛选" /></label>
    <p v-if="error" role="alert" class="error-message">{{ error }}</p>
    <p v-if="!groups.length" class="empty-conversations">{{ keyword.trim() ? '没有匹配的会话。' : '还没有会话，从一个新问题开始。' }}</p>
    <section v-for="group in groups" :key="group.label" class="conversation-group">
      <h3>{{ group.label }}</h3>
      <ul>
        <li v-for="item in group.items" :key="item.id" :class="{ selected: item.id === conversationId }">
          <form v-if="editing === item.id" @submit.prevent="rename">
            <input v-model="title" aria-label="会话标题" required />
            <div class="row-actions"><button type="submit" class="quiet-button" :disabled="busy || !title.trim()">保存</button><button type="button" class="quiet-button" :disabled="busy" @click="editing = ''">取消</button></div>
          </form>
          <template v-else>
            <RouterLink :to="{ name: 'conversation', params: { projectId, conversationId: item.id } }" :aria-current="item.id === conversationId ? 'page' : undefined">{{ displayTitle(item) }}</RouterLink>
            <div class="row-actions">
              <button type="button" :aria-label="`重命名 ${displayTitle(item)}`" :disabled="busy" @click="edit(item)">重命名</button>
              <button type="button" :aria-label="`删除 ${displayTitle(item)}`" :disabled="busy" @click="remove(item)">删除</button>
            </div>
          </template>
        </li>
      </ul>
    </section>
  </nav>
</template>

<style scoped>
.conversation-sidebar { padding: 18px 14px; }
.new-conversation { width: 100%; }
.search-label { display: grid; gap: 6px; font-size: 11px; color: var(--ink-muted); margin: 18px 0; }
input { min-width: 0; width: 100%; }
.empty-conversations { font-size: 12px; margin-top: 24px; }
.conversation-group { margin-top: 24px; }
h3 { font-size: 11px; color: var(--ink-muted); letter-spacing: .08em; padding-left: 7px; }
ul { list-style: none; padding: 0; margin: 8px 0; }
li { padding: 9px 8px; border-left: 2px solid transparent; }
li.selected { background: var(--moss-light); border-left-color: var(--moss); }
a { display: block; text-decoration: none; overflow-wrap: anywhere; }
a:hover { text-decoration: underline; }
.row-actions { display: flex; flex-wrap: wrap; gap: 12px; margin-top: 5px; }
.row-actions button { background: transparent; border: 0; padding: 2px 0; font-size: 10px; color: var(--ink-muted); }
.row-actions button:hover { color: var(--moss); text-decoration: underline; }
</style>
