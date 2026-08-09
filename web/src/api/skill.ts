// Skills 技能仓库 API 封装：列表 / 详情 / 新建 / 更新 / 删除。
// 后端契约见 server/internal/api/skill.go（路径 :name 为技能名，共享技能只读）。
import { request } from './client'

export interface Skill {
  name: string
  description: string
  scope: string
  read_only: boolean
}

export interface SkillDetail extends Skill {
  body: string
}

export async function listSkills(): Promise<Skill[]> {
  const data = await request<{ skills: Skill[]; total: number }>('/skills')
  return data.skills ?? []
}

export async function getSkill(name: string): Promise<SkillDetail> {
  return request<SkillDetail>(`/skills/${encodeURIComponent(name)}`)
}

export async function createSkill(name: string, body: string): Promise<SkillDetail> {
  return request<SkillDetail>('/skills', { method: 'POST', body: { name, body } })
}

export async function updateSkill(name: string, body: string): Promise<SkillDetail> {
  return request<SkillDetail>(`/skills/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: { body },
  })
}

export async function deleteSkill(name: string): Promise<void> {
  await request(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
}
