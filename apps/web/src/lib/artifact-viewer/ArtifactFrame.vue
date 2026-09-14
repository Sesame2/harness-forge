<script setup lang="ts">
import { computed } from 'vue'
import { artifactURL } from '../../app/config'
import type { Artifact } from '../api/types'

const props = defineProps<{ artifact: Artifact }>()
const url = computed(() => artifactURL(props.artifact.gateway_url))
</script>

<template>
  <div class="artifact-viewer">
    <p v-if="!url" role="alert" class="error-message">制品地址不受信任，请检查 Artifact Gateway 配置。</p>
    <img v-else-if="artifact.type === 'image'" :src="url.href" :alt="artifact.title" referrerpolicy="no-referrer">
    <iframe v-else :key="artifact.id" :src="url.href" :title="artifact.title" :sandbox="artifact.type === 'html' ? 'allow-scripts' : ''" referrerpolicy="no-referrer" />
  </div>
</template>

<style scoped>
.artifact-viewer { flex: 1; min-height: 360px; display: flex; background: white; }
iframe { width: 100%; flex: 1; min-height: 480px; border: 0; }
img { display: block; width: 100%; object-fit: contain; align-self: flex-start; }
.error-message { padding: 24px; }
</style>
