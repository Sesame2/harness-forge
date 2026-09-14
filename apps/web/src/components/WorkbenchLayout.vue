<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { paneLimits, useWorkbenchStore } from '../app/store'
import ResizablePane from './ResizablePane.vue'

const route = useRoute()
const router = useRouter()
const store = useWorkbenchStore()
const projectId = computed(() => String(route.params.projectId ?? ''))
const conversationId = computed(() => String(route.params.conversationId ?? ''))
const selectedArtifact = computed(() => typeof route.query.artifact === 'string' ? route.query.artifact : null)
const context = computed(() => ({ projectId: projectId.value, conversationId: conversationId.value }))
const collapsed = ref(false)
const tabs = [{ id: 'sidebar', label: '会话' }, { id: 'chat', label: '对话' }, { id: 'artifact', label: '制品' }] as const
type Tab = typeof tabs[number]['id']
const activeTab = ref<Tab>('chat')
const media = window.matchMedia('(max-width: 999px)')
const narrow = ref(media.matches)
const viewportWidth = ref(window.innerWidth)
const chatMax = computed(() => Math.max(paneLimits.chat.min, Math.min(paneLimits.chat.max,
  viewportWidth.value - (collapsed.value ? 0 : store.widths.sidebar + 8) - 340 - 8)))
const chatWidth = computed(() => Math.min(store.widths.chat, chatMax.value))
function viewportChanged() {
  narrow.value = media.matches
  viewportWidth.value = window.innerWidth
}
media.addEventListener('change', viewportChanged)
window.addEventListener('resize', viewportChanged)
onBeforeUnmount(() => {
  media.removeEventListener('change', viewportChanged)
  window.removeEventListener('resize', viewportChanged)
})
function visible(tab: Tab) {
  return narrow.value ? activeTab.value === tab : tab !== 'sidebar' || !collapsed.value
}
function selectArtifact(id: string | null) {
  return router.replace({ query: { ...route.query, artifact: id || undefined } })
}
async function switchTab(event: KeyboardEvent, index: number) {
  const next = { ArrowRight: (index + 1) % 3, ArrowLeft: (index + 2) % 3, Home: 0, End: 2 }[event.key]
  if (next === undefined) return
  event.preventDefault()
  activeTab.value = tabs[next]!.id
  await nextTick()
  document.getElementById(`${activeTab.value}-tab`)?.focus()
}
</script>

