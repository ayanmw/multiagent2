// 对话相关 API：已启用模型（供 Model 选择）+ 基于 fetch 的 AG-UI SSE 流式消费。
// 注意：SSE 端点需要鉴权头（Authorization: Bearer），原生 EventSource 无法自定义
// 请求头，因此这里用 fetch + ReadableStream 手动解析 AG-UI 的 SSE 帧（data: {...}\n\n）。
import { request, getToken } from './client'

// 已启用模型（Agent 只可选已启用），来自 GET /api/models。
export interface EnabledModel {
  id: number
  provider_id: number
  provider_name: string
  protocol: string
  model_id: string
  name: string
  is_default: boolean
  enabled: boolean
}

// 拉取当前用户所有「已启用」模型，作为对话工作台 Model 选择器的数据源。
export async function listEnabledModels(): Promise<EnabledModel[]> {
  const data = await request<{ models: EnabledModel[]; total_enabled: number }>('/models')
  return data.models ?? []
}

// AG-UI SSE 事件类型（前端关心的子集，详见 server/internal/api/sse.go 的 aguiConverter）。
export type AGUIEventType =
  | 'RUN_STARTED'
  | 'TEXT_MESSAGE_CONTENT'
  | 'TOOL_CALL_START'
  | 'TOOL_CALL_ARGS'
  | 'TOOL_CALL_END'
  | 'RUN_FINISHED'
  | 'RUN_ERROR'

export interface AGUIEvent {
  type: AGUIEventType
  messageId?: string
  delta?: string
  threadId?: string
  runId?: string
  toolCallId?: string
  toolCallName?: string
  parentMessageId?: string
  message?: string
  [key: string]: unknown
}

export interface StreamOptions {
  // 会话标识（session_key）；空串由服务端新建（本项目在调用前已显式建会话）。
  sessionKey: string
  message: string
  // 指定托管模型 id（可选）；缺省后端取默认启用模型。
  modelId?: number
  // 取消信号，用于「停止生成」。
  signal?: AbortSignal
  // 每收到一条 AG-UI 事件即回调。
  onEvent: (ev: AGUIEvent) => void
}

// 消费一次流式对话：以 fetch（POST body 携带 message，M0.5-06 起不再走 GET query）
// 拉取 SSE，逐帧解析并回调 onEvent。异常时抛出 Error（含后端返回的错误信息）。
export async function streamChat(opts: StreamOptions): Promise<void> {
  const url = `/api/chat/${encodeURIComponent(opts.sessionKey)}/stream`
  const token = getToken()
  const body: Record<string, unknown> = { message: opts.message }
  if (opts.modelId != null) {
    body.model_id = opts.modelId
  }
  const res = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
    signal: opts.signal,
  })

  if (!res.ok || !res.body) {
    let msg = `请求失败 (${res.status})`
    try {
      const text = await res.text()
      if (text) {
        const j = JSON.parse(text) as { error?: string }
        if (j.error) msg = j.error
      }
    } catch {
      /* 忽略解析失败，用默认错误信息 */
    }
    throw new Error(msg)
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''

  // 解析单个 SSE 帧（可能含多行，仅处理 data: 前缀）。
  const handleFrame = (frame: string) => {
    for (const line of frame.split('\n')) {
      const trimmed = line.trim()
      if (!trimmed.startsWith('data:')) continue
      const payload = trimmed.slice(5).trim()
      if (!payload) continue
      try {
        opts.onEvent(JSON.parse(payload) as AGUIEvent)
      } catch {
        /* 非 JSON 帧（如心跳注释）忽略 */
      }
    }
  }

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    // SSE 帧以空行 (\n\n) 分隔。
    let idx: number
    while ((idx = buf.indexOf('\n\n')) !== -1) {
      const frame = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      handleFrame(frame)
    }
  }
  // flush 剩余缓冲（可能没有尾随空行）。
  if (buf.trim()) handleFrame(buf)
}
