---
name: conventional-commits
description: 约定式提交（Conventional Commits）规范；当需要写提交说明、生成 changelog 或规范 commit message 时使用。
---
# 约定式提交规范（conventional-commits）

提交说明格式：`<type>(<scope>): <subject>`，正文与脚注可选。

## 类型（type）
- `feat`：新功能
- `fix`：缺陷修复
- `docs`：文档
- `refactor`：重构（不改变外部行为）
- `test`：测试
- `chore`：构建/工具/杂项
- `perf`：性能优化
- `style`：格式（不影响逻辑）

## 范围（scope）
对应里程碑/模块，如 `M6-04`、`engine`、`api`、`skillrepo`。

## 示例
- `feat(M6-04): 种子技能库加上 warm-start 真实命中 E2E 测试`
- `fix(engine): 修复多轮记忆回灌越界`

## 规则
- subject 使用祈使句、简洁、不超过 50 字。
- 一次提交只做一件事，便于回滚与评审。
- 与 git-flow 技能配合：提交说明即 PR/合并记录的核心线索。
