// 知识库 RAG API 封装（M5-02）：知识库 CRUD + 文档索引/检索。
// 后端契约见 server/internal/api/knowledge.go。
import { request } from './client'

export interface KnowledgeBase {
  id: number
  user_id: number
  name: string
  description: string
  embedding_model: string
  doc_count: number
  chunk_count: number
  created_at: string
  updated_at: string
}

export interface DocInfo {
  name: string
  chunk_count: number
}

export interface SearchHit {
  content: string
  score: number
  source: string
  chunk_index: number
}

export async function listKnowledgeBases(): Promise<KnowledgeBase[]> {
  const data = await request<{ knowledge_bases: KnowledgeBase[]; total: number }>('/knowledge')
  return data.knowledge_bases ?? []
}

export async function createKnowledgeBase(name: string, description: string): Promise<KnowledgeBase> {
  return request<KnowledgeBase>('/knowledge', { method: 'POST', body: { name, description } })
}

export async function updateKnowledgeBase(
  id: number,
  name: string,
  description: string,
): Promise<KnowledgeBase> {
  return request<KnowledgeBase>(`/knowledge/${id}`, {
    method: 'PUT',
    body: { name, description },
  })
}

export async function deleteKnowledgeBase(id: number): Promise<void> {
  await request(`/knowledge/${id}`, { method: 'DELETE' })
}

export async function listDocuments(id: number): Promise<DocInfo[]> {
  const data = await request<{ documents: DocInfo[]; total: number }>(`/knowledge/${id}/documents`)
  return data.documents ?? []
}

export async function indexDocument(
  id: number,
  name: string,
  content: string,
  contentType: string,
): Promise<{ indexed_chunks: number }> {
  return request<{ indexed_chunks: number }>(`/knowledge/${id}/documents`, {
    method: 'POST',
    body: { name, content, content_type: contentType },
  })
}

export async function deleteDocument(id: number, name: string): Promise<{ deleted_chunks: number }> {
  return request<{ deleted_chunks: number }>(
    `/knowledge/${id}/documents/${encodeURIComponent(name)}`,
    { method: 'DELETE' },
  )
}

export async function searchKnowledge(
  id: number,
  query: string,
  topK: number,
): Promise<SearchHit[]> {
  const data = await request<{ hits: SearchHit[]; total: number }>(`/knowledge/${id}/search`, {
    method: 'POST',
    body: { query, top_k: topK },
  })
  return data.hits ?? []
}
