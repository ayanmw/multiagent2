// 自动化管理 API 封装（M4-08 自动化管理前端）。
// 后端契约见 server/internal/api/automation.go（M4-01 CRUD）+ 新增运行历史端点（M4-08）。
// owner 隔离由后端强制：每个用户只能看到/管理自己名下的自动化；viewer 仅 read，写操作被后端 RBAC 拒绝。
import { request } from './client'

// 触发方式（对应 model.AutomationTriggerType）。
export type AutomationTriggerType = 'cron' | 'webhook'

// 单条自动化配置（对应后端 automationView）。
export interface Automation {
  id: number
  user_id: number
  name: string
  trigger_type: AutomationTriggerType
  cron_expr: string
  goal_prompt: string
  enabled: boolean
  last_run: string | null
  next_run: string | null
  created_at: string
  updated_at: string
  [key: string]: unknown
}

// 创建/更新请求体（对应 automationRequest / automationUpdateRequest）。
export interface AutomationPayload {
  name: string
  trigger_type: AutomationTriggerType
  cron_expr?: string
  goal_prompt: string
  enabled?: boolean
}

export interface AutomationListResult {
  automations: Automation[]
  total: number
}

// 运行历史记录（对应后端 automationRunView / model.AutomationRun）。
export interface AutomationRun {
  id: number
  automation_id: number
  session_key: string
  channel: string // cron / webhook / recover
  status: 'running' | 'done' | 'failed'
  error: string
  attempts: number
  created_at: string
  updated_at: string
  [key: string]: unknown
}

export interface AutomationRunListResult {
  runs: AutomationRun[]
  total: number
  automation_id: number
}

// GET /api/automations —— 当前用户归属的自动化列表。
export async function listAutomations(): Promise<AutomationListResult> {
  return request<AutomationListResult>('/automations')
}

// POST /api/automations —— 新建自动化（需 automations:write）。webhook 触发器由后端自动生成令牌。
export async function createAutomation(payload: AutomationPayload): Promise<Automation> {
  return request<Automation>('/automations', { method: 'POST', body: payload })
}

// PUT /api/automations/:id —— 部分更新（需 automations:write，owner-scoped）。
export async function updateAutomation(id: number, payload: Partial<AutomationPayload>): Promise<Automation> {
  return request<Automation>(`/automations/${id}`, { method: 'PUT', body: payload })
}

// DELETE /api/automations/:id —— 删除（需 automations:write，owner-scoped）。
export async function deleteAutomation(id: number): Promise<void> {
  await request<void>(`/automations/${id}`, { method: 'DELETE' })
}

// GET /api/automations/:id/runs —— 运行历史（需 automations:read，owner-scoped）。
export async function listAutomationRuns(id: number): Promise<AutomationRunListResult> {
  return request<AutomationRunListResult>(`/automations/${id}/runs`)
}

// 触发类型 → 中文标签（用于表格/表单着色）。
export function triggerTypeLabel(t: AutomationTriggerType): string {
  switch (t) {
    case 'cron':
      return '定时'
    case 'webhook':
      return '事件'
    default:
      return t || '-'
  }
}

// 运行渠道 → 中文标签。
export function runChannelLabel(c: string): string {
  switch (c) {
    case 'cron':
      return '定时调度'
    case 'webhook':
      return 'Webhook'
    case 'recover':
      return '跨天恢复'
    default:
      return c || '-'
  }
}

// 运行状态 → 中文标签。
export function runStatusLabel(s: AutomationRun['status']): string {
  switch (s) {
    case 'running':
      return '运行中'
    case 'done':
      return '完成'
    case 'failed':
      return '失败'
    default:
      return s || '-'
  }
}
