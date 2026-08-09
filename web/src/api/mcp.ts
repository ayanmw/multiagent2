// MCP 服务器管理 API 封装：列表 / 详情 / 新建 / 更新 / 删除（仅管理面 + 校验）。
// 后端契约见 server/internal/api/mcp.go（路径 :id 为数字 id）。
import { request } from './client'

export type MCPTransport = 'stdio' | 'sse' | 'streamable'

export interface MCPServer {
  id: number
  user_id: number
  name: string
  transport: MCPTransport
  command: string
  args: string[]
  env: Record<string, string>
  url: string
  headers: Record<string, string>
  enabled: boolean
  description: string
  created_at: string
  updated_at: string
}

export interface MCPServerPayload {
  name: string
  transport: MCPTransport
  command?: string
  args?: string[]
  env?: Record<string, string>
  url?: string
  headers?: Record<string, string>
  enabled?: boolean
  description?: string
}

export async function listMCPServers(): Promise<MCPServer[]> {
  const data = await request<{ mcp_servers: MCPServer[]; total: number }>('/mcp')
  return data.mcp_servers ?? []
}

export async function getMCPServer(id: number): Promise<MCPServer> {
  return request<MCPServer>(`/mcp/${id}`)
}

export async function createMCPServer(payload: MCPServerPayload): Promise<MCPServer> {
  return request<MCPServer>('/mcp', { method: 'POST', body: payload })
}

export async function updateMCPServer(
  id: number,
  payload: Partial<MCPServerPayload>,
): Promise<MCPServer> {
  return request<MCPServer>(`/mcp/${id}`, { method: 'PUT', body: payload })
}

export async function deleteMCPServer(id: number): Promise<void> {
  await request(`/mcp/${id}`, { method: 'DELETE' })
}
