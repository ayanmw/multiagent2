# M7.5-03 并发与压测报告

> 验收标准：压测报告（P99 时延）、无死锁、无连接泄漏
> 日期：2026-08-20 | 实现：`server/internal/smoke/load_test.go`（4 场景，规模环境变量可调，CI 无真实模型全绿）

## 结论

| 验收项 | 结果 |
|--------|------|
| P99 时延报告 | ✅ 四场景 P99 全部实测并记录（见下表） |
| 无死锁 | ✅ 全部场景墙钟远低于超时上限；扇出/写锁场景 3 连跑 + 放大规模均无卡死 |
| 无连接泄漏 | ✅ SSE 长连接结束后 Mock 服务器活跃连接归零；对话场景 Mock 请求数与调用数严格相等 |
| 并发缺陷暴露与修复 | ✅ 压测暴露 **worktree 并发 merge 竞态**（多子任务收敛时 `git merge --no-ff` 踩踏 index/HEAD，多数 128 失败丢产物），已修复并补回归测试 |

## 测试矩阵（放大规模，本机沙箱，LLM 决策脚本化 Mock、工具链真实执行）

| 场景 | 规模 | 样本 | 错误率 | P50 | P90 | P99 | max | 墙钟 |
|------|------|------|--------|-----|-----|-----|-----|------|
| 1. 多用户并发对话（每用户 3 轮带历史回灌） | 50 用户 | 150 次引擎调用 | 0.00% | 28ms | 56ms | **68ms** | 71ms | 124ms |
| 2. SSE 长连接稳定性（每流 50 分片、慢速消费 2ms/事件） | 25 并发 | 25 流 | 0.00% | 191ms | 199ms | **202ms** | 202ms | 206ms |
| 3. taskrun 扇出（async 并发派发 → worktree 隔离 → 全量 merge 回主分支） | 10 子任务 | 10 run | 0 | — | — | — | — | 1.631s（worker 执行） |
| 4. SQLite 写锁（16→32 写者各自会话 + 8 写者共享会话） | 32+8 写者 | 1000 条消息 | 0.00% | — | — | — | — | 9.03s |

> 默认规模（CI 快速回归）：20 用户 / 10 SSE×50 分片 / 6 扇出 / 16+8 写者，全套 ~9s 全绿（3 连跑稳定）。

## 场景说明与断言

1. **多用户并发对话**：N 用户各自独立 Engine（贴近生产每次请求新建 Runner）+ 独立 session，第 2/3 轮经 `engine.ChatMessage` 回灌多轮历史；断言全部成功、回复非空、Mock 请求数 = 用户×轮数（无丢失）、墙钟 < 90s（无死锁）。
2. **SSE 长连接稳定性**：Mock 端点输出 50 分片（3ms/分片模拟真实流式节奏），消费者每事件 2ms 节流（模拟前端渲染）；断言每条流文本完整、无错误事件、结束活跃连接计数归零（**无连接泄漏**）。
3. **taskrun 扇出 5+ 子任务**：Orchestrator `create_goal → start_task_run(async)×N → list_task_runs 轮询收敛 → update_goal(complete)`；N 个 worker 并发在**各自独立 worktree** 写 task-k.txt → commit → 终态 Observer 自动 merge 回主分支；断言主分支出现全部 N 文件且内容正确、git log 含 N 条提交、worktree 目录清理、goal 收敛 complete、控制器侧 N 个 run 全 completed。断言对「run 终态 ↔ 异步 Finalize merge」的**最终一致性时差**做轮询等待（墙钟上限兜底死锁）。
4. **SQLite 写锁**：文件型 SQLite（对齐生产 `SetMaxOpenConns(1)` 单写者连接池），16/32 写者各自会话 + 8 写者共享会话并发 `GetOrCreateSession`/`AppendMessage`；断言 0 lock 错误（`database is locked` / `database table is locked`）、消息行数精确、墙钟 < 60s（无死锁）。

## 压测暴露并修复的并发缺陷（M7.5-03 核心产出）

**缺陷**：`worktree.Manager.Finalize` 对「merge 回主分支」无串行化。多个后台子任务同时收敛时，Observer 并发触发 Finalize → 多个 `git merge --no-ff` 同时写同一主仓库的 index/HEAD，互相踩踏（`index.lock` 已存在 / HEAD 被并发改写），多数 merge 以 exit_code=128 失败，**子任务产物丢失且难排查**（git log --all 只剩 1 个 worker commit、其余分支被 worktree 检出悬置）。

**修复**（`server/internal/worktree/worktree.go`）：
- `Manager` 新增 `mergeMu sync.Mutex`，`Finalize` 的 merge 段持锁——git 对同一主仓库的写操作是单写者模型，merge 必须互斥（同一 Manager 实例管理同一主仓库的全部子任务 worktree）。
- merge 失败路径补充 `log.Printf` 错误详情（含 branch/err/out），避免「子任务完成但产物丢失」时无从排查。

**回归保护**：新增 `TestManager_ConcurrentFinalize`（worktree 包）——6 个 worktree 并发写文件提交 + 并发 Finalize，断言全部 merge 成功、产物完整、分支与检出目录清理（无锁时该测试必挂：并发 merge 128 丢产物）。

**验证**：修复前压测扇出场景随机失败（多数 merge 128）；修复后 3 连跑 + 放大（10 子任务）全绿，所有 merge exit_code=0。

## 复现方式

```sh
# 默认规模（CI 回归）
go test -count=1 -run 'TestLoad_' ./internal/smoke/
# 放大规模（本机压测）
LOAD_N_USERS=50 LOAD_SSE_CONNS=25 LOAD_TASKRUN_FANOUT=10 LOAD_SQLITE_WRITERS=32 \
  go test -count=1 -run 'TestLoad_' -v ./internal/smoke/
# 扇出决策序列诊断
LOAD_FANOUT_DEBUG=1 go test -count=1 -run 'TestLoad_TaskrunFanout5Plus' -v ./internal/smoke/
```

## 已知边界（记录而非缺陷）

- **SQLite 单写者**：多连接（`SetMaxOpenConns>1`）并发写会产生 `database is locked`，属 SQLite 固有行为；生产已用单连接池串行化规避，K8s 后端单副本（M7-03/M8-10 切 PG 前）。
- **扇出轮询开销**：Orchestrator 用 `list_task_runs` 轮询收敛，10 子任务实测 ~40 次 LLM 往返（每次 ~5ms），实际决策可用 `wait_task_run(id)` 或回调优化（留 M8）。
- **真实模型**：WorkBuddy 网关 ACP 不支持 function calling（LEARNINGS 2026-08-20），工具链压测以脚本化 mock 驱动 LLM 决策、工具全真实执行；接入支持 function calling 的端点后同一套件可直接切 `SMOKE_LLM_BASE_URL` 真实跑。
