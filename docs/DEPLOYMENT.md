# goMultiAgentV2 生产部署手册（运维向）

> 覆盖：CI/CD 与镜像发布 · Kubernetes 部署主线 · Prometheus+Grafana 监控告警 ·
> 结构化日志与 trace 贯通 · 密钥管理 · 备份/升级/回滚 · 排障清单。
> **验收标准**：新人按本文档可独立完成 K8s 部署，并能在 Grafana/Loki 看到运行监控与一次对话的 trace 串联。
> 对应里程碑：**M7-07**（依赖 M7-01 CI / M7-02 容器化 / M7-03 K8s / M7-04 告警 / M7-05 Grafana / M7-06 日志聚合）。

---

## 0. 部署拓扑总览

```
                        ┌─────────────────────────── Ingress (agent.example.com) ───────────────────────────┐
                        │                                                                                    │
                    /api /health /metrics /.well-known/  ──▶  server:8080（后端 Service，名称固定为 server）   │
                    /（SPA）                              ──▶  web:80（前端静态站 + nginx 反代 /api）            │
                        └────────────────────────────────────────┬───────────────────────────────────────────┘
                                                                 │ OpenAI 兼容 /v1
                                                                 ▼
                                                ┌──────────────────────────────┐
                                                │ gateway（LLM 网关，:8088）      │
                                                │ passthrough → 外部 LLM 端点     │
                                                └──────────────────────────────┘

  旁路（可选但推荐，监控告警）：
  Prometheus ──scrape──▶ server:8080/metrics
      │  加载 alert-rules.yml → 触发 Alertmanager → webhook → server /api/alerts → 通知中心
  Promtail ──docker.sock──▶ 容器 stdout（JSON 日志）──▶ Loki ──▶ Grafana Explore（按 trace_id 串联）
```

| 组件 | 镜像（ghcr.io/ayanmw/multiagent2/...） | 端口 | 说明 |
|------|----------------------------------------|------|------|
| backend | `server:latest` | 8080 | 后端 API + Agent 引擎；**单副本**（本地 SQLite 单文件，多副本写冲突） |
| web | `web:latest` | 80 | nginx 托管 SPA，反代 `/api` 到 `server:8080`（**服务名写死 `server`**） |
| gateway | `gateway:latest` | 8088 | LLM 网关；容器内必须 `passthrough` 后端（`codebuddy` 后端需本机 WorkBuddy 桌面，无法进容器） |

> 三镜像均为**纯 Go 多阶段构建（`CGO_ENABLED=0`）**，运行态 alpine，无需 C 编译器。

---

## 1. 前置条件

- **Kubernetes 集群** 1.25+，有默认 StorageClass（PVC 用）；`kubectl` 可访问目标集群。
- **域名**（如 `agent.example.com`）解析到 Ingress Controller（nginx-ingress 等），并有 TLS 证书（cert-manager 或现有 secret）。
- **镜像仓库账号**：可推送/拉取 `ghcr.io`（或替换为自建 Harbor 等）。
- **外部 OpenAI 兼容 LLM 端点**（自建 vLLM / 云厂商 / 本地网关），用于 Server 端 Provider 配置。
- 本机工具：`docker`、`kubectl`、`openssl`（生成密钥）、`git`。

---

## 2. 镜像构建与发布（CI/CD）

### 2.1 CI 现状（M7-01）

`.github/workflows/ci.yml` 在 **push main / PR** 时跑三作业：

| 作业 | 内容 | 触发 |
|------|------|------|
| `server` | `CGO_ENABLED=0 go build/vet/test`（glebarez/sqlite 纯 Go，无需 gcc；worktree/taskrun 测试在 ubuntu runner 真跑 git） | push / PR |
| `web` | `npm ci` + `npm run build` + `npm run typecheck` | push / PR |
| `docker` | 三镜像 `docker build` **仅校验不推送**（`push: false`，防误发） | 仅 push main |

### 2.2 发版推送（手动）

CI 目前不推送镜像。发布新版本时在本地或 CI 增加推送步骤：

```sh
# 在仓库根目录执行（三镜像分别构建并推送）
docker build -t ghcr.io/ayanmw/multiagent2/server:latest ./server
docker build -t ghcr.io/ayanmw/multiagent2/web:latest ./web
docker build -t ghcr.io/ayanmw/multiagent2/gateway:latest ./tool/workbuddyLLMAPI

docker push ghcr.io/ayanmw/multiagent2/server:latest
docker push ghcr.io/ayanmw/multiagent2/web:latest
docker push ghcr.io/ayanmw/multiagent2/gateway:latest
```

