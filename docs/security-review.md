# M7.5-05 安全复核报告

> 日期：2026-08-20 ｜ 阶段：M7.5 上线前真实验证 ｜ 状态：✅ 通过
> 范围：登录/对话限流（MX-07）、CORS 白名单（MX-07）、日志脱敏（MX-07 + M7-06 securelogging）、Alertmanager webhook token（M7-04）

## 一、结论摘要

四项安全防线经「代码审查 + 集成级测试 + 真实服务运行时验证」三层复核，**全部生效**，未发现可利用缺口。本报告同时交付自动化可回归的集成测试套件（`server/cmd/server/security_test.go`，5 用例全绿）。

| # | 防线 | 单测（既有） | 集成测试（新增） | Runtime curl 验证 | 结论 |
|---|------|-------------|-----------------|------------------|------|
| 1 | 高频登录限流 | `ratelimit_test.go` | `TestSecurity_HighFreqLoginRateLimited` | 401→401→401→**429** | ✅ |
| 2 | 对话限流（按用户） | `ratelimit_test.go` | `TestSecurity_ChatRateLimited` | 同实现链路 | ✅ |
| 3 | CORS 白名单 | `cors_test.go` | `TestSecurity_CORSWhitelist` | 白名单 204+ACAO / 非白名单无 ACAO | ✅ |
| 4 | 访问日志脱敏 | `securelogging_test.go` | `TestSecurity_AccessLog_NoPlaintext` | 3 个明文 0 命中 | ✅ |
| 5 | Alerts webhook token | `alerts_test.go` | `TestSecurity_AlertsWebhookToken` | 无/错 401，对 200 | ✅ |

## 二、逐项复核详情

### 1. 登录/注册防爆破限流（MX-07）

- **实现**：`middleware/ratelimit.go` 滑动窗口限流器（`NewRateLimiter`，进程内、并发安全）；`RateLimit(ClientIPKey, limit, window)` 挂载于 `POST /api/auth/register` 与 `/api/auth/login`（`main.go:85-87`）。
- **配置**：`RATE_LIMIT_LOGIN_ENABLED`（默认 true）/ `RATE_LIMIT_LOGIN_LIMIT`（默认 10 次）/ `RATE_LIMIT_LOGIN_WINDOW_SECONDS`（默认 60s）。
- **验证**：低阈值 3 次/60s 下 4 连发 → `[401, 401, 401, 429]`（前 3 次凭据错误 401 到达 handler，第 4 次被中间件 429 拦截，响应含 `retry_after`）。

### 2. 对话防滥用限流（MX-07）

- **实现**：同一限流器按**认证用户 ID** 限流（`UserIDKey`，未认证回落 IP），挂载于 `POST /api/chat` 与 `POST /api/chat/:session_id/stream`（`main.go:297-301`）。
- **配置**：`RATE_LIMIT_CHAT_ENABLED`（默认 true）/ `RATE_LIMIT_CHAT_LIMIT`（默认 30 次）/ `RATE_LIMIT_CHAT_WINDOW_SECONDS`（默认 60s）。
- **验证**：低阈值 3 次/60s 下注册用户 4 连发 `/api/chat` → `[502, 502, 502, 429]`（前 3 次到达 handler，第 4 次 429）。限流先于业务逻辑生效，`limit<=0` 时中间件直接放行（可开关）。

### 3. CORS 精细化白名单（MX-07）

- **实现**：`middleware/cors.go` —— **仅白名单源**获 `Access-Control-Allow-Origin` 头；**无反射回显**（不把请求 Origin 原样写回，防 naive CSRF）；带 Origin 的 OPTIONS 预检短路 204（不落路由/鉴权）；`AllowCredentials` 仅与具体源组合（规范合规，不配 `*`）。
- **配置**：`CORS_ALLOWED_ORIGINS`（逗号分隔，默认本地 Vite 源 `http://localhost:5173,http://127.0.0.1:5173`；生产需显式配置前端域名；`*` 仅公开非凭证接口）。
- **验证**：白名单源预检 → `204` + `Access-Control-Allow-Origin: http://localhost:5173` + `Access-Control-Allow-Credentials: true` + `Vary: Origin`；非白名单源 → 无任何 ACAO 头（浏览器据此拒绝读取响应）。

### 4. 访问日志脱敏（MX-07 + M7-06 securelogging）

- **实现**：`middleware/securelogging.go` `SecureLogger` —— 结构化 JSON 访问日志（经 obslog，自动携带 trace_id/request_id）；**不打印请求体**（M0.5-06 已将 message 移入 POST body，query 不再含消息）；`redactHeaders` 对 `Authorization` / `X-API-Key` 做掩码（防御性，防未来改动引入泄露）。
- **验证**：请求携带明文 `Authorization: Bearer <token>`、`X-API-Key: <key>`、body 密码 → 捕获完整访问日志，三个明文串 **0 命中**；日志含 `http.request` + `method/path/status/latency_ms/client_ip` 字段。

### 5. Alertmanager webhook 共享密钥（M7-04）

- **实现**：`api/alerts.go` `AlertsWebhookHandler`——`ALERT_WEBHOOK_TOKEN` 非空时要求请求携带密钥，支持 `Authorization: Bearer <token>` 与 `X-Alert-Token: <token>` 双通道；空则关闭校验（向后兼容，生产建议配置）。`/api/alerts` 由 `main.go:745` 注册（不在 buildRouter 内，集成测试仿照接线）。
- **验证**：无令牌 → 401；错误令牌 → 401；正确令牌（Bearer / X-Alert-Token）→ 200 且回显 `received:1`、投递通知（`notified:1`）。

## 三、交付物

1. **集成测试套件**：`server/cmd/server/security_test.go`（5 用例，走完整 `buildRouter` 中间件链，`config.Load()` + `t.Setenv` 注入低阈值/白名单，测试间环境隔离；`go test -count=1 -v ./cmd/server/ -run 'TestSecurity_'`）。
2. **运行时验证脚本记录**：真实服务（PORT=8091，低阈值配置）curl 实测数据见第二节各表。

## 四、验证命令与结果

```sh
# 集成测试套件（5/5 PASS）
go test -count=1 -v ./cmd/server/ -run 'TestSecurity_' -timeout 10m
# 全量（无回归）
go build ./... && go vet ./... && go test -count=1 ./cmd/server/ -timeout 15m  # ok
```

## 五、残留风险与建议（非阻塞）

- 限流器为**进程内**实现（`RateLimiter` 内存态）：单实例部署足够；`hpa-web` 扩容后多副本场景下限流计数不共享，需换 Redis 等共享存储（`ratelimit.go:14` 注释已说明）。
- `/api/alerts` 在 `buildRouter` 之外由 `main()` 注册，集成测试已仿照接线覆盖；如未来将路由收敛进 `buildRouter`，测试无需改动。
- 生产部署务必配置 `ALERT_WEBHOOK_TOKEN` 与显式 `CORS_ALLOWED_ORIGINS`（.env.example 已含两变量，见 M7-07）。
