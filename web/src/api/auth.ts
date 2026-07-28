// 认证相关 API 封装：注册 / 登录 / 获取当前用户。
import { request } from './client'

export interface User {
  id: number
  username: string
  email: string
  display_name: string
  role_id: number
  role: string
  status: string
}

export interface AuthResponse {
  token: string
  user: User
}

export async function register(payload: {
  username: string
  email: string
  password: string
  display_name?: string
}): Promise<AuthResponse> {
  return request<AuthResponse>('/auth/register', {
    method: 'POST',
    body: payload,
    auth: false,
  })
}

export async function login(payload: {
  account: string
  password: string
}): Promise<AuthResponse> {
  return request<AuthResponse>('/auth/login', {
    method: 'POST',
    body: payload,
    auth: false,
  })
}

export async function me(): Promise<{ user: User }> {
  return request<{ user: User }>('/me')
}
