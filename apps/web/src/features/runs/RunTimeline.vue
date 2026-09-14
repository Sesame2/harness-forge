<script setup lang="ts">
import { computed } from 'vue'
import { canCancel, useRunStore } from './runStore'

const props = defineProps<{ runId: string; cancelling?: boolean }>()
defineEmits<{ cancel: [runId: string] }>()
const store = useRunStore()
const run = computed(() => store.items.find(item => item.id === props.runId))
const statusLabels = { queued: '排队中', running: '执行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断' }
const phaseLabels = { preparing: '准备资料', agent: '分析中', publishing: '正在发布' }
const phases = computed(() => (store.events.get(props.runId) ?? []).flatMap(event => {
  const phase = event.payload.phase
  return event.type === 'phase.changed' && (phase === 'preparing' || phase === 'agent' || phase === 'publishing')
    ? [{ sequence: event.sequence, label: phaseLabels[phase] }] : []
}))
const steps = computed(() => {
  const tools = new Map<string, { id: string; name: string; input?: unknown; outcome?: string; output?: string; error?: string }>()
  for (const event of store.events.get(props.runId) ?? []) {
    const p = event.payload
    if (!['tool.started', 'tool.completed'].includes(event.type) || typeof p.tool_call_id !== 'string' || typeof p.name !== 'string') continue
    const step = tools.get(p.tool_call_id) ?? { id: p.tool_call_id, name: p.name }
    if (event.type === 'tool.started') step.input = p.input
    if (event.type === 'tool.completed') {
      step.outcome = typeof p.outcome === 'string' ? p.outcome : undefined
      step.output = typeof p.output === 'string' ? p.output : undefined
      step.error = typeof p.error === 'string' ? p.error : undefined
    }
    tools.set(step.id, step)
  }
  return [...tools.values()]
})
</script>

<template>
  <section v-if="run" class="run-timeline" :data-run-id="run.id" :aria-label="`Run ${run.id} 时间线`">
    <div class="run-heading">
      <span class="eyebrow">RUN / {{ run.id.slice(0, 8) }}</span>
      <span role="status">{{ statusLabels[run.status] }}<template v-if="run.status === 'running' && run.phase"> · {{ phaseLabels[run.phase] }}</template></span>
      <button v-if="canCancel(run)" type="button" class="quiet-button" :aria-label="`取消 Run ${run.id}`"
        :disabled="cancelling" @click="$emit('cancel', run.id)">{{ cancelling ? '正在取消…' : '取消' }}</button>
    </div>
    <p v-if="run.status === 'running' && run.phase === 'publishing'" class="run-note">正在发布，无法取消。请等待制品与运行状态确认。</p>
    <p v-else-if="!['queued', 'running'].includes(run.status) && !run.finalized_at" class="run-note">正在确认运行结束…</p>
    <ol v-if="phases.length" class="run-phases" aria-label="运行阶段"><li v-for="phase in phases" :key="phase.sequence">{{ phase.label }}</li></ol>
    <details v-for="step in steps" :key="step.id" class="tool-step">
      <summary>{{ step.name }} <span>{{ step.outcome === 'succeeded' ? '完成' : step.outcome === 'failed' ? '失败' : '进行中' }}</span></summary>
      <pre v-if="step.input !== undefined">{{ JSON.stringify(step.input, null, 2) }}</pre>
      <pre v-if="step.output">{{ step.output }}</pre>
      <p v-if="step.error" class="error-message">{{ step.error }}</p>
    </details>
    <p v-if="run.error" role="alert" class="error-message">{{ run.error.message }} <span>关联 ID：{{ run.error.request_id || run.id }}</span></p>
    <p v-if="store.connectionErrors.get(run.id)" role="status" class="run-note">{{ store.connectionErrors.get(run.id) }}</p>
  </section>
</template>

<style scoped>
.run-timeline { margin: 12px 0 24px; padding: 12px 14px; border-left: 2px solid var(--line); background: var(--paper); font-size: 11px; }
.run-heading { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.run-heading button { margin-left: auto; padding: 2px 8px; font-size: 11px; }
.run-note, .error-message { margin-top: 8px; }
.run-phases { display: flex; flex-wrap: wrap; gap: 6px 18px; margin: 10px 0 0; padding-left: 16px; color: var(--ink-muted); }
.error-message span { display: block; }
.tool-step { border-top: 1px solid var(--line); margin-top: 10px; padding-top: 8px; }
summary { cursor: pointer; overflow-wrap: anywhere; }
summary span { color: var(--ink-muted); margin-left: 8px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; font: 11px/1.7 var(--font-mono); }
</style>
