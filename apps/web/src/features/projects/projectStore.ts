import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../../lib/api/client'
import type { Project } from '../../lib/api/types'

export const useProjectStore = defineStore('projects', () => {
  const items = ref<Project[]>([])
  const selected = ref<Project | null>(null)
  let selection = 0
  function remember(project: Project) {
    items.value = [...items.value.filter(item => item.id !== project.id), project]
  }
  async function list(signal: AbortSignal) {
    const result = await api.listProjects(signal)
    if (!signal.aborted) {
      // Preserve projects created while the list was in flight.
      items.value = [...result, ...items.value.filter(item => !result.some(project => project.id === item.id))]
    }
  }
  async function load(id: string, signal: AbortSignal) {
    const version = ++selection
    selected.value = null
    if (!id) return
    const project = await api.getProject(id, signal)
    if (!signal.aborted && version === selection) { selected.value = project; remember(project) }
  }
  async function create(name: string) {
    const project = await api.createProject(name.trim(), 'geo-analysis')
    remember(project)
    return project
  }
  return { items, selected, list, load, create }
})
