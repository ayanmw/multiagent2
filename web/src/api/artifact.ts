// Artifact 浏览器 API 封装（M3-06）：列出 / 查看 / 下载某会话作用域下的全部产物。
// 后端契约见 server/internal/api/artifact.go。
//  - GET /api/sessions/:id/artifacts              列出该会话全部 artifact（元信息）
//  - GET /api/sessions/:id/artifacts/:name         查看内容（>256KiB 截断，二进制不内联）
//  - GET /api/sessions/:id/artifacts/:name?download=1  以附件形式下载原始字节
// owner 隔离由后端强制：只能浏览自己会话下的产物（跨用户返回 404）。
import { request, getToken, ApiError } from './client'

// 单个 artifact 的元信息（列表项）。
export interface ArtifactEntry {
  name: string
  size: number
  modified_at: string // RFC3339，可能为空
  is_state: boolean // 是否为 M1-16 三种核心状态文件之一
}

// 列表响应体。
export interface ArtifactListResult {
  session_key: string
  enabled: boolean // 状态外置是否启用（未启用时 artifacts 恒为空）
  total: number
  artifacts: ArtifactEntry[]
}

// 查看（内联）响应体。
export interface ArtifactContent {
  session_key: string
  name: string
  size: number
  is_state: boolean
  binary: boolean // 二进制产物不内联，需走下载
  truncated: boolean // 内容超过 256KiB 被截断
  content: string
}

// GET /api/sessions/:id/artifacts —— 列出某会话的全部产物。
export async function listArtifacts(sessionKey: string): Promise<ArtifactListResult> {
  return request<ArtifactListResult>(`/sessions/${encodeURIComponent(sessionKey)}/artifacts`)
}

// GET /api/sessions/:id/artifacts/:name —— 查看内容（内联 JSON）。
export async function getArtifact(sessionKey: string, name: string): Promise<ArtifactContent> {
  return request<ArtifactContent>(
    `/sessions/${encodeURIComponent(sessionKey)}/artifacts/${encodeURIComponent(name)}`,
  )
}

// GET /api/sessions/:id/artifacts/:name?download=1 —— 以附件形式下载原始字节。
// 由于返回的是二进制流而非 JSON，这里用原生 fetch 取 blob 触发浏览器下载，
// 绕开 request() 的 JSON 解析。
export async function downloadArtifact(sessionKey: string, name: string): Promise<void> {
  const token = getToken()
  const url = `/api/sessions/${encodeURIComponent(sessionKey)}/artifacts/${encodeURIComponent(name)}?download=1`
  const res = await fetch(url, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) {
    let msg = `下载失败 (${res.status})`
    try {
      const t = await res.text()
      const data = t ? JSON.parse(t) : null
      if (data && typeof data === 'object' && 'error' in data) {
        msg = String((data as Record<string, unknown>).error)
      }
    } catch {
      /* 忽略解析错误，沿用状态码提示 */
    }
    throw new ApiError(res.status, msg)
  }
  const blob = await res.blob()
  const objectUrl = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objectUrl
  a.download = name
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(objectUrl)
}
