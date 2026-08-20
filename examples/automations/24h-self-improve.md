# 示例：24h 自主自改进自动化（cron）

对标 OpenClaw + Claude 的「24h 无人值守自主工作」核心演示。每小时整点触发一次，Agent Team 自动挑选待办、在 worktree 隔离实现、review、merge、写日志。

---

## 1. 创建自动化

```sh
curl -s -X POST http://localhost:8080/api/automations \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "24h-self-improve",
    "trigger_type": "cron",
    "cron_expr": "0 * * * *",
    "enabled": true,
    "goal_prompt": "你是 goMultiAgentV2 平台的自改进 Agent，运行在无人值守模式。请每轮按以下契约推进：\n1. 读取仓库 docs/loop/PLAN.md 与 PROGRESS.md，挑选第一个待办或一处明确的技术债；\n2. 用 start_task_run 在 git worktree 隔离目录中实现改动，遵循仓库 LEARNINGS.md 的约定（所有代码执行经 SafeExecutor、文件工具经 resolveSafePath、不绕过 internal/engine）；\n3. 实现后 self-review（只读 grep/file_read），确保 go build ./... 与 go vet ./... 不引入错误；\n4. 在 worktree 内 git commit，由平台自动 merge 回主分支（不要 push 远程）；\n5. 更新 docs/loop/PROGRESS.md，写清改动、验证与下一步；\n6. 若 PLAN.md 待办已清零，输出 STOP 摘要并结束。\n安全约束：危险命令进检查点等待人工审批；不删除测试、不修改迁移真相源以外的约定文档；预算超限时立即停止并报告。"
  }'
```

成功后返回含 `id` 的视图，`next_run` 显示下一个整点。

---

## 2. 观察运行

```sh
# 列表查看 next_run 是否已排程
curl -s http://localhost:8080/api/automations \
  -H "Authorization: Bearer $TOKEN"

# 运行历史
curl -s http://localhost:8080/api/automations/<id>/runs \
  -H "Authorization: Bearer $TOKEN"
```

前端「自动化」页可看到该配置；「会话」页会按整点出现新的 Goal Session；「运行监控」有实时指标；「审计」页有执行留痕；「通知中心」在完成后收到站内信。

---

## 3. 暂停 / 恢复

```sh
# 暂停（避免演示环境持续运行）
curl -s -X PUT http://localhost:8080/api/automations/<id> \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled": false}'

# 恢复
curl -s -X PUT http://localhost:8080/api/automations/<id> \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled": true}'
```

---

## 4. 验证清单（复现 24h 演示）

- [ ] 创建后 `next_run` 为下一个整点
- [ ] 整点后「会话」页出现新 Goal Session 且流式输出
- [ ] 主分支 `git log --oneline` 出现自动提交（或 PROGRESS.md 被更新）
- [ ] 运行监控 / 审计 / 通知中心均有对应记录
- [ ] `enabled=false` 后不再新建 session

> 注意：真实模型经本地 codebuddy 网关不支持 function calling（见 LEARNINGS），工具链 E2E 需脚本化 mock 驱动；演示时建议用支持 OpenAI function calling 的端点（`WB_BACKEND=passthrough` 指向 vLLM/云厂商）以获得完整自动 coding 体验。
