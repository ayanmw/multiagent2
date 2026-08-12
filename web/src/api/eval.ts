// 评估回归（M5-05）API 封装：评估集 / 用例 / 运行 / 结果 四区。
// 后端契约见 server/internal/api/eval.go（路径前缀 /eval，client 自动拼 /api）。
import { request } from './client'

// ---- 类型 ----

export type GraderType = 'exact' | 'contains' | 'llm'
export type EvalRunStatus = 'running' | 'done' | 'failed'

export interface EvalDataset {
  id: number
  user_id: number
  name: string
  description: string
  default_grader: GraderType
  default_model: string
  created_at: string
  updated_at: string
}

export interface EvalCase {
  id: number
  dataset_id: number
  input: string
  expected: string
  grader: GraderType
  model: string
  created_at: string
  updated_at: string
}

export interface EvalRun {
  id: number
  dataset_id: number
  user_id: number
  model: string
  grader: GraderType
  repeats: number
  status: EvalRunStatus
  score_avg: number
  pass_rate: number
  total_cases: number
  total_attempts: number
  error: string
  started_at: string | null
  finished_at: string | null
  created_at: string
}

export interface EvalResult {
  id: number
  run_id: number
  dataset_id: number
  case_id: number
  attempt: number
  grader: GraderType
  output: string
  score: number
  passed: boolean
  latency_ms: number
  error: string
  created_at: string
}

// ---- 评估集 Dataset ----

export interface ListEvalDatasetsResp {
  datasets: EvalDataset[]
  total: number
}

export async function listEvalDatasets(): Promise<ListEvalDatasetsResp> {
  return request<ListEvalDatasetsResp>('/eval/datasets')
}

export async function createEvalDataset(body: {
  name: string
  description?: string
  default_grader: GraderType
  default_model?: string
}): Promise<EvalDataset> {
  return request<EvalDataset>('/eval/datasets', { method: 'POST', body })
}

export async function updateEvalDataset(
  id: number,
  body: {
    name?: string
    description?: string
    default_grader?: GraderType
    default_model?: string
  },
): Promise<EvalDataset> {
  return request<EvalDataset>(`/eval/datasets/${id}`, { method: 'PUT', body })
}

export async function deleteEvalDataset(id: number): Promise<void> {
  await request(`/eval/datasets/${id}`, { method: 'DELETE' })
}

// ---- 用例 Case ----

export interface ListEvalCasesResp {
  cases: EvalCase[]
  total: number
}

export async function listEvalCases(datasetId: number): Promise<ListEvalCasesResp> {
  return request<ListEvalCasesResp>(`/eval/datasets/${datasetId}/cases`)
}

export async function createEvalCase(
  datasetId: number,
  body: { input: string; expected: string; grader?: GraderType; model?: string },
): Promise<EvalCase> {
  return request<EvalCase>(`/eval/datasets/${datasetId}/cases`, { method: 'POST', body })
}

export async function updateEvalCase(
  datasetId: number,
  caseId: number,
  body: { input?: string; expected?: string; grader?: GraderType; model?: string },
): Promise<EvalCase> {
  return request<EvalCase>(`/eval/datasets/${datasetId}/cases/${caseId}`, {
    method: 'PUT',
    body,
  })
}

export async function deleteEvalCase(datasetId: number, caseId: number): Promise<void> {
  await request(`/eval/datasets/${datasetId}/cases/${caseId}`, { method: 'DELETE' })
}

// ---- 运行 Run ----

export interface ListEvalRunsResp {
  runs: EvalRun[]
  total: number
}

export async function listEvalRuns(datasetId?: number): Promise<ListEvalRunsResp> {
  const qs = datasetId != null ? `?dataset_id=${datasetId}` : ''
  return request<ListEvalRunsResp>(`/eval/runs${qs}`)
}

export async function getEvalRun(id: number): Promise<EvalRun> {
  return request<EvalRun>(`/eval/runs/${id}`)
}

export async function runEval(
  datasetId: number,
  body?: { model?: string; grader?: GraderType; repeats?: number },
): Promise<EvalRun> {
  return request<EvalRun>(`/eval/datasets/${datasetId}/run`, { method: 'POST', body: body ?? {} })
}

// ---- 结果 Result ----

export interface ListEvalResultsResp {
  results: EvalResult[]
  total: number
}

export async function listEvalResults(runId: number): Promise<ListEvalResultsResp> {
  return request<ListEvalResultsResp>(`/eval/runs/${runId}/results`)
}