<template>
  <div class="workbench" :class="{ 'is-narrow': narrow }">
    <header class="workbench-header">
      <RouterLink to="/" class="brand" aria-label="Harness Forge 首页">
        <span class="brand-mark" aria-hidden="true">⌖</span>
        <span>Harness Forge<small>空间分析工作台</small></span>
      </RouterLink>
      <div class="project-context">
        <slot name="project" v-bind="context">
          <span class="eyebrow">PROJECT / 项目</span>
          <span class="context-name">{{ projectId || '尚未选择项目' }}</span>
        </slot>
      </div>
      <button v-if="!narrow" type="button" class="quiet-button collapse-toggle"
        :aria-label="collapsed ? '展开会话栏' : '折叠会话栏'" :aria-expanded="!collapsed"
        aria-controls="sidebar-pane" @click="collapsed = !collapsed">
        <span aria-hidden="true">{{ collapsed ? '⇥' : '⇤' }}</span> 会话栏
      </button>
    </header>

    <div v-if="narrow" class="mobile-tabs" role="tablist" aria-label="工作区面板">
      <button v-for="(tab, index) in tabs" :id="`${tab.id}-tab`" :key="tab.id" type="button"
        role="tab" :aria-selected="activeTab === tab.id" :aria-controls="`${tab.id}-pane`"
        :tabindex="activeTab === tab.id ? 0 : -1" @click="activeTab = tab.id" @keydown="switchTab($event, index)">
        {{ tab.label }}
      </button>
    </div>

    <main class="workbench-panes" aria-label="项目工作区">
      <ResizablePane v-show="visible('sidebar')" class="sidebar-pane" :model-value="store.widths.sidebar"
        :min="paneLimits.sidebar.min" :max="paneLimits.sidebar.max" pane-id="sidebar-pane"
        label="会话栏宽度" :resizable="!narrow" :tab-id="narrow ? 'sidebar-tab' : undefined" @update:model-value="store.setWidth('sidebar', $event)">
        <div class="panel-inner">
          <div class="panel-heading"><h2>会话</h2><span class="eyebrow">01 / SESSIONS</span></div>
          <slot name="sidebar" v-bind="context">
            <div class="sidebar-empty"><span class="index-mark" aria-hidden="true">—</span>
              <h3>{{ projectId ? '这里收纳项目会话' : '从项目开始' }}</h3>
              <p>{{ projectId ? '项目资料与会话将保留在同一个工作空间。' : '先创建或选择项目，再围绕资料展开会话。' }}</p>
            </div>
            <p class="panel-footnote">项目与会话管理尚未接入</p>
          </slot>
        </div>
      </ResizablePane>

      <ResizablePane v-show="visible('chat')" class="chat-pane" :model-value="chatWidth"
        :min="paneLimits.chat.min" :max="chatMax" pane-id="chat-pane" label="对话栏宽度"
        :resizable="!narrow" :tab-id="narrow ? 'chat-tab' : undefined" @update:model-value="store.setWidth('chat', $event)">
        <div class="panel-inner">
          <div class="panel-heading"><h2>对话</h2><span class="eyebrow">02 / DIALOGUE</span></div>
          <slot name="chat" v-bind="context">
            <div class="chat-empty">
              <span class="section-kicker">{{ !projectId ? '起点 / START HERE' : conversationId ? '会话 / READY TO EXPLORE' : '项目 / WORKSPACE' }}</span>
              <h1>{{ !projectId ? '先建立一个项目' : conversationId ? '从一个问题开始' : '选择或新建会话' }}</h1>
              <p>{{ !projectId ? '将数据、问题和分析制品归于一处。使用顶部的新建项目开始探索。' : conversationId ? '围绕项目资料描述问题。对话能力接入后，分析过程将在这里展开。' : '展开顶部的项目资料即可上传文件，再到会话栏为新的分析建立会话。' }}</p>
              <ol v-if="!projectId" class="onboarding-steps">
                <li><span>01</span><div><h3>建立项目</h3><p>为资料与分析确定一个空间</p></div></li>
                <li><span>02</span><div><h3>添加资料</h3><p>整理本次分析需要的数据</p></div></li>
                <li><span>03</span><div><h3>展开对话</h3><p>从问题走向可检查的制品</p></div></li>
              </ol>
              <div v-else class="context-card"><span class="eyebrow">{{ conversationId ? 'CONVERSATION' : 'PROJECT' }}</span><code>{{ conversationId || projectId }}</code></div>
            </div>
            <div class="composer-placeholder"><span class="status-dot" aria-hidden="true" />{{ conversationId ? '聊天功能尚未接入' : '资料与会话就绪后，在此展开分析' }}<span class="eyebrow">WORKSPACE / V0</span></div>
          </slot>
        </div>
      </ResizablePane>

      <section v-show="visible('artifact')" id="artifact-pane" class="artifact-pane" aria-label="制品"
        :role="narrow ? 'tabpanel' : 'region'" :aria-labelledby="narrow ? 'artifact-tab' : undefined" :tabindex="narrow ? 0 : undefined">
        <div class="panel-heading"><h2>制品</h2><span class="eyebrow">03 / ARTIFACTS</span></div>
        <slot name="artifact" v-bind="context" :selected-artifact="selectedArtifact" :select-artifact="selectArtifact">
          <div class="artifact-canvas">
            <span class="canvas-coordinate top-coordinate" aria-hidden="true">N ↑ &nbsp; / &nbsp; WORKSPACE</span>
            <div class="artifact-empty"><div class="sheet-symbol" aria-hidden="true"><span /> <span /> <span /></div>
              <span class="section-kicker">成果预览 / OUTPUT</span>
              <h2>{{ selectedArtifact ? '已定位制品' : '制品将在这里展开' }}</h2>
              <p>{{ selectedArtifact ? '链接中的制品选择已保留，预览功能尚未接入。' : '地图、表格与报告将集中呈现。当前尚未加载制品。' }}</p>
              <code v-if="selectedArtifact" class="artifact-id">{{ selectedArtifact }}</code>
            </div>
            <span class="canvas-coordinate bottom-coordinate" aria-hidden="true">— &nbsp; — &nbsp; — &nbsp; 视图待载入</span>
          </div>
        </slot>
      </section>
    </main>
    <footer class="workbench-footer"><span>HARNESS FORGE <span class="footer-divider">/</span> 分析工作空间</span><span>项目 · 对话 · 制品</span></footer>
  </div>
</template>
