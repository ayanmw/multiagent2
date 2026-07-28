// Session 管理 API 封装：新建 / 列表 / 详情（含历史消息）。
// 后端契约见 server/internal/api/session.go：
//  - POST   /api/sessions        新建会话（body 可选 {title}），返回 sessionView（含 session_key）
//  - GET    /api/sessions        当前用户会话列表（按最近活动倒序）
//  - GET    /api/sessions/:id    会话详情（:id 即 session_key），含 messages[]
import { request } from './client'

// 会话对外视图（列表/详情共用基础字段）。
export interface SessionView {
  id: number
  user_id: number
  session_key: string
  title: string
  created_at: string
  updated_at: string
}

// 单条消息视图（历史回放用）。
export interface MessageView {
  id: number
  role: 'user' | 'assistant'
  content: string
  created_at: string
}

// 会话详情（在 SessionView 基础上追加历史消息列表）。
export interface SessionDetail extends SessionView {
  messages: MessageView[]
}

// 新建会话；标题可选，缺省由后端填「新对话」。
export async function createSession(title?: string): Promise<SessionView> {
  return request<SessionView>('/sessions', {
    method: 'POST',
    body: title ? { title } : {},
  })
}

// 拉取当前用户的全部会话（已按最近活动倒序）。
export async function listSessions(): Promise<SessionView[]> {
  const data = await request<{ sessions: SessionView[]; total: number }>('/sessions')
  return data.sessions ?? []
}

// 按 session_key 拉取会话详情（含历史消息）。
export async function getSession(key: string): Promise<SessionDetail> {
  return request<SessionDetail>(`/sessions/${key}`)
}
