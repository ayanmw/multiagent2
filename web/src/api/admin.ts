// 用户管理后台 API 封装（MX-06）。后端契约见 server/internal/api/admin.go（admin-only）。
// 所有端点挂载在 /api/admin 下，由后端 RequireRole(admin) 强制管理员鉴权。
import { request } from './client'

export type UserRole = 'admin' | 'developer' | 'viewer'
export type UserStatus = 'active' | 'disabled'

// 用户配额（对应作用于该用户的最具体预算策略，M3-04）。
export interface UserQuota {
  max_tokens: number
  window: string
  is_global: boolean
}

// 用户管理视图（对应后端 adminUserView）。
export interface AdminUser {
  id: number
  username: string
  email: string
  display_name: string
  role_id: number
  role: UserRole
  status: UserStatus
  created_at: string
  quota?: UserQuota | null
  [key: string]: unknown
}

export interface AdminUserListResult {
  users: AdminUser[]
  total: number
}

export interface CreateUserPayload {
  username: string
  email: string
  password: string
  display_name?: string
  role?: UserRole
}

export interface UpdateUserPayload {
  display_name?: string
  role?: UserRole
  status?: UserStatus
}

export interface ResetPasswordPayload {
  password: string
}

// GET /api/admin/users —— 用户列表（含角色/状态/配额）。
export async function listUsers(): Promise<AdminUserListResult> {
  return request<AdminUserListResult>('/admin/users')
}

// POST /api/admin/users —— 新建用户（默认 developer，可指定角色）。
export async function createUser(payload: CreateUserPayload): Promise<AdminUser> {
  return request<AdminUser>('/admin/users', { method: 'POST', body: payload })
}

// GET /api/admin/users/:id —— 用户详情。
export async function getUser(id: number): Promise<AdminUser> {
  return request<AdminUser>(`/admin/users/${id}`)
}

// PUT /api/admin/users/:id —— 更新（显示名/角色/状态）。
export async function updateUser(id: number, payload: UpdateUserPayload): Promise<AdminUser> {
  return request<AdminUser>(`/admin/users/${id}`, { method: 'PUT', body: payload })
}

// POST /api/admin/users/:id/disable —— 禁用用户。
export async function disableUser(id: number): Promise<AdminUser> {
  return request<AdminUser>(`/admin/users/${id}/disable`, { method: 'POST' })
}

// POST /api/admin/users/:id/enable —— 启用用户。
export async function enableUser(id: number): Promise<AdminUser> {
  return request<AdminUser>(`/admin/users/${id}/enable`, { method: 'POST' })
}

// POST /api/admin/users/:id/reset-password —— 管理员重置密码。
export async function resetPassword(id: number, payload: ResetPasswordPayload): Promise<void> {
  await request<void>(`/admin/users/${id}/reset-password`, { method: 'POST', body: payload })
}

// 角色 → 中文标签（用于表格着色）。
export function roleLabel(r: UserRole): string {
  switch (r) {
    case 'admin':
      return '管理员'
    case 'developer':
      return '开发者'
    case 'viewer':
      return '只读'
    default:
      return (r as string) || '-'
  }
}

// 状态 → 中文标签。
export function statusLabel(s: UserStatus): string {
  switch (s) {
    case 'active':
      return '启用'
    case 'disabled':
      return '禁用'
    default:
      return (s as string) || '-'
  }
}
