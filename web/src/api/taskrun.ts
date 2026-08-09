// 后台任务（taskrun）管控 API 封装：列表 / 详情 / 取消 / 子任务 transcript。
// 后端契约见 server/internal/api/taskrun.go。创建只能由 Agent 内部工具发起，无人工下发端点。
import { request } from './client'

// 后台任务运行记录（来自框架 taskrun.Controller，字段随时间演进，故开放索引）。
export interface TaskRun {
  id: string
  owner_user_id: string
  app_name?: string
  child_session_id?: string
  status?: string
  // 框架可能附带的其他字段（start_time / end_time / error 等）。
  [key: string]: unknown
}

export async function listTaskRuns(): Promise<TaskRun[]> {
  const data = await request<{ runs: TaskRun[] }>('/taskruns')
  return data.runs ?? []
}

export async function getTaskRun(id: string): Promise<TaskRun> {
  const data = await request<{ run: TaskRun }>(`/taskruns/${encodeURIComponent(id)}`)
  return data.run
}

export async function cancelTaskRun(id: string): Promise<TaskRun> {
  const data = await request<{ run: TaskRun }>(`/taskruns/${encodeURIComponent(id)}/cancel`, {
    method: 'POST',
  })
  return data.run
}

// 子任务 transcript 事件（来自框架 session 事件流）。结构较自由，按记录保留。
export interface TaskRunTranscript {
  child_session_id: string
  events: Record<string, unknown>[]
}

export async function getTaskRunTranscript(id: string): Promise<TaskRunTranscript> {
  return request<TaskRunTranscript>(`/taskruns/${encodeURIComponent(id)}/transcript`)
}
