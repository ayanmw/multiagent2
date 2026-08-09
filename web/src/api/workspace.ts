// Workspace 管理 API 封装：列表 / 详情 / 新建 / 更新 / 删除。
// 后端契约见 server/internal/api/workspace.go（路径 :id 实为 workspace_key）。
import { request } from './client'

export interface Workspace {
  id: number
  user_id: number
  key: string
  name: string
  local_path: string
  git_remote: string
  description: string
  status: string
  created_at: string
  updated_at: string
}

export interface WorkspacePayload {
  name: string
  git_remote?: string
  description?: string
}

export async function listWorkspaces(): Promise<Workspace[]> {
  const data = await request<{ workspaces: Workspace[]; total: number }>('/workspaces')
  return data.workspaces ?? []
}

export async function getWorkspace(key: string): Promise<Workspace> {
  return request<Workspace>(`/workspaces/${key}`)
}

export async function createWorkspace(payload: WorkspacePayload): Promise<Workspace> {
  return request<Workspace>('/workspaces', { method: 'POST', body: payload })
}

// 部分更新：只传需要改的字段。
export async function updateWorkspace(
  key: string,
  payload: Partial<WorkspacePayload>,
): Promise<Workspace> {
  return request<Workspace>(`/workspaces/${key}`, { method: 'PUT', body: payload })
}

export async function deleteWorkspace(key: string): Promise<void> {
  await request(`/workspaces/${key}`, { method: 'DELETE' })
}
