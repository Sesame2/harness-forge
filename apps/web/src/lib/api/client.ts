import type { Conversation, ErrorEnvelope, InputFile, Project } from './types'

export class ApiError extends Error implements ErrorEnvelope {
  status: number
  code: string
  details: Record<string, unknown> | null
  request_id: string

  constructor(status: number, value?: Partial<ErrorEnvelope>) {
    super(value?.message || `请求失败（HTTP ${status}）`)
    this.name = 'ApiError'
    this.status = status
    this.code = value?.code || 'invalid_response'
    this.details = value?.details ?? null
    this.request_id = value?.request_id || ''
  }
}

export function decodeResponse<T>(status: number, text: string): T {
  if (status === 204) return undefined as T
  let value: unknown
  try { value = JSON.parse(text) } catch { throw new ApiError(status) }
  if (status < 200 || status >= 300) {
    throw new ApiError(status, value && typeof value === 'object' ? value as Partial<ErrorEnvelope> : undefined)
  }
  return value as T
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) return '存在资源冲突，请先取消或等待当前 Run 结束，再重试。'
  if (error instanceof ApiError && (error.status === 404 || error.status === 400)) {
    return error.status === 404 ? '项目或会话不存在，可能已被删除。' : error.message
  }
  return error instanceof Error ? error.message : '请求失败，请重试。'
}

async function request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    method, signal,
    ...(body === undefined ? {} : { body: JSON.stringify(body), headers: { 'Content-Type': 'application/json' } }),
  })
  return decodeResponse<T>(response.status, await response.text())
}

const projectPath = (id: string) => `/projects/${encodeURIComponent(id)}`
const conversationPath = (id: string) => `/conversations/${encodeURIComponent(id)}`
export const inputPath = (id: string) => `${projectPath(id)}/inputs`

export const api = {
  listProjects: (signal?: AbortSignal) => request<Project[]>('/projects', 'GET', undefined, signal),
  getProject: (id: string, signal?: AbortSignal) => request<Project>(projectPath(id), 'GET', undefined, signal),
  createProject: (name: string, profileId: string, signal?: AbortSignal) => request<Project>('/projects', 'POST', { name, profile_id: profileId }, signal),
  listInputs: (id: string, signal?: AbortSignal) => request<InputFile[]>(inputPath(id), 'GET', undefined, signal),
  listConversations: (id: string, signal?: AbortSignal) => request<Conversation[]>(`${projectPath(id)}/conversations`, 'GET', undefined, signal),
  getConversation: (id: string, signal?: AbortSignal) => request<Conversation>(conversationPath(id), 'GET', undefined, signal),
  createConversation: (id: string, signal?: AbortSignal) => request<Conversation>(`${projectPath(id)}/conversations`, 'POST', {}, signal),
  renameConversation: (id: string, title: string, signal?: AbortSignal) => request<Conversation>(conversationPath(id), 'PATCH', { title }, signal),
  deleteConversation: (id: string, signal?: AbortSignal) => request<void>(conversationPath(id), 'DELETE', undefined, signal),
}
