import { defineStore } from 'pinia'

export const paneLimits = {
  sidebar: { min: 200, max: 320, initial: 240 },
  chat: { min: 320, max: 640, initial: 440 },
} as const
type Pane = keyof typeof paneLimits
const storageKey = 'harness-forge:pane-widths'

function clamp(pane: Pane, value: unknown) {
  const { min, max, initial } = paneLimits[pane]
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.round(Math.min(max, Math.max(min, value))) : initial
}

function readWidths() {
  try {
    const stored = JSON.parse(localStorage.getItem(storageKey) ?? '{}')
    return { sidebar: clamp('sidebar', stored?.sidebar), chat: clamp('chat', stored?.chat) }
  } catch {
    return { sidebar: 240, chat: 440 }
  }
}

export const useWorkbenchStore = defineStore('workbench', {
  state: () => ({ widths: readWidths() }),
  actions: {
    setWidth(pane: Pane, width: number) {
      this.widths[pane] = clamp(pane, width)
      try {
        localStorage.setItem(storageKey, JSON.stringify(this.widths))
      } catch {
        // Browser preferences are optional: blocked/full storage must not interrupt work.
      }
    },
  },
})
