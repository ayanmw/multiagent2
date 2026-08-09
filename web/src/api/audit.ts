// 审计日志 API 封装（M3-02）：列表查询带分页 / 用户 / 决策 / 时间筛选。
// 后端契约见 server/internal/api/audit.go。owner 隔离由后端强制：
// viewer 仅看自己（忽略 user_id）；admin/developer 看全员（可经 user_id 收敛）。
import { request } from './client'

// 审计日志记录（对应 model.AuditLog，扩展索引以便字段演进）。
export interface AuditLog {
  id: number
  user_id: number
  command: string
  workdir: string
  decision: string // allow / deny / ask（来自 executor.Decision）
  reason: string
  allowed: boolean
  note: string
  created_at: string
  [key: string]: unknown
}

// 决策筛选枚举（all 表示不按决策过滤，其余对应后端 decision 取值）。
export type AuditDecisionFilter = 'all' | 'allow' | 'deny' | 'ask'

// 审计列表查询参数（前端视角）：时间范围以毫秒时间戳传参（后端兼容 ms / 字符串）。
export interface AuditListParams {
  user_id?: number // 仅 admin/developer 生效，viewer 被后端忽略
  decision?: AuditDecisionFilter
  command?: string
  start?: number // 毫秒时间戳
  end?: number // 毫秒时间戳
  limit?: number
  offset?: number
}

// GET /api/audit 的响应体（M3-02 增加 limit/offset/scope 元信息）。
export interface AuditListResult {
  audit_logs: AuditLog[]
  total: number
  limit: number
  offset: number
  scope: 'all' | 'self' // 后端实际生效的可见范围，供前端提示
}

export async function listAuditLogs(params: AuditListParams = {}): Promise<AuditListResult> {
  const query = new URLSearchParams()
  if (params.user_id !== undefined && params.user_id > 0) {
    query.set('user_id', String(params.user_id))
  }
  if (params.decision && params.decision !== 'all') {
    query.set('decision', params.decision)
  }
  if (params.command && params.command.trim()) {
    query.set('command', params.command.trim())
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
  return request<AuditListResult>(`/audit${qs ? `?${qs}` : ''}`)
}
