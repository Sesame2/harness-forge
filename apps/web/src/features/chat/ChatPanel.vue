<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import Composer from './Composer.vue'
import { useChatStore } from './chatStore'
import { useRunStore } from '../runs/runStore'
import RunTimeline from '../runs/RunTimeline.vue'

const props = defineProps<{ conversationId: string }>()
const chat = useChatStore()
const runs = useRunStore()
// Go commits one canonical Message per assistant.message event. If messages
// are ahead, replay may still contain deltas for an already committed reply.
const showPreviews = computed(() => chat.ready && chat.messages.filter(message => message.role === 'assistant').length
  <= [...runs.events.values()].reduce((count, events) => count + events.filter(event => event.type === 'assistant.message').length, 0))
watch(() => props.conversationId, id => { void chat.load(id) }, { immediate: true })
onBeforeUnmount(chat.clear)
</script>

<template>
  <div class="chat-panel">
    <p v-if="chat.loading" class="chat-notice" role="status">正在读取对话与运行历史…</p>
    <div v-if="chat.error" class="chat-notice"><p role="alert" class="error-message">{{ chat.error }}</p>
      <button type="button" class="quiet-button" :disabled="chat.loading || chat.pending" @click="chat.load(conversationId)">重新读取</button>
    </div>
    <div class="chat-history" aria-label="对话记录">
      <div v-if="!chat.loading && !chat.messages.length && !chat.error" class="chat-intro">
        <span class="section-kicker">会话 / READY TO EXPLORE</span><h1>从一个问题开始</h1><p>描述分析目标，过程与回复将在这里展开。</p>
      </div>
      <template v-for="message in chat.messages" :key="message.id">
        <article class="chat-message" :class="message.role" :data-message-id="message.id" :data-message-role="message.role">
          <span class="eyebrow">{{ message.role === 'user' ? '你 / QUESTION' : '助手 / RESPONSE' }}</span>
          <p>{{ message.content }}</p>
        </article>
        <RunTimeline v-for="run in runs.items.filter(item => item.trigger_message_id === message.id)" :key="run.id"
          :run-id="run.id" :cancelling="chat.cancelling.has(run.id)" @cancel="chat.cancel" />
      </template>
      <RunTimeline v-for="run in runs.items.filter(item => !chat.messages.some(message => message.id === item.trigger_message_id))" :key="run.id"
        :run-id="run.id" :cancelling="chat.cancelling.has(run.id)" @cancel="chat.cancel" />
      <template v-if="showPreviews">
        <article v-for="[runId, text] in runs.previews" :key="runId" class="chat-message assistant" :data-preview-run="runId">
          <span class="eyebrow">助手 / 正在回复</span><p>{{ text }}</p>
        </article>
      </template>
    </div>
    <Composer />
  </div>
</template>

<style scoped>
.chat-panel { display: flex; flex-direction: column; flex: 1; min-height: 0; }
.chat-history { padding: 24px 22px 8px; }
.chat-intro { margin: 16px 6px 38px; }
.chat-intro p { margin-top: 12px; font-size: 12px; }
.chat-message { margin-bottom: 20px; overflow-wrap: anywhere; }
.chat-message p { margin-top: 8px; white-space: pre-wrap; color: var(--ink); line-height: 1.85; }
.chat-message.user { border-bottom: 1px solid var(--line); padding-bottom: 14px; }
.chat-message.assistant { padding: 6px 0; }
.chat-notice { padding: 18px 22px 0; font-size: 12px; }
.chat-notice button { margin-top: 8px; }
</style>
