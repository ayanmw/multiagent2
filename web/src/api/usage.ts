// Token/费用计量 API 封装（M3-03）：列表查询带分页 / 用户 / provider / model / 会话 / 时间筛选，
// 同时返回过滤范围内的 token 累计（totals）。后端契约见 server/internal/api/usage.go。
// owner 隔离由后端强制：viewer 仅看自己（忽略 user_id）；admin/developer 看全员（可经 user_id 收敛）。
import { request } from './client'

// 单条用量记录（对应 model.UsageRecord）。
export interface UsageRecord {
  id: number
  user_id: number
  session_id: number
  session_key: string
  provider_id: number
  model_id: number
  model_name: string
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  estimated: boolean // true=上游未给 usage，本地估算
  created_at: string
  [key: string]: unknown
}

// 用量查询参数（前端视角）。
export interface UsageListParams {
  user_id?: number // 仅 admin/developer 生效，viewer 被后端忽略
  provider_id?: number
  model_id?: number
  session_key?: string
  start?: number // 毫秒时间戳
  end?: number // 毫秒时间戳
  limit?: number
  offset?: number
}

// 累计汇总（过滤范围内）。
export interface UsageTotals {
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  records: number
}

// GET /api/usage 的响应体（M3-03）。
export interface UsageListResult {
  usage_records: UsageRecord[]
  total: number
  totals: UsageTotals
  limit: number
  offset: number
  scope: 'all' | 'self' // 后端实际生效的可见范围，供前端提示
}

export async function listUsage(params: UsageListParams = {}): Promise<UsageListResult> {
  const query = new URLSearchParams()
  if (params.user_id !== undefined && params.user_id > 0) {
    query.set('user_id', String(params.user_id))
  }
  if (params.provider_id !== undefined && params.provider_id > 0) {
    query.set('provider_id', String(params.provider_id))
  }
  if (params.model_id !== undefined && params.model_id > 0) {
    query.set('model_id', String(params.model_id))
  }
  if (params.session_key && params.session_key.trim()) {
    query.set('session_key', params.session_key.trim())
  }
  if (params.start !== undefined) {
    query.set('start', String(params.start))
  }
  if (params.end !== undefined) {
    query.set('end', String(params.end))
  }
  if (params.limit !== undefined) {
    query.set('limit', String(params.limit))
  }
  if (params.offset !== undefined) {
    query.set('offset', String(params.offset))
  }
  const qs = query.toString()
  return request<UsageListResult>(`/usage${qs ? `?${qs}` : ''}`)
}
