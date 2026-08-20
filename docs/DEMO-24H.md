# 24h 自主演示复现手册

> 目标：让一名**新人**按本文档独立复现「goMultiAgentV2 对标 OpenClaw + Claude 的 24h 无人值守自主工作」演示。
> 配套：[docs/ARCHITECTURE.md](./ARCHITECTURE.md)（架构图）、[docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md)（运维）、[examples/automations/](./examples/automations/)（可拷贝示例）。

---

## 0. 演示一句话总结

平台在**无人值守**（`RUN_MODE=unattended`）下，由定时/事件触发 Agent Team（Orchestrator→Coder→Reviewer），自动把「一个目标」拆成子任务、在 git worktree 隔离目录里写代码、自动 review、merge 回主分支、记录审计与预算、并在完成时回发通知——**全程不需人盯，可跨天恢复续跑**。

---

## 1. 前置条件

| 项 | 要求 |
|----|------|
| Go | 1.25+（**无需 C 编译器**，纯 Go SQLite） |
| Node | 22+（前端） |
| LLM 网关 | 二选一：① 本机已登录 WorkBuddy/CodeBuddy 桌面 + `tool/workbuddyLLMAPI`；② 任意 OpenAI 兼容端点（`passthrough` 后端） |
| git | 运行时需可用（worktree/commit 依赖） |

---

## 2. 快速部署（Docker Compose，演示首选）

```sh
# 1) 环境变量
cp .env.example .env
#   编辑 .env：JWT_SECRET / PROVIDER_ENC_KEY 设为随机值；
#   若用 passthrough 网关，设 WB_BASE_URL / WB_API_KEY

# 2) 启动三服务
docker compose up -d --build

# 3) 验证
curl -s http://localhost:8080/health      # 200
curl -s http://localhost:8081             # 前端首页
curl -s http://localhost:8088/healthz     # 网关
```

> 纯演示也可走「手动启动」：`server` + `web` + 本地网关分别起（见 README「快速开始」）。

---

## 3. 配置 LLM Provider

前端登录（默认 `docker-compose` 起的服务，访问 `http://localhost:8081`）→「Provider 管理」新增：

- `base_url` = `http://gateway:8088/v1`（容器内经网关）或 `http://localhost:8088/v1`（本机联调）
- `api_key` = 任意非空（codebuddy 后端不校验；passthrough 填真实 key）
- `protocol` = `openai`
- 保存后点「测试连接 / 同步模型」→ 启用至少一个模型。

---

## 4. 创建「24h 自主」自动化

进入「自动化」页 → 新建：

- **名称**：`24h-self-improve`
- **触发**：Cron `0 * * * *`（每小时整点）或 Webhook
- **Goal Prompt**（示例，见 `examples/automations/24h-self-improve.md`）：

```
你是平台的自改进 Agent。请每轮：
1. 读取仓库 docs/loop/PLAN.md 与 PROGRESS.md；
2. 挑选一个待办或技术债，用 taskrun 在 worktree 隔离目录实现；
3. 提交并 merge 回主分支（不要 push 远程）；
4. 更新 PROGRESS.md，写清改动与验证；
5. 若全部待办清零，输出 STOP 摘要。
遵守安全约束：危险命令进检查点，不破坏现有测试。
```

启用后，自动化调度器在整点创建 Goal Session 并启动 Loop。`Gateway` 用稳定 `session_id` + 串行锁保证不串会话，跨进程重启可恢复续跑。

---

## 5. 观察自主运行

- **前端「运行监控」**：实时调用量 / 时延 / 错误率 / Active Loops / 检查点堆积。
- **Prometheus**：`http://localhost:9090` → 查 `codeagent_loop_runs_total` / `llm_calls_total` / `budget_exhausted_total`。
- **审计日志**：「审计」页可见每一条 shell/file 执行（owner 隔离）。
- **Artifact 浏览器**：查看某 session 的 `PLAN/PROGRESS/LEARNINGS` 与 diff。
- **通知中心**：Loop 完成 / 需检查点时收到站内信。

---

## 6. 典型场景案例

### 场景 A：定时代码自查与修复（最贴近 24h 演示）

**触发**：Cron 每小时。
**Goal**：扫描 `internal/executor` 找可优化点 → Coder 在 worktree 写修复 → Reviewer 只读审阅 → merge。
**验证点**：主分支 `git log --oneline` 出现 `feat(auto): ...`；`go build ./...` 仍通过；审计页出现对应执行记录。
**复现脚本**：见 `examples/automations/24h-self-improve.md`。

### 场景 B：IM 触发需求落地

