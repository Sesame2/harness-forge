<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, errorMessage } from '../../lib/api/client'
import { inputAccept, normalizeInputFile, uploadInput } from '../../lib/api/upload'
import type { InputFile, Project } from '../../lib/api/types'

const props = defineProps<{ project: Project }>()
const files = ref<InputFile[]>([])
const error = ref('')
const notice = ref('')
const loading = ref(false)
const uploading = ref(false)
const percent = ref<number | null>(null)
const accept = computed(() => inputAccept(props.project.accepted_input_media_types))
let context: AbortController
let upload: AbortController | undefined
watch(() => props.project.id, async (id, _, onCleanup) => {
  const current = new AbortController()
  context = current
  onCleanup(() => { current.abort(); upload?.abort() })
  files.value = []
  error.value = notice.value = ''
  loading.value = true
  uploading.value = false
  try {
    const result = await api.listInputs(id, current.signal)
    if (!current.signal.aborted) {
      files.value = [...result, ...files.value.filter(file => !result.some(item => item.id === file.id))]
    }
  } catch (cause) { if (!current.signal.aborted) error.value = errorMessage(cause) }
  finally { if (!current.signal.aborted) loading.value = false }
}, { immediate: true })

async function choose(event: Event) {
  const picker = event.target as HTMLInputElement
  const selected = picker.files?.[0]
  picker.value = ''
  if (!selected || uploading.value) return
  const file = normalizeInputFile(selected, props.project.accepted_input_media_types)
  error.value = notice.value = ''
  if (!props.project.accepted_input_media_types.includes(file.type)) {
    error.value = `不支持此文件类型。此项目接受：${props.project.accepted_input_media_types.join('、')}。`
    return
  }
  const current = context
  upload = new AbortController()
  const controller = upload
  uploading.value = true
  percent.value = null
  try {
    const result = await uploadInput(props.project.id, file, {
      signal: controller.signal,
      onProgress: value => { if (!current.signal.aborted) percent.value = value },
    })
    if (!current.signal.aborted) {
      files.value = [...files.value.filter(item => item.id !== result.id), result]
      notice.value = '上传完成'
    }
  } catch (cause) {
    if (!current.signal.aborted) {
      if (controller.signal.aborted) notice.value = '已取消上传；如服务端已接收，可重新进入项目核对资料。'
      else error.value = errorMessage(cause)
    }
  } finally { if (!current.signal.aborted) uploading.value = false }
}
function size(bytes: number) {
  return bytes < 1024 ? `${bytes} B` : bytes < 1048576 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / 1048576).toFixed(1)} MB`
}
</script>

<template>
  <details class="input-files">
    <summary>项目资料 <span class="eyebrow">{{ files.length }} FILES · {{ project.profile_id }} / {{ project.profile_version }}</span></summary>
    <div class="input-content">
      <label class="file-label">添加资料<input type="file" :accept="accept" :disabled="uploading" @change="choose" /></label>
      <p class="input-hint">接受 {{ project.accepted_input_media_types.join('、') }}；最终校验以服务端为准。</p>
      <p v-if="loading" role="status">正在读取资料…</p>
      <div v-if="uploading" class="upload-status" role="status">
        <progress max="100" :value="percent ?? undefined" aria-label="上传进度" />
        <span>{{ percent === null ? '上传中…' : percent === 100 ? '已传输 100%，正在校验…' : `已传输 ${percent}%` }}</span>
        <button type="button" class="quiet-button" aria-label="取消上传" @click="upload?.abort()">取消</button>
      </div>
      <p v-if="error" role="alert" class="error-message">{{ error }}</p>
      <p v-if="notice" role="status">{{ notice }}</p>
      <ul class="file-list">
        <li v-for="file in files" :key="file.id">
          <div class="file-heading"><span>{{ file.display_name }}</span><span class="eyebrow">{{ size(file.size_bytes) }}</span></div>
          <code>SHA-256 {{ file.sha256_digest }}</code>
        </li>
      </ul>
      <p v-if="!loading && !files.length">暂无资料。可以先上传文件，再新建会话。</p>
    </div>
  </details>
</template>

<style scoped>
.input-files { margin-top: 8px; min-width: 0; font-size: 12px; }
summary { cursor: pointer; overflow-wrap: anywhere; }
summary .eyebrow { margin-left: 8px; }
.input-content { max-height: min(230px, 30dvh); overflow: auto; padding: 12px 0 4px; }
.file-label { display: grid; gap: 5px; }
input { max-width: 100%; width: 100%; font-size: 12px; }
.input-hint { margin: 7px 0; font-size: 11px; overflow-wrap: anywhere; }
.file-list { list-style: none; padding: 0; margin: 10px 0 0; }
li { border-top: 1px solid var(--line); padding: 8px 0; }
.file-heading { display: flex; justify-content: space-between; gap: 12px; }
.file-heading > :first-child { min-width: 0; overflow-wrap: anywhere; }
.file-heading .eyebrow { flex-shrink: 0; }
code { display: block; font-size: 9px; color: var(--ink-muted); }
.upload-status { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
progress { accent-color: var(--moss); max-width: 100%; width: 90px; }
</style>
