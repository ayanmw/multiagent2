// 租户管理 API 封装（M8-09 多租户隔离）。后端契约见 server/internal/api/tenant.go。
// 租户是「一组用户的配额边界」：租户内用户共享租户级预算上限，租户 A 超配额不影响 B。
import { request } from './client'

export type TenantStatus = 'active' | 'disabled'

// 租户视图（对应后端 tenantView，含成员数）。
export interface Tenant {
  id: number
  name: string
  description: string
  status: TenantStatus
  created_by: number
  member_count: number
  created_at: string
  updated_at: string
}

export interface TenantListResult {
  tenants: Tenant[]
  total: number
}

export interface CreateTenantPayload {
  name: string
  description?: string
}

export interface UpdateTenantPayload {
  name?: string
  description?: string
  status?: TenantStatus
}

// GET /api/tenants —— 租户列表（tenants:read）。
export async function listTenants(): Promise<TenantListResult> {
  return request<TenantListResult>('/tenants')
}

// POST /api/tenants —— 创建租户（tenants:write）。
export async function createTenant(payload: CreateTenantPayload): Promise<Tenant> {
  return request<Tenant>('/tenants', { method: 'POST', body: payload })
}

// PUT /api/tenants/:id —— 更新租户（改名/描述/启停用）。
export async function updateTenant(id: number, payload: UpdateTenantPayload): Promise<Tenant> {
  return request<Tenant>(`/tenants/${id}`, { method: 'PUT', body: payload })
}

// DELETE /api/tenants/:id —— 删除租户（有成员时后端 409）。
export async function deleteTenant(id: number): Promise<void> {
  await request<void>(`/tenants/${id}`, { method: 'DELETE' })
}

// POST /api/tenants/:id/members —— 把用户加入租户。
export async function addTenantMember(tenantId: number, userId: number): Promise<void> {
  await request<void>(`/tenants/${tenantId}/members`, { method: 'POST', body: { user_id: userId } })
}

// DELETE /api/tenants/:id/members/:uid —— 把用户移出租户。
export async function removeTenantMember(tenantId: number, userId: number): Promise<void> {
  await request<void>(`/tenants/${tenantId}/members/${userId}`, { method: 'DELETE' })
}

// ---- 预算策略（M3-04 平台级预算护栏；M8-09 支持 tenant/workspace 作用域） ----

export type BudgetScope = 'user' | 'session' | 'automation' | 'tenant' | 'workspace'
export type BudgetWindow = 'daily' | 'total'

export interface BudgetPolicy {
  id: number
  scope: BudgetScope
  scope_key: string
  max_tokens: number
  window: BudgetWindow
}

export interface UpsertBudgetPayload {
  scope: BudgetScope
  scope_key?: string
  max_tokens: number
  window?: BudgetWindow
}

// GET /api/budgets —— 全部预算策略（budgets:read）。
export async function listBudgets(): Promise<BudgetPolicy[]> {
  const r = await request<{ budget_policies: BudgetPolicy[] }>('/budgets')
  return r.budget_policies
}

// PUT /api/budgets —— upsert 预算策略（budgets:write）。
export async function upsertBudget(payload: UpsertBudgetPayload): Promise<BudgetPolicy> {
  return request<BudgetPolicy>('/budgets', { method: 'PUT', body: payload })
}

// DELETE /api/budgets/:id —— 删除预算策略（budgets:write）。
export async function deleteBudget(id: number): Promise<void> {
  await request<void>(`/budgets/${id}`, { method: 'DELETE' })
}
