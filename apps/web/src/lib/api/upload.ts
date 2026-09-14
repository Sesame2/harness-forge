import { decodeResponse, inputPath } from './client'
import type { InputFile } from './types'

// Browsers can leave these MIME types blank; never infer arbitrary JSON or override a known type.
const extensions: Record<string, string> = { csv: 'text/csv', geojson: 'application/geo+json' }
export function normalizeInputFile(file: File, accepted: string[]): File {
  const type = extensions[file.name.split('.').pop()!.toLowerCase()]
  return !file.type && type && accepted.includes(type)
    ? new File([file], file.name, { type, lastModified: file.lastModified }) : file
}
export function inputAccept(accepted: string[]): string {
  return [...accepted, ...Object.entries(extensions).filter(([, type]) => accepted.includes(type)).map(([ext]) => `.${ext}`)].join(',')
}

// A small structural adapter keeps the native XHR transport injectable without a second implementation.
export interface UploadXHR {
  upload: Pick<XMLHttpRequestUpload, 'addEventListener' | 'removeEventListener'>
  status: number
  responseText: string
  onload: (() => void) | null
  onerror: (() => void) | null
  onabort: (() => void) | null
  ontimeout: (() => void) | null
  open(method: string, url: string): void
  send(body: FormData): void
  abort(): void
}
export function uploadInput(projectId: string, file: File, options: {
  signal?: AbortSignal
  onProgress?: (percent: number | null) => void
  createXHR?: () => UploadXHR
} = {}): Promise<InputFile> {
  return new Promise((resolve, reject) => {
    const { signal, onProgress } = options
    if (signal?.aborted) { reject(new DOMException('已取消上传', 'AbortError')); return }
    const xhr = options.createXHR?.() ?? new XMLHttpRequest()
    const progress = (event: ProgressEvent) => onProgress?.(event.lengthComputable && event.total > 0
      ? Math.round(event.loaded / event.total * 100) : null)
    const abort = () => xhr.abort()
    const cleanup = () => {
      signal?.removeEventListener('abort', abort)
      xhr.upload.removeEventListener('progress', progress)
      xhr.onload = xhr.onerror = xhr.onabort = xhr.ontimeout = null
    }
    xhr.onload = () => {
      cleanup()
      try { resolve(decodeResponse<InputFile>(xhr.status, xhr.responseText)) } catch (error) { reject(error) }
    }
    xhr.onerror = xhr.ontimeout = () => { cleanup(); reject(new Error('网络异常，上传失败，请重试。')) }
    xhr.onabort = () => { cleanup(); reject(new DOMException('已取消上传', 'AbortError')) }
    xhr.upload.addEventListener('progress', progress)
    signal?.addEventListener('abort', abort, { once: true })
    try {
      xhr.open('POST', `/api/v1${inputPath(projectId)}`)
      const body = new FormData()
      body.append('file', file, file.name)
      xhr.send(body)
    } catch (error) { cleanup(); reject(error) }
  })
}
