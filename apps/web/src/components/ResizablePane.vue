<script setup lang="ts">
import { onBeforeUnmount } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: number
  min: number
  max: number
  label: string
  paneId: string
  tabId?: string
  resizable?: boolean
}>(), { resizable: true })
const emit = defineEmits<{ 'update:modelValue': [width: number] }>()
let startX = 0
let startWidth = 0

function resize(width: number) {
  emit('update:modelValue', Math.min(props.max, Math.max(props.min, Math.round(width))))
}
function keydown(event: KeyboardEvent) {
  const values: Record<string, number> = {
    ArrowLeft: props.modelValue - 16, ArrowRight: props.modelValue + 16,
    Home: props.min, End: props.max,
  }
  if (values[event.key] === undefined) return
  event.preventDefault()
  resize(values[event.key]!)
}
function move(event: PointerEvent) { resize(startWidth + event.clientX - startX) }
function stop() {
  window.removeEventListener('pointermove', move)
  window.removeEventListener('pointerup', stop)
  window.removeEventListener('pointercancel', stop)
  window.removeEventListener('blur', stop)
}
function start(event: PointerEvent) {
  if (event.button !== 0) return
  event.preventDefault()
  ;(event.currentTarget as HTMLElement).focus()
  startX = event.clientX
  startWidth = props.modelValue
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', stop)
  window.addEventListener('pointercancel', stop)
  window.addEventListener('blur', stop)
}
onBeforeUnmount(stop)
</script>

<template>
  <div class="resizable-pane" :style="{ '--pane-width': `${modelValue}px` }">
    <section :id="paneId" class="pane-content" :role="tabId ? 'tabpanel' : undefined" :aria-labelledby="tabId" :tabindex="tabId ? 0 : undefined"><slot /></section>
    <div v-if="resizable !== false" class="pane-splitter" role="separator" tabindex="0"
      :aria-label="label" aria-orientation="vertical" :aria-controls="paneId"
      :aria-valuemin="min" :aria-valuemax="max" :aria-valuenow="modelValue"
      :aria-valuetext="`${modelValue} 像素`" @keydown="keydown" @pointerdown="start" />
  </div>
</template>
