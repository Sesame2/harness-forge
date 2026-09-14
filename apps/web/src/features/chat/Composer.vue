<script setup lang="ts">
import { ref } from 'vue'
import { useChatStore } from './chatStore'

const chat = useChatStore()
const draft = ref('')
async function submit() {
  const text = draft.value
  if (await chat.submit(text)) draft.value = ''
}
</script>

<template>
  <form class="composer" aria-label="发送消息" @submit.prevent="submit">
    <label for="message-content" class="eyebrow">问题 / MESSAGE</label>
    <textarea id="message-content" v-model="draft" aria-label="消息内容" rows="3" placeholder="围绕项目资料，描述你想分析的问题…"
      :disabled="!chat.ready || chat.pending" @keydown.ctrl.enter.prevent="submit" @keydown.meta.enter.prevent="submit" />
    <div class="composer-actions"><p>可继续提交问题，Run 将依次执行。</p>
      <button type="submit" class="primary-button" :disabled="!chat.ready || chat.pending || !draft.trim()">{{ chat.pending ? '正在提交…' : '发送' }}</button>
    </div>
  </form>
</template>

<style scoped>
.composer { margin-top: auto; padding: 18px 22px 22px; border-top: 1px solid var(--line); background: var(--paper-raised); }
label { display: block; margin-bottom: 8px; }
textarea { display: block; width: 100%; resize: vertical; min-height: 88px; max-height: 280px; border: 1px solid var(--line); padding: 10px 12px; background: var(--paper-raised); color: var(--ink); border-radius: 0; }
textarea:focus-visible { outline: 2px solid var(--moss); outline-offset: 2px; }
.composer-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 10px; }
.composer-actions p { font-size: 10px; }
</style>