> 生产建议打版本 tag（如 `server:v0.15.1`）而非 `latest`，便于回滚。
> 若在 CI 推送，需接入 `docker/login-action` + 仓库 secrets（`GHCR_TOKEN` 等）。

### 2.3 更新集群镜像

```sh
kubectl set image deployment/backend backend=ghcr.io/ayanmw/multiagent2/server:v0.15.1
kubectl set image deployment/web web=ghcr.io/ayanmw/multiagent2/web:v0.15.1
# 滚动更新后检查
kubectl rollout status deployment/backend
```

---

## 3. 密钥管理（生产必读）

### 3.1 最小密钥集

| 密钥 | 生成 | 作用 |
|------|------|------|
| `JWT_SECRET` | `openssl rand -hex 32` | JWT 签名（登录/APIKey 会话） |
| `PROVIDER_ENC_KEY` | `openssl rand -hex 32` | AES-256-GCM 主密钥（加密 Provider APIKey / MCP env/headers），**建议独立于 JWT_SECRET** |
| `ALERT_WEBHOOK_TOKEN` | `openssl rand -hex 32` | Alertmanager → `/api/alerts` 的 Bearer 共享密钥（**开启后两侧必须一致**） |

### 3.2 替换 k8s 占位符

`k8s/secret.yaml` 内为占位符，**严禁带真实值提交仓库**。生产做法：

```sh
kubectl create secret generic go-multi-agent-secrets \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=PROVIDER_ENC_KEY="$(openssl rand -hex 32)" \
  --from-literal=ALERT_WEBHOOK_TOKEN="$(openssl rand -hex 32)" \
  --dry-run=client -o yaml | kubectl apply -f -
```

> 推荐升级：**sealed-secrets**（Bitnami，加密后 Secret 可安全入库）或 **external-secrets / CSI Secret Store** 对接云 KMS，彻底告别「明文 secret 进仓库」。

### 3.3 Alertmanager 联动

开启 `ALERT_WEBHOOK_TOKEN` 后，`monitoring/alertmanager.yml` 的 webhook receiver 必须带同令牌：

```yaml
receivers:
  - name: codeagent-notify
    webhook_configs:
      - url: "http://server:8080/api/alerts"
        send_resolved: true
        http_config:
          authorization:
            type: Bearer
            credentials: "<ALERT_WEBHOOK_TOKEN>"
```

告警通知目标默认投递到 `ALERT_NOTIFY_USER_IDS=1`（管理员），可逗号分隔多用户。

---

## 4. K8s 部署主线（快速开始）

> 全部清单在 `k8s/`，对象名与相互引用已对齐（backend 的 Service 名为 `server`，与前端 nginx.conf 的 `http://server:8080` 匹配，**不可改名**）。

```sh
# 0) 前置：把 k8s/secret.yaml 换成真实密钥（见 §3.2，或直接 create secret）

# 1) 配置与共享技能
kubectl apply -f k8s/configmap.yaml          # 应用配置（PORT/路径/开关/跨域）
bash k8s/gen-skills-configmap.sh             # 把仓库 skills/ 生成 ConfigMap（warm-start 共享技能）

# 2) 数据卷（SQLite + workspaces + agent-state + 私有技能，10Gi）
kubectl apply -f k8s/pvc.yaml

# 3) 后端（Deployment 单副本 + liveness/readiness /health + PVC + skills ConfigMap）
kubectl apply -f k8s/backend-deployment.yaml
kubectl apply -f k8s/backend-service.yaml    # Service 名 = server（固定）

# 4) 前端（静态站 + HPA）
kubectl apply -f k8s/web-deployment.yaml
kubectl apply -f k8s/web-service.yaml
kubectl apply -f k8s/hpa-web.yaml            # CPU 70% 目标，1~5 副本

# 5) 入口（域名改 k8s/ingress.yaml 的 host 后再 apply）
kubectl apply -f k8s/ingress.yaml

# 6) 验证
kubectl get pods -w                          # backend/web 均 Running 且 Ready
curl -s https://agent.example.com/health     # 200
curl -s https://agent.example.com/metrics    # Prometheus 文本（codeagent_llm_calls_total 等）
```

### 4.1 要点与坑

