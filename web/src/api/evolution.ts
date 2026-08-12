// 进化飞轮（M5-04）API 封装：候选技能列表 / 触发扫描 / 审批（approve 发布为托管技能 / reject 丢弃）。
// 后端契约见 server/internal/api/skill_candidate.go（路径前缀 /skill-candidates，client 自动拼 /api）。
import { request } from './client'

export type SkillCandidateStatus = 'pending' | 'approved' | 'rejected'

export interface SkillCandidate {
  id: number
  user_id: number
  name: string
  description: string
  body: string
  source_session_key: string
  status: SkillCandidateStatus
  reject_reason: string
  quality_notes: string
  created_at: string
  updated_at: string
}

export interface ListSkillCandidatesResp {
  skill_candidates: SkillCandidate[]
  total: number
  limit: number
  offset: number
}

export async function listSkillCandidates(params?: {
  status?: string
  limit?: number
  offset?: number
}): Promise<ListSkillCandidatesResp> {
  const q = new URLSearchParams()
  if (params?.status) q.set('status', params.status)
  if (params?.limit != null) q.set('limit', String(params.limit))
  if (params?.offset != null) q.set('offset', String(params.offset))
  const qs = q.toString()
  return request<ListSkillCandidatesResp>(`/skill-candidates${qs ? `?${qs}` : ''}`)
}

export interface ScanSkillCandidatesResp {
  scanned: number
  created: number
  skipped: number
  errors: number
}

export async function scanSkillCandidates(): Promise<ScanSkillCandidatesResp> {
  return request<ScanSkillCandidatesResp>('/skill-candidates/scan', { method: 'POST' })
}

export async function resolveSkillCandidate(
  id: number,
  decision: 'approve' | 'reject',
  rejectReason?: string,
): Promise<SkillCandidate> {
  return request<SkillCandidate>(`/skill-candidates/${id}/resolve`, {
    method: 'POST',
    body: { decision, reject_reason: rejectReason ?? '' },
  })
}
