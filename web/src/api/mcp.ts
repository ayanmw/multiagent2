// MCP 服务器管理 API 封装：列表 / 详情 / 新建 / 更新 / 删除（仅管理面 + 校验）。
// 后端契约见 server/internal/api/mcp.go（路径 :id 为数字 id）。
import { request } from './client'

export type MCPTransport = 'stdio' | 'sse' | 'streamable'

// M3-07：env / headers 含 token 等机密，后端以 AES-256-GCM 加密落库，**读接口一律
// 不回显明文**，只给掩码信息（has_env / env_keys）。前端编辑时留空即「不修改」，
// 需要改就整份提交新的 env / headers。
export interface MCPServer {
  id: number
  user_id: number
  name: string
  transport: MCPTransport
  command: string
  args: string[]
  /** 是否已配置 env（不含值） */
  has_env: boolean
  /** 已配置的 env 键名（升序，不含值） */
  env_keys: string[]
  url: string
  /** 是否已配置 headers（不含值） */
  has_headers: boolean
  /** 已配置的 headers 键名（升序，不含值） */
  header_keys: string[]
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
  /** 明文提交；不传表示保持原值，传 {} 表示清空 */
  env?: Record<string, string>
  url?: string
  /** 明文提交；不传表示保持原值，传 {} 表示清空 */
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

// MCPTestResult 是「测试连接」的返回结构（MX-02）。
// ok=false 表示连接/装载失败（配置错误），error 给出明确文案；ok=true 时 tools 为发现的工具列表。
export interface MCPToolInfo {
  name: string
  description: string
}
export interface MCPTestResult {
  ok: boolean
  transport: string
  count: number
  tools: MCPToolInfo[]
  error?: string
}

// testMCPServer 实际调 toolsearch 连接并预取工具列表，验证配置可用（MX-02）。
export async function testMCPServer(id: number): Promise<MCPTestResult> {
  return request<MCPTestResult>(`/mcp/${id}/test`, { method: 'POST' })
}

