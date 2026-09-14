<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useProjectStore } from './projectStore'
import { errorMessage } from '../../lib/api/client'
import type { Project } from '../../lib/api/types'
import CreateProjectDialog from './CreateProjectDialog.vue'

const store = useProjectStore()
const route = useRoute()
const router = useRouter()
const showCreate = ref(false)
const error = ref('')
const loading = ref(false)
const controller = new AbortController()
onBeforeUnmount(() => controller.abort())
watch(() => route.fullPath, () => { showCreate.value = false })
async function list() {
  loading.value = true
  error.value = ''
  try { await store.list(controller.signal) }
  catch (cause) { if (!controller.signal.aborted) error.value = errorMessage(cause) }
  finally { if (!controller.signal.aborted) loading.value = false }
}
void list()
function created(project: Project) {
  showCreate.value = false
  void router.push({ name: 'project', params: { projectId: project.id } })
}
function select(event: Event) {
  const id = (event.target as HTMLSelectElement).value
  if (id) void router.push({ name: 'project', params: { projectId: id } })
}
</script>

<template>
  <div class="project-switcher">
    <label class="project-select"><span class="eyebrow">PROJECT / 项目</span>
      <select aria-label="选择项目" :value="route.params.projectId || ''" @change="select">
        <option value="" disabled>{{ loading ? '加载项目…' : '选择项目' }}</option>
        <option v-for="project in store.items" :key="project.id" :value="project.id">{{ project.name }}</option>
      </select>
    </label>
    <button type="button" class="quiet-button" aria-label="新建项目" @click="showCreate = true">＋ 新建项目</button>
    <p v-if="error" role="alert" class="error-message">{{ error }} <button type="button" class="quiet-button" @click="list">重试</button></p>
    <CreateProjectDialog v-if="showCreate" @close="showCreate = false" @created="created" />
  </div>
</template>

<style scoped>
.project-switcher { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; min-width: 0; }
.project-select { display: grid; min-width: 0; flex: 1; }
select { width: 100%; min-width: 0; max-width: 340px; text-overflow: ellipsis; }
.quiet-button { flex-shrink: 0; }
.error-message { flex-basis: 100%; }
</style>