- **backend 保持 1 副本**：本地 SQLite 单文件，多副本并发写冲突。横向扩展需先切 PG（`M8-10` 条件触发），届时再放开 replicas/HPA。
- **Service 名固定 `server`**：`web/nginx.conf` 与 `monitoring/*.yml` 均写死 `server:8080`，改名会导致前端 502、Prometheus 抓取失败。
- **SSE 不缓冲**：`ingress.yaml` 已带 `proxy-buffering: "off"` + `proxy-read-timeout: 3600`，勿删，否则流式对话断流。
- **ingress 路径分流**：`/api`、`/health`、`/metrics`、`/.well-known/agent.json` → server；其余 → web。
- **skills ConfigMap 变更**：改 `skills/` 后重跑 `gen-skills-configmap.sh` 并 `kubectl rollout restart deployment/backend` 生效。
- **HTTPS**：`ingress.yaml` 底部有注释的 `tls` 段，配 cert-manager 或手动 secret 后取消注释。
- **跨域**：同域名经 ingress 访问为同源；若前端独立域名，改 `configmap.yaml` 的 `CORS_ALLOWED_ORIGINS`。

---

## 5. 可观测性（Prometheus + Alertmanager + Grafana）

后端默认 `METRICS_ENABLED=true`，暴露 `/metrics`（Prometheus 文本）。监控编排在 `monitoring/`。

### 5.1 方式 A：K8s 内（推荐生产）

把 Prometheus / Alertmanager / Grafana / Loki / Promtail 以 Deployment 部署到集群，`monitoring/prometheus.yml` 的 scrape 目标 `server:8080` 与 alertmanager receiver `http://server:8080/api/alerts` **无需修改**（同命名空间 DNS 直连）。加载方式：

```sh
kubectl create configmap codeagent-prometheus \
  --from-file=prometheus.yml=monitoring/prometheus.yml \
  --from-file=alert-rules.yml=monitoring/alert-rules.yml
kubectl create configmap codeagent-alertmanager \
  --from-file=alertmanager.yml=monitoring/alertmanager.yml
# ... 再 apply Prometheus/Alertmanager/Grafana Deployment + Service（可用 prometheus-operator / kube-prometheus-stack 替代手工部署）
```

> 若使用 Prometheus Operator / kube-prometheus-stack：把 `alert-rules.yml` 转成 `PrometheusRule` CRD、`prometheus.yml` 的 scrape 目标改 `ServiceMonitor` 即可，规则表达式原样复用。

### 5.2 方式 B：本地单机（快速体验）

```sh
# 1) 先起后端（本机）：
cd server && go run ./cmd/server
# 2) 把 monitoring/prometheus.yml 的 scrape 目标改为 host.docker.internal:8080；
#    alertmanager.yml 的 url 改为 http://host.docker.internal:8080/api/alerts
# 3) 拉起监控栈：
docker compose -f monitoring/docker-compose.monitoring.yml up -d
```

| 服务 | 地址 | 说明 |
|------|------|------|
| Prometheus | http://localhost:9090 | Status > Targets 确认 backend UP；Alerts 页看规则 |
| Alertmanager | http://localhost:9093 | 告警路由与接收器 |
| Grafana | http://localhost:3000 | 默认 `admin/admin`；**自动供应** Prometheus/Loki 数据源与「CodeAgent 运行监控概览」看板（`monitoring/grafana/provisioning/`） |
| Loki | http://localhost:3100 | 日志存储 |
| Promtail | — | docker.sock 服务发现，采集 `gm-*` / `codeagent-*` 容器 JSON 日志 |

### 5.3 告警闭环（M7-04）

`monitoring/alert-rules.yml` 六类规则 → Alertmanager → webhook → `server /api/alerts` → 通知中心（M4-07 统一出口）：

| 告警 | 信号 |
|------|------|
| CodeAgentHighLLMErrorRate | LLM 错误率 >10%（5m） |
| CodeAgentHighToolFailureRate | 工具执行失败率 >20%（5m） |
| CodeAgentHighLoopFailureRate | 自主 Loop 失败率 >30%（5m，critical） |
| CodeAgentBudgetExhausted | 15m 内预算拦截 |
| CodeAgentFrequentRestarts | 1h 内重启 >2 次（critical） |
| CodeAgentHighLLMLatencyP99 | LLM P99 时延 >10s |

规则校验有自动化保障：`server/internal/metrics/alertrules_test.go` 解析本文件并核对指标名确实由 `/metrics` 暴露，避免「规则写了但指标不存在」的静默失效。

---

## 6. 日志与 trace 贯通（M7-06）

### 6.1 配置

| 环境变量 | 默认 | 说明 |
|----------|------|------|
| `LOG_FORMAT` | `json` | `json`（生产，可被 Loki/ELK 集中采集）/ `text`（本地调试） |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

### 6.2 五层 span 树（一次对话）

`http.request` → `gateway.run/stream` → `engine.llm_run` → `executor.run`（含 decision/exit_code）→ `automation.run`（cron Loop 根 span）。每行 JSON 携带 `trace_id` / `request_id` / `span_id` / `parent_span_id`；HTTP 响应头回显 `X-Request-ID` 与 `traceparent`，外部调用方可凭 trace 检索整条链路。

