// 可观测性概览 API 封装（M3-09）：GET /api/monitoring/overview 返回进程内
// OpenTelemetry 指标聚合快照，供前端「运行监控」概览卡片渲染。后端契约见
// server/internal/api/monitoring.go 与 server/internal/metrics/metrics.go。
import { request } from './client'

// 运行监控概览快照（与 metrics.Overview 对齐）。
export interface MonitoringOverview {
  enabled: boolean // 后端 metrics 子系统是否启用（METRICS_ENABLED）
  llm_calls: number // LLM 调用总数
  llm_errors: number // LLM 调用失败数
  tool_calls: number // 工具（代码执行）调用总数
  tool_errors: number // 工具（代码执行）调用失败数
  token_prompt: number // 提示 token 累计
  token_completion: number // 补全 token 累计
  token_total: number // 总 token 累计
  loop_runs: number // 自主 Loop 运行总数
  loop_failures: number // 自主 Loop 失败总数
  budget_exhausted: number // 平台级预算耗尽拦截次数
  active_loops: number // 当前并发运行的自主 Loop 数（M7-05）
  pending_checkpoints: number // 待审批检查点堆积数（M7-05）
}

export async function getMonitoringOverview(): Promise<MonitoringOverview> {
  return request<MonitoringOverview>('/monitoring/overview')
}

// Grafana 看板地址（运维侧配置，前端「运行监控」内嵌/外链使用，M7-05）。
// 默认指向本地 docker-compose 暴露的 3000 端口；生产可由 .env 注入 VITE_GRAFANA_URL 覆盖。
export const GRAFANA_URL =
  (import.meta.env.VITE_GRAFANA_URL as string | undefined) || 'http://localhost:3000'

