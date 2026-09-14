<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useProjectStore } from './projectStore'
import { errorMessage } from '../../lib/api/client'
import type { Project } from '../../lib/api/types'

const emit = defineEmits<{ created: [project: Project]; close: [] }>()
const store = useProjectStore()
const dialog = ref<HTMLDialogElement>()
const name = ref('')
const busy = ref(false)
const error = ref('')
let alive = true
onMounted(() => dialog.value?.showModal())
onBeforeUnmount(() => { alive = false })
async function submit() {
  if (!name.value.trim() || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const project = await store.create(name.value)
    if (alive) emit('created', project)
  } catch (cause) { if (alive) error.value = errorMessage(cause) }
  finally { if (alive) busy.value = false }
}
</script>

<template>
  <dialog ref="dialog" aria-labelledby="create-project-title" class="project-dialog" @cancel.prevent="emit('close')">
    <form @submit.prevent="submit">
      <span class="section-kicker">NEW PROJECT / 新的探索</span>
      <h2 id="create-project-title">建立一个项目</h2>
      <label>项目名称<input v-model="name" autofocus required placeholder="例如：城市绿地与步行可达性" /></label>
      <p>分析配置：地理分析 <code>geo-analysis</code></p>
      <p class="dialog-note">支持 CSV 与 GeoJSON。配置版本以创建后的项目为准。</p>
      <p v-if="error" role="alert" class="error-message">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="quiet-button" @click="emit('close')">取消</button>
        <button type="submit" class="primary-button" :disabled="busy || !name.trim()">{{ busy ? '创建中…' : '创建项目' }}</button>
      </div>
    </form>
  </dialog>
</template>

<style scoped>
.project-dialog { width: min(460px, calc(100vw - 32px)); border: 1px solid var(--line); padding: 28px; color: var(--ink); background: var(--paper-raised); }
.project-dialog::backdrop { background: #28372f66; }
h2 { font: 500 25px var(--font-title); margin-bottom: 24px; }
label { display: grid; gap: 8px; margin-bottom: 18px; }
input { width: 100%; }
p { font-size: 12px; overflow-wrap: anywhere; }
.dialog-note { margin-top: 6px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 28px; }
</style>
