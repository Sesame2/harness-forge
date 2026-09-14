<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useRunStore } from '../runs/runStore'
import { useArtifactStore } from './artifactStore'
import { artifactURL } from '../../app/config'
import ArtifactFrame from '../../lib/artifact-viewer/ArtifactFrame.vue'
import ArtifactTabs from './ArtifactTabs.vue'

const props = defineProps<{ conversationId: string }>()
const route = useRoute()
const router = useRouter()
const runs = useRunStore()
const store = useArtifactStore()
// REST order is ascending; retain its finer timestamp/ID order when Date.parse ties.
const versions = computed(() => runs.items.filter(run => run.conversation_id === props.conversationId)
  .reverse().sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)))
watch(() => props.conversationId, id => store.clear(id), { immediate: true, flush: 'sync' })
watch(() => versions.value.map(run => `${run.id}:${run.status}`).join(','), () => {
  versions.value.forEach(run => { if (!store.errors.has(run.id)) void store.load(run) })
}, { immediate: true })
onBeforeUnmount(() => store.clear())
const selectedRun = computed(() => {
  const explicit = versions.value.find(run => run.id === route.query.run)
    ?? versions.value.find(run => store.byRun.get(run.id)?.some(artifact => artifact.id === route.query.artifact))
  return explicit ?? versions.value.find(run => run.status === 'succeeded') ?? versions.value[0]
})
const artifacts = computed(() => store.byRun.get(selectedRun.value?.id ?? '') ?? [])
const selected = computed(() => artifacts.value.find(artifact => artifact.id === route.query.artifact)
  ?? artifacts.value.find(artifact => artifact.is_primary) ?? artifacts.value[0])
const previewURL = computed(() => selected.value ? artifactURL(selected.value.gateway_url) : null)
const downloadURL = computed(() => {
  if (!previewURL.value) return null
  const url = new URL(previewURL.value.href); url.searchParams.set('download', '1'); return url.href
})
const loading = computed(() => Boolean(selectedRun.value && store.pending.has(selectedRun.value.id)))
const error = computed(() => store.errors.get(selectedRun.value?.id ?? ''))
function selectRun(event: Event) {
  void router.replace({ query: { ...route.query, run: (event.target as HTMLSelectElement).value, artifact: undefined } })
}
function selectArtifact(id: string) {
  void router.replace({ query: { ...route.query, run: selectedRun.value?.id, artifact: id } })
}
const statusLabels = { queued: '排队中', running: '运行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断' }
</script>

<template>
  <div class="artifact-panel">
    <div v-if="versions.length" class="version-bar">
      <label><span class="eyebrow">VERSION / 版本</span>
        <select aria-label="制品版本" :value="selectedRun?.id" @change="selectRun">
          <option v-for="(run, index) in versions" :key="run.id" :value="run.id">{{ `第 ${versions.length - index} 次运行 · ${statusLabels[run.status]} · ${run.id.slice(0, 8)}` }}</option>
        </select>
      </label>
      <span class="immutable-note">只读快照</span>
    </div>
    <ArtifactTabs v-if="artifacts.length" :artifacts="artifacts" :selected-id="selected?.id" @select="selectArtifact" />
    <div v-if="selected" class="artifact-toolbar">
      <div><h3>{{ selected.title }}</h3><p>{{ selected.entry_path }}</p></div>
      <div v-if="previewURL" class="artifact-actions">
        <a :href="downloadURL!" download rel="noopener noreferrer" aria-label="下载入口文件" title="下载入口文件，不包含关联资源">下载</a>
        <a :href="previewURL.href" target="_blank" rel="noopener noreferrer" aria-label="新标签页打开制品">新标签页 ↗</a>
      </div>
    </div>
    <ArtifactFrame v-if="selected" :artifact="selected" />
    <div v-else class="artifact-canvas">
      <span class="canvas-coordinate top-coordinate" aria-hidden="true">N ↑ &nbsp; / &nbsp; OUTPUT</span>
      <div class="artifact-empty">
        <p v-if="loading" role="status">正在读取制品…</p>
        <template v-else-if="error"><p role="alert" class="error-message">{{ error }}</p>
          <button type="button" class="quiet-button" aria-label="重新读取制品" @click="selectedRun && store.load(selectedRun)">重新读取制品</button>
        </template>
        <template v-else><span class="section-kicker">成果预览 / OUTPUT</span>
          <h2>{{ selectedRun ? '本次运行没有生成制品' : '尚无制品' }}</h2>
          <p>{{ selectedRun ? '可切换历史版本，或在对话中继续分析。' : '地图、表格与报告将在运行完成后呈现。' }}</p>
        </template>
      </div>
    </div>
    <p v-if="selected" class="artifact-footnote">不可变制品 · 下载仅包含入口文件，不包含关联资源</p>
  </div>
</template>

<style scoped>
.artifact-panel { display: flex; flex-direction: column; flex: 1; min-height: 0; }
.version-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 18px; border-bottom: 1px solid var(--line); }
.version-bar label { display: grid; gap: 7px; min-width: 0; }
select { min-width: 0; max-width: 100%; border: 0; background: transparent; color: var(--ink); font: inherit; font-size: 12px; }
.immutable-note { color: var(--ink-muted); font-size: 10px; white-space: nowrap; border-left: 1px solid var(--line); padding-left: 12px; }
.artifact-toolbar { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; padding: 16px 18px; border-bottom: 1px solid var(--line); }
h3 { font: 18px var(--font-title); }
.artifact-toolbar p { margin-top: 5px; font: 10px var(--font-mono); overflow-wrap: anywhere; }
.artifact-toolbar > div { min-width: 0; }
.artifact-actions { display: flex; gap: 14px; font-size: 11px; }
.artifact-actions a { white-space: nowrap; }
.artifact-footnote { padding: 10px 18px; font-size: 10px; border-top: 1px solid var(--line); }
.artifact-empty button { margin-top: 12px; }
</style>
