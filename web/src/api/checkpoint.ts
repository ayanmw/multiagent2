// 人工检查点 API 封装（M3-05 human-in-the-loop）。
// 无人值守下命中 ask 级危险命令时，后端不再直接 deny，而是落库为一条 checkpoint 并暂停；
// 人在此处 approve（后端按记录的工作目录实际执行并回填结果）或 reject（命令永不执行）。
// 后端契约见 server/internal/api/checkpoint.go。owner 隔离由后端强制：
// viewer 仅看/处置自己的检查点；admin/developer 可见全员。
import { request } from './client'

// 检查点状态（对应 model.CheckpointPending/Approved/Rejected）。
export type CheckpointStatus = 'pending' | 'approved' | 'rejected'

// 检查点记录（对应 model.Checkpoint，扩展索引以便字段演进）。
export interface Checkpoint {
  ID: number
  CreatedAt: string
  UpdatedAt: string
  session_id: string
  user_id: number
  command: string
  workdir: string
  reason: string
  context: string
  status: CheckpointStatus
  comment: string
  resolved_by: number
  result: string
  [key: string]: unknown
}

// 状态筛选（all 表示不过滤）。
export type CheckpointStatusFilter = 'all' | CheckpointStatus

export interface CheckpointListParams {
  status?: CheckpointStatusFilter
  limit?: number
  offset?: number
}

export interface CheckpointListResult {
  checkpoints: Checkpoint[]
  total: number
  limit: number
  offset: number
  scope: 'all' | 'self' // 后端实际生效的可见范围，供前端提示
}

export interface ResolveCheckpointResult {
  ok: boolean
  status: CheckpointStatus
  display_id: string
  result?: string // approve 时为命令实际执行结果（exit_code + stdout/stderr）
}

// GET /api/checkpoints
export async function listCheckpoints(
  params: CheckpointListParams = {},
): Promise<CheckpointListResult> {
  const query = new URLSearchParams()
  if (params.status && params.status !== 'all') {
    query.set('status', params.status)
  }
  if (params.limit !== undefined) {
    query.set('limit', String(params.limit))
  }
  if (params.offset !== undefined) {
    query.set('offset', String(params.offset))
  }
  const qs = query.toString()
  return request<CheckpointListResult>(`/checkpoints${qs ? `?${qs}` : ''}`)
}

// POST /api/checkpoints/:id/resolve —— approve 放行执行，reject 中止。
export async function resolveCheckpoint(
  id: number,
  action: 'approve' | 'reject',
  comment = '',
): Promise<ResolveCheckpointResult> {
  return request<ResolveCheckpointResult>(`/checkpoints/${id}/resolve`, {
    method: 'POST',
    body: { action, comment },
  })
}

// 展示编号：与后端 model.Checkpoint.DisplayID() 保持一致。
export function checkpointDisplayID(cp: Pick<Checkpoint, 'ID'>): string {
  return `CP-${cp.ID}`
}
