# 日志聚合与 trace 贯通（M7-06）

> 目标：一次对话的完整链路（HTTP 请求 → 统一网关 → 引擎/LLM → 工具执行）
> 可以在集中日志里按 **trace_id** 一键串联；出错时可下钻到**具体被拒绝/失败的命令**。

## 1. 结构化日志（默认 JSON）

后端启动时经 `obslog` 初始化全局日志（基于标准库 `log/slog`）：

| 环境变量 | 默认 | 说明 |
|----------|------|------|
| `LOG_FORMAT` | `json` | `json`（生产友好，可被 Loki/ELK 集中采集）或 `text`（本地调试） |
| `LOG_LEVEL`  | `info` | `debug` / `info` / `warn` / `error` |

- 全局 `slog` 与标准 `log` 包（既有 `log.Printf` 调用）输出统一进同一 handler，
  存量日志行也以 JSON 产出（消息落在 `msg` 字段），逐步收敛。
- 每行 JSON 自动携带（若有）：`trace_id` / `request_id` / `span_id` / `parent_span_id`。

典型日志行：

```json
{"time":"2026-08-19T12:00:01.123+08:00","level":"INFO","msg":"http.request","trace_id":"9f2c...","request_id":"a1b2...","method":"POST","path":"/api/chat/sess-xxx/stream","status":200,"latency_ms":3412,"client_ip":"127.0.0.1"}
{"time":"...","level":"INFO","msg":"span.end","trace_id":"9f2c...","request_id":"a1b2...","span_id":"c3d4...","parent_span_id":"e5f6...","span_name":"gateway.stream","duration_ms":3412,"channel":"web","session_key":"sess-xxx","user_id":1,"status":"ok"}
{"time":"...","level":"INFO","msg":"span.end","trace_id":"9f2c...","span_id":"...","parent_span_id":"...","span_name":"engine.llm_run","model":"hy3","session_id":"sess-xxx","duration_ms":3301,"status":"ok"}
{"time":"...","level":"INFO","msg":"span.end","trace_id":"9f2c...","span_id":"...","parent_span_id":"...","span_name":"executor.run","command":"git status --short","workdir":"...","duration_ms":12,"decision":"allowed","exit_code":0,"status":"ok"}
```

## 2. trace 贯通链路（一次对话的 span 树）

| 层级 | span_name | 何时产生 | 关键字段 |
|------|-----------|----------|----------|
| HTTP 入口 | `http.request` | RequestID 中间件（访问日志） | method / path / status / latency_ms / client_ip |
| 统一网关 | `gateway.run` / `gateway.stream` | Gateway 处理对话 | channel / session_key / user_id |
| 引擎 | `engine.llm_run` | 每次 Runner 运行 | model / session_id |
| 工具执行 | `executor.run` | 每条受安全策略保护的命令 | command / workdir / decision / exit_code |
| 自主 Loop | `automation.run` | cron 触发的自主化运行 | automation_id / automation_name / attempts |

- 每个 span 由 `StartSpan` 生成：自带 `span_id`，记录 `parent_span_id`（可重建调用树），
  结束时写一条 `span.end`（含 `duration_ms` / `status`）；出错时 `status=error` 并附 `err`。
- trace_id 为 **W3C 格式**（32 位 hex）：HTTP 入口解析客户端 `traceparent` 头
  （无则自动生成），响应头回显 `X-Request-ID` 与 `traceparent`——外部调用方可凭
  响应头中的 trace 到日志/监控中检索本次请求。
- 后台自主 Loop（cron / webhook / 恢复）不经过 HTTP，以 `automation.run` 为根 span
  自建 trace，同样贯通到其内部 LLM 调用与工具执行。

## 3. 集中采集（Loki + Promtail）

`monitoring/docker-compose.monitoring.yml` 已包含 Loki + Promtail：

```sh
docker compose -f monitoring/docker-compose.monitoring.yml up -d
```

- Promtail 通过 docker.sock 服务发现采集 `gm-server` / `gm-web` / `gm-gateway`
  （根 compose）与 `codeagent-*`（监控 compose）容器日志，推送进 Loki（:3100）。
- Grafana（:3000）已自动供应数据源；Explore 中选择 Loki 即可查询。

### LogQL 查询示例

```logql
# 1) 按 trace 串联一次对话的全部日志（从响应头 traceparent 取 trace_id）
{container="gm-server"} | json | trace_id="9f2c..."

# 2) 只看某次请求的访问日志与 span 汇总
{container="gm-server"} | json | request_id="a1b2..." | line_format "{{.msg}} {{.span_name}} {{.status}} {{.duration_ms}}ms"

# 3) 错误下钻：某 trace 内所有被安全策略拒绝的命令
{container="gm-server"} | json | trace_id="9f2c..." | decision="denied"

# 4) 全局最近失败（LLM / 命令 / 自主 Loop）
{container="gm-server"} | json | status="error"

# 5) 自主 Loop 运行概览
{container="gm-server"} | json | span_name="automation.run" | line_format "automation={{.automation_name}} status={{.status}} attempts={{.attempts}} {{.duration_ms}}ms"
```

> 若后端以**裸进程**跑在宿主机（不经 docker），可给 promtail 增加一个
> `file_sd_configs`/`static_configs` 指向后端日志文件（需后端把 stdout 重定向到文件），
> 或在后端侧改用 OTel Collector 的 filelog receiver（见下）。

## 4. 升级路径：OTel Collector

当前实现用「结构化日志 + W3C trace-id/span-id 自描述」达成 trace 贯通，
**不引入 tracing SDK/导出器**（沙箱无法可靠拉取 OTel exporter 依赖，且避免进程内
链路采样开销）。字段语义与 W3C traceparent 完全兼容，后续升级 OTel 无需改业务代码：

1. 加 `go.opentelemetry.io/otel` tracing SDK + OTLP exporter（gRPC/HTTP）；
2. `obslog.StartSpan` 内部改为同时创建 `trace.Span`（traceID 沿用现有 W3C 值）；
3. 部署 OTel Collector（`receivers: otlp` → `exporters: loki` 或 `prometheus`），
   用 `trace_id` 做 `trace_id` 标签，即可在 Grafana Tempo / Loki 无缝串联。

## 5. 相关配置与代码位置

| 项 | 位置 |
|----|------|
| 日志/trace 基础设施 | `server/internal/obslog/` |
| RequestID 中间件 | `server/internal/middleware/requestid.go` |
| 访问日志 | `server/internal/middleware/securelogging.go` |
| 对话 span | `server/internal/api/gateway.go` |
| LLM span | `server/internal/engine/engine.go` |
| 命令执行 span | `server/internal/executor/policy.go` |
| 自主 Loop span | `server/internal/scheduler/scheduler.go` |
| 配置项 | `LOG_FORMAT` / `LOG_LEVEL`（`server/internal/config`） |
| 采集编排 | `monitoring/docker-compose.monitoring.yml` + `monitoring/promtail/` |