### 6.3 Loki 查询（Grafana Explore）

```logql
{container="gm-server"} | json | trace_id="<trace_id>"            # 串联一次对话全部日志
{container="gm-server"} | json | decision="denied"                 # 错误下钻：被安全策略拒绝的命令
{container="gm-server"} | json | span_name="automation.run" | line_format "{{.automation_name}} {{.status}} {{.attempts}}"
```

完整 LogQL 示例与 OTel Collector 升级路径见 `monitoring/logging.md`。

---

## 7. 升级 / 回滚 / 备份

### 7.1 数据库备份（SQLite 单文件）

```sh
# 在线备份（后端运行中）：SQLite 备份 API 比直接 cp 更安全
kubectl exec deploy/backend -- sh -c 'cd /data && sqlite3 codeagent.db ".backup backup-$(date +%F).db"' 2>/dev/null \
  || kubectl exec deploy/backend -- sh -c 'cp /data/codeagent.db /data/backup-$(date +%F).db'
kubectl cp backend/<pod>:/data/backup-*.db ./        # 拉回本机异地保存
```

更推荐：**PVC 快照**（云厂商 CSI 快照）或定期把整个 `/data` 卷 tar 走：

```sh
kubectl exec deploy/backend -- tar czf /tmp/data-$(date +%F).tar.gz -C /data .
kubectl cp backend/<pod>:/tmp/data-xxx.tar.gz ./
```

> `/data` 含 SQLite、workspaces（用户代码）、agent-state（Loop 状态 artifact）、私有技能，**一键恢复**即整个卷。

### 7.2 升级

```sh
kubectl set image deployment/backend backend=ghcr.io/ayanmw/multiagent2/server:<新版本>
kubectl rollout status deployment/backend
# 数据库结构变更由 schema_migrations 版本表自动应用（见 README「数据库与迁移」），
# 升级前建议先备份（§7.1），迁移失败时回滚镜像即可（迁移函数幂等）。
```

### 7.3 回滚

```sh
kubectl rollout undo deployment/backend      # 回到上一版本
kubectl rollout history deployment/backend   # 查看历史版本
```

---

## 8. 排障清单

| 症状 | 排查 | 处置 |
|------|------|------|
| `/health` 不返回 200 | 后端日志（`kubectl logs deploy/backend`） | 检查 JWT_SECRET 是否空、PVC 是否挂载成功（`kubectl describe pod` 看 Events） |
| 前端页面 502 | `server` Service 是否存在、Selector 是否匹配 | `kubectl get svc server`；Service 名固定为 `server`（§4.1） |
| 流式对话断流/无逐字 | ingress 缓冲未关 | 确认 `proxy-buffering: off` + read-timeout 3600（§4.1） |
| 登录后 403 | RBAC/密钥 | `JWT_SECRET` 变更后旧 token 失效，重新登录 |
| Prometheus Targets DOWN | scrape 目标地址 | 同命名空间用 `server:8080`；本地用 `host.docker.internal:8080`（§5.1/5.2） |
| 看板无数据 | 后端未开 `METRICS_ENABLED` | 置 `true`（默认开）；`curl /metrics` 应返回指标 |
| 告警收不到 | token 两侧不一致 | `ALERT_WEBHOOK_TOKEN` 与 `alertmanager.yml` 的 credentials 必须一致（§3.3） |
| 日志无 trace 串联 | 日志格式 | `LOG_FORMAT=json`；Loki 查询用 `{container="gm-server"} \| json`（§6.3） |
| 后端反复重启 | OOM / 探针失败 | `kubectl describe pod`；告警 CodeAgentFrequentRestarts 已覆盖此信号（§5.3） |
| warm-start 不命中技能 | skills ConfigMap 缺失/过期 | 重跑 `k8s/gen-skills-configmap.sh` 并 `rollout restart`（§4.1） |

---

## 9. 相关文件索引

| 用途 | 路径 |
|------|------|
| CI 流水线 | `.github/workflows/ci.yml` |
| K8s 清单 | `k8s/`（configmap/secret/pvc/backend/web/ingress/hpa + gen-skills-configmap.sh） |
| 监控编排（本地） | `monitoring/docker-compose.monitoring.yml` |
| Prometheus / 告警规则 / Alertmanager | `monitoring/prometheus.yml` / `alert-rules.yml` / `alertmanager.yml` |
| Grafana 看板与数据源供应 | `monitoring/grafana/provisioning/` |
| 日志聚合说明（LogQL/OTel 升级） | `monitoring/logging.md` |
| 一键三服务（单机/测试） | `docker-compose.yml` + `.env.example` |
