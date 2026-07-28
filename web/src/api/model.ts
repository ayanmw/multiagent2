// Model 管理 API 封装：按 Provider 拉取/托管模型列表、启用/禁用、标记默认。
// 后端契约见 server/internal/api/model.go：
//  - GET    /api/providers/:id/models/managed  已托管（含用户启用/默认标记）模型列表
//  - POST   /api/providers/:id/models/sync      触发上游发现并 upsert 落库，返回最新托管列表
//  - PUT    /api/providers/:id/models/:mid       切换 enabled / is_default（字段可选，nil=不修改）
import { request } from './client'

// 一条已托管模型记录（与后端 modelView 对齐）。
export interface ManagedModel {
  id: number
  provider_id: number
  model_id: string
  name: string
  owned_by: string
  enabled: boolean
  is_default: boolean
  created_at: string
  updated_at: string
}

// 切换启用/默认标记的请求体（两个字段均可选）。
export interface ModelPatch {
  enabled?: boolean
  is_default?: boolean
}

// sync 返回结构：除托管模型外还带缓存命中、本次新同步数量等元信息。
export interface SyncResult {
  provider_id: number
  cached: boolean
  synced: number
  models: ManagedModel[]
}

// 获取某个 Provider 下已托管的模型列表（不含上游重新拉取）。
export async function listManagedModels(providerId: number): Promise<ManagedModel[]> {
  const data = await request<{ provider_id: number; models: ManagedModel[] }>(
    `/providers/${providerId}/models/managed`,
  )
  return data.models ?? []
}

// 触发从 Provider 上游发现模型并 upsert 落库，返回最新托管列表。
// 等价于「刷新」——成功后托管表里就有了该 Provider 的最新模型。
export async function syncProviderModels(providerId: number): Promise<SyncResult> {
  return request<SyncResult>(`/providers/${providerId}/models/sync`, { method: 'POST' })
}

// 切换某个托管模型的启用/默认标记，返回更新后的整行。
export async function updateModel(
  providerId: number,
  modelId: number,
  patch: ModelPatch,
): Promise<ManagedModel> {
  return request<ManagedModel>(`/providers/${providerId}/models/${modelId}`, {
    method: 'PUT',
    body: patch,
  })
}
