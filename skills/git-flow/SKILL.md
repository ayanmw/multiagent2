---
name: git-flow
description: Git 分支模型、提交规范与 PR/合并协作流程；当用户涉及 git 提交、分支管理、合并冲突、PR 评审或 Worktree 隔离时使用。
---
# Git 工作流规范（git-flow）

本技能规范本仓库的 Git 协作方式，所有代码变更必须经此流程。

## 分支策略
- `main`：受保护主干，只接受经过评审的合并，禁止直接 `git push`（仅自动化 post-commit hook 推送已本地提交的代码）。
- 功能分支：`feature/<简短描述>`；修复分支：`fix/<描述>`；hotfix：`hotfix/<描述>`。
- 后台任务隔离：每个 taskrun 派生独立 `git worktree add <dir> -b taskrun/<id>`，完成后本地 commit 再 merge 回主分支（**只 merge 不直接 push 远程**，这是已确认决策）。

## 提交规范
- 提交说明遵循「类型(范围): 简述」的约定式提交（见 conventional-commits 技能）。
- 禁止 `git push --force`、`git reset --hard` 到共享分支（属高危命令，无人值守下被安全策略拒绝或进入人工检查点）。
- 每次提交应原子、可回滚，并关联对应任务编号（如 `feat(M6-04): ...`）。

## PR / 合并流程
1. 在功能分支完成开发并通过 `go build` / `go vet` / `go test`。
2. 本地 `git commit`，由 post-commit hook 自动 push 到远程同名分支。
3. 发起合并请求（人工或自动化 Reviewer 评审），冲突交 Reviewer 或人工处理。
4. 合并采用 `git merge`（保留分支历史），不删除主分支。

## 冲突处理
- Worktree 隔离场景下，主分支中途不应被污染；冲突仅在 merge 阶段出现，需人工/Reviewer 裁决。
