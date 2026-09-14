import { createRouter, createWebHistory, type RouterHistory } from 'vue-router'
import WorkbenchLayout from '../components/WorkbenchLayout.vue'

export function createWorkbenchRouter(history: RouterHistory = createWebHistory()) {
  return createRouter({
    history,
    routes: [
      { path: '/', name: 'onboarding', component: WorkbenchLayout },
      { path: '/projects/:projectId', name: 'project', component: WorkbenchLayout },
      { path: '/projects/:projectId/conversations/:conversationId', name: 'conversation', component: WorkbenchLayout },
      { path: '/:pathMatch(.*)*', redirect: '/' },
    ],
  })
}
