---
name: example
description: 示例技能，演示 Skills 仓库的结构与 warm-start 机制；新会话会自动把相关技能注入系统上下文。
---
# Example Skill（示例技能）

这是一个示例技能，用于演示 `skills/` 仓库的结构，以及如何让 Agent 在会话开始时
自动「带着技能知识」开工（warm-start）。

## 何时使用

- 需要向用户说明「技能（Skill）」机制时；
- 作为你新建技能的模板时。

## 文件约定

- 每个技能是一个独立目录，目录名即技能名（仅允许 `A-Za-z0-9_-`）；
- 目录下必须有 `SKILL.md`；
- `SKILL.md` 顶部可选 YAML front matter：`name` / `description`；
- front matter 之后的 Markdown 即为技能正文，warm-start 会把正文注入 Orchestrator 系统提示词。

## 两类技能根

- **共享技能**：写在仓库 `skills/<name>/SKILL.md`，对所有用户可见、只读，通常由内置/管理员维护；
- **私有技能**：写在 `data/skills/<uid>/<name>/SKILL.md`，经 API（`/api/skills`）增删改，按用户隔离，互不可见。

## 长度控制

warm-start 注入受 `SKILL_WARM_START_MAX_CHARS`（默认 6000）上限约束：未提供关键词时注入全部技能
（受上限截断），提供关键词时仅展开命中的技能，避免上下文被技能内容撑爆。
