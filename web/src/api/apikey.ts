// API Key 管理 API 封装：列表 / 新建（仅创建时返回明文）/ 撤销。
// 后端契约见 server/internal/api/apikey.go（需 apikeys:write 权限）。
import { request } from './client'

export interface APIKey {
  id: number
  name: string
  prefix: string
  status: string
  last_used_at?: string
  expires_at?: string
  created_at: string
}

export interface CreateAPIKeyResult extends APIKey {
  api_key: string // 明文密钥，仅创建时返回一次
}

export async function listAPIKeys(): Promise<APIKey[]> {
  const data = await request<{ api_keys: APIKey[] }>('/auth/apikeys')
  return data.api_keys ?? []
}

export async function createAPIKey(
  name: string,
  expiresInDays?: number,
): Promise<CreateAPIKeyResult> {
  const body: { name: string; expires_in_days?: number } = { name }
  if (expiresInDays != null) body.expires_in_days = expiresInDays
  return request<CreateAPIKeyResult>('/auth/apikeys', { method: 'POST', body })
}

export async function revokeAPIKey(id: number): Promise<void> {
  await request(`/auth/apikeys/${id}`, { method: 'DELETE' })
}
