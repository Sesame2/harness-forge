<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ProjectSwitcher from './features/projects/ProjectSwitcher.vue'
import InputFiles from './features/projects/InputFiles.vue'
import ConversationSidebar from './features/conversations/ConversationSidebar.vue'
import ChatPanel from './features/chat/ChatPanel.vue'
import { useProjectStore } from './features/projects/projectStore'
import { useConversationStore } from './features/conversations/conversationStore'
import { ApiError, errorMessage } from './lib/api/client'

const route = useRoute()
const projects = useProjectStore()
const conversations = useConversationStore()
const loading = ref(false)
const routeError = ref('')
watch([() => route.params.projectId, () => route.params.conversationId], async ([projectId, conversationId], _, onCleanup) => {
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  routeError.value = ''
  loading.value = Boolean(projectId)
  conversations.clear()
  try {
    // Inputs belong to the project; conversation navigation must not unmount an active upload.
    if (projects.selected?.id !== projectId) await projects.load(String(projectId || ''), controller.signal)
    if (controller.signal.aborted || !projectId) return
    await conversations.load(String(projectId), String(conversationId || ''), controller.signal)
  } catch (cause) {
    if (!controller.signal.aborted) routeError.value = cause instanceof ApiError && cause.status === 400
      ? '项目或会话链接无效，请返回项目重新选择。' : errorMessage(cause)
  }
  finally { if (!controller.signal.aborted) loading.value = false }
}, { immediate: true })
</script>

<template>
  <RouterView v-slot="{ Component }">
    <component :is="Component">
      <template #project>
        <ProjectSwitcher />
        <InputFiles v-if="projects.selected" :key="projects.selected.id" :project="projects.selected" />
      </template>
      <template #sidebar="{ projectId, conversationId }">
        <ConversationSidebar v-if="projects.selected && !loading && !routeError" :key="projectId" :project-id="projectId" :conversation-id="conversationId" />
        <p v-else class="sidebar-empty">{{ loading ? '正在读取项目…' : routeError ? '请检查项目或会话链接。' : '先创建或选择项目，再建立会话。' }}</p>
      </template>
      <template v-if="loading || routeError || conversations.selected" #chat>
        <ChatPanel v-if="!loading && !routeError && conversations.selected" :key="conversations.selected.id" :conversation-id="conversations.selected.id" />
        <div v-else class="chat-empty">
          <p v-if="loading" role="status">正在读取工作空间…</p>
          <template v-else>
            <p role="alert" class="error-message">{{ routeError }}</p>
            <RouterLink :to="projects.selected ? { name: 'project', params: { projectId: projects.selected.id } } : '/'">{{ projects.selected ? '返回项目' : '返回项目首页' }}</RouterLink>
          </template>
        </div>
      </template>
    </component>
  </RouterView>
</template>
