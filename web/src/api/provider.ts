// Provider 管理 API 封装：列表 / 详情 / 新建 / 更新 / 删除 / 模型发现。
// 后端约定：api_key 仅在创建/更新请求体传入，响应从不回显，只返回 has_api_key 布尔。
import { request } from './client'

export type Protocol = 'openai' | 'anthropic' | 'gemini'

export interface Provider {
  id: number
  name: string
  protocol: Protocol
  base_url: string
  has_api_key: boolean
  description: string
  created_at: string
  updated_at: string
}

// 新建/更新请求体；api_key 可选（编辑时留空表示不修改）。
export interface ProviderPayload {
  name: string
  protocol: Protocol
  base_url?: string
  api_key?: string
  description?: string
}

export interface ProviderModel {
  id: string
  owned_by?: string
}

export interface ProviderModelsResult {
  provider_id: number
  protocol: Protocol
  base_url: string
  cached: boolean
  models: ProviderModel[]
}

export async function listProviders(): Promise<Provider[]> {
  const data = await request<{ providers: Provider[] }>('/providers')
  return data.providers ?? []
}

export async function getProvider(id: number): Promise<Provider> {
  return request<Provider>(`/providers/${id}`)
}

export async function createProvider(payload: ProviderPayload): Promise<Provider> {
  return request<Provider>('/providers', {
    method: 'POST',
    body: payload,
  })
}

export async function updateProvider(id: number, payload: ProviderPayload): Promise<Provider> {
  return request<Provider>(`/providers/${id}`, {
    method: 'PUT',
    body: payload,
  })
}

export async function deleteProvider(id: number): Promise<void> {
  await request(`/providers/${id}`, { method: 'DELETE' })
}

// 拉取 Provider 暴露的模型列表，等价于「测试连接」——成功即表示地址/密钥可达。
export async function fetchProviderModels(id: number): Promise<ProviderModelsResult> {
  return request<ProviderModelsResult>(`/providers/${id}/models`)
}
