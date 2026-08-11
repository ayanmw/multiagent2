// 通知中心 API 封装（M4-07 outbound）。
// 后端契约见 server/internal/api/notification.go：站内信（in-app）列表 + 未读计数 + 标记已读。
// owner 隔离由后端强制：每个用户只能看到自己的通知；viewer 仅 read，写操作被后端 RBAC 拒绝。
import { request } from './client'

// 通知类型（对应 server/internal/model/notification.go 常量）。
export type NotificationType = 'success' | 'failure' | 'checkpoint'

// 引用来源（对应 model.NotificationRef* 常量）。
export type NotificationRefKind = 'automation' | 'automation_run' | 'checkpoint' | ''

// 单条通知（对应后端 notificationView）。
export interface Notification {
  id: number
  user_id: number
  type: NotificationType
  title: string
  message: string
  ref_kind: NotificationRefKind
  ref_id: string
  read: boolean
  created_at: string
  [key: string]: unknown
}

export interface NotificationListResult {
  notifications: Notification[]
  total: number
  unread: number
  limit: number
  offset: number
}

// GET /api/notifications —— 列表 + 未读计数，供通知中心与顶栏红点使用。
export async function listNotifications(params: {
  limit?: number
  offset?: number
} = {}): Promise<NotificationListResult> {
  const query = new URLSearchParams()
  if (params.limit !== undefined) query.set('limit', String(params.limit))
  if (params.offset !== undefined) query.set('offset', String(params.offset))
  const qs = query.toString()
  return request<NotificationListResult>(`/notifications${qs ? `?${qs}` : ''}`)
}

// POST /api/notifications/:id/read —— 标记单条已读（需 notifications:write）。
export async function markNotificationRead(id: number): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(`/notifications/${id}/read`, { method: 'POST' })
}

// POST /api/notifications/read-all —— 全部已读（需 notifications:write）。
export async function markAllNotificationsRead(): Promise<{ ok: boolean; affected: number }> {
  return request<{ ok: boolean; affected: number }>(`/notifications/read-all`, { method: 'POST' })
}

// 类型 → 标签语义（用于列表/顶栏配色）。
export function notificationTypeLabel(t: NotificationType): string {
  switch (t) {
    case 'success':
      return '成功'
    case 'failure':
      return '失败'
    case 'checkpoint':
      return '待审批'
    default:
      return t || '-'
  }
}