**触发**：飞书/钉钉/企微机器人 webhook（在「IM 绑定」页绑定后，向机器人发消息即触发）。
**Goal**：「帮我在 web/ 加一个 X 页面骨架」→ Orchestrator 委托 Coder → 完成后结果回发 IM。
**验证点**：IM 收到完成回发；对应 session 在「会话」页可见；worktree 已 merge。
**前提**：配置 `IM_FEISHU_WEBHOOK_URL` / `IM_DINGTALK_*` / `IM_WECOM_*` 且绑定。

### 场景 C：技能进化飞轮

**触发**：后台 `EVOLUTION_ENABLED=true` 每小时扫描已结束 session。
**流程**：transcript → 提取候选 `SKILL.md` → 质量门控（长度/结构/去重）→「进化」页待审批 → 你 approve → 进 `skills/` 共享库 → 新会话 warm-start 自动命中。
**验证点**：跑完一个典型任务后「进化」页出现候选；审批后新建会话的 system 上下文含该技能；评估集自举自动生成回归用例。
**复现**：`examples/automations/skill-flywheel.md`。

### 场景 D：知识库 RAG 问答

**触发**：普通对话。
**流程**：上传文档到知识库 → 新会话按关键词检索注入上下文（控长）。
**验证点**：问一个文档内的问题，回答引用了文档内容。
**扩展**：大规模文档可切 PG/pgvector（`KB_STORE=pgvector`，见 `docs/knowledge-pgvector.md`）。

---

## 7. 演示视频分镜脚本（Storyboard）

> 说明：本仓库为文本型代码库，**实际视频渲染需由人工/剪辑工具按此脚本生成**（超出自主文本 Agent 范围）。脚本按「30–60 秒产品 Demo」节奏设计，可直接交剪辑。

| 镜头 | 画面 | 旁白/字幕 | 时长 |
|------|------|-----------|------|
| 1 | 标题页：goMultiAgentV2 — 24h 无人值守多 Agent 平台 | 「不止问答，自我驱动」 | 3s |
| 2 | 架构图（取自 ARCHITECTURE.md §1）高亮 Channel→Gateway→Team | 「统一入口接收定时、Webhook、IM 触发」 | 5s |
| 3 | 前端「自动化」页新建 `24h-self-improve` cron | 「配置一个每小时自改进的目标」 | 5s |
| 4 | 终端 `docker compose up` + `/health` 200 | 「一条命令起三服务」 | 4s |
| 5 | 录屏：到点自动建 session，消息逐字流式输出（AG-UI SSE） | 「整点自动启动 Loop，Agent 流式推进」 | 8s |
| 6 | 「运行监控」卡片：调用量/错误率/Active Loops | 「全程可观测」 | 4s |
| 7 | 「审计」页滚动 + 「Artifact 浏览器」看 diff | 「每次执行留痕，代码变更可回溯」 | 5s |
| 8 | 后端日志：`git merge --no-ff` + `git log` 新提交 | 「子任务在 worktree 隔离，自动 merge 回主分支」 | 6s |
| 9 | 「通知中心」弹出完成通知 | 「完成自动回发，无需人盯」 | 4s |
| 10 | 结尾：GitHub 仓库 + 文档链接 | 「开源复现：github.com/ayanmw/multiagent2」 | 3s |

---

## 8. 排障清单

| 现象 | 排查 |
|------|------|
| 定时没触发 | 确认自动化已「启用」；`RUN_MODE=unattended`；看 server 日志 `next_run` 更新 |
| Loop 卡住无输出 | 危险命令进了检查点（无人值守 deny）→ 去「检查点」页审批；或看是否预算耗尽 |
| 工具调用无效（真实模型） | 本地 codebuddy 网关不支持 function calling，工具链 E2E 需脚本化 mock 驱动（见 LEARNINGS） |
| worktree 产物丢失 | 并发 merge 竞态（已修 mergeMu）；断言产物须轮询等待（见 LEARNINGS M7.5-03） |
| 跨重启丢进度 | 确认 `STATE_ENABLED=true` 且 `DB_AUTO_MIGRATE=false`（走版本迁移） |

---

## 9. 一键复现清单（Checklist）

- [ ] `docker compose up -d --build` 三服务起得来
- [ ] `/health` / `/healthz` 返回 200
- [ ] Provider 配置 + 模型启用，测试连接通过
- [ ] 创建 `24h-self-improve` 自动化并启用
- [ ] 等一个整点，前端看到新 session 自动运行
- [ ] 运行监控有指标、审计页有执行记录
- [ ] 主分支 `git log` 出现自动提交
- [ ] 通知中心收到完成通知

完成以上即复现「24h 自主演示」。
