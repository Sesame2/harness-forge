<script setup lang="ts">
import type { Artifact } from '../../lib/api/types'
defineProps<{ artifacts: Artifact[]; selectedId?: string }>()
defineEmits<{ select: [id: string] }>()
</script>

<template>
  <nav class="artifact-tabs" aria-label="本次运行的制品">
    <button v-for="artifact in artifacts" :key="artifact.id" type="button" :data-artifact-id="artifact.id"
      :aria-pressed="selectedId === artifact.id" @click="$emit('select', artifact.id)">
      <span class="eyebrow">{{ artifact.type }}</span> {{ artifact.title }}<span v-if="artifact.is_primary" class="primary-mark">主制品</span>
    </button>
  </nav>
</template>

<style scoped>
.artifact-tabs { display: flex; flex-wrap: wrap; gap: 6px; padding: 12px 18px; border-bottom: 1px solid var(--line); }
button { padding: 8px 10px; border: 1px solid transparent; background: transparent; font-size: 12px; text-align: left; overflow-wrap: anywhere; }
button[aria-pressed="true"] { background: var(--moss-light); border-color: var(--moss); }
button:hover { border-color: var(--line); }
.eyebrow { font-size: 9px; }
.primary-mark { margin-left: 7px; color: var(--moss); font-size: 10px; }
</style>
