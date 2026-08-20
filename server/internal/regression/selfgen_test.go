package regression

// M8-05「评估集自举」测试：从技能正文反向生成 eval 用例的纯函数解析。
// 不依赖 LLM / DB，直接断言解析出的用例结构与断言质量。

import (
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// gitFlowLikeBody 是贴近 skills/git-flow/SKILL.md 的样例正文（含 front matter、标题、
// 行内代码命令与尖括号参数占位），覆盖三种用例生成路径。
const gitFlowLikeBody = `---
name: git-flow
description: Git 分支模型、提交规范与 PR/合并协作流程。
---
# Git 工作流规范（git-flow）

本技能规范本仓库的 Git 协作方式，所有代码变更必须经此流程。

## 分支策略
- main：受保护主干，禁止直接 git push。
- 后台任务隔离：每个 taskrun 派生独立 git worktree add <dir> -b taskrun/<id>，完成后本地 commit 再 merge 回主分支（只 merge 不直接 push 远程）。

## 提交规范
- 提交说明遵循约定式提交（类型(范围): 简述），每次提交用 git commit -m 落盘。

## PR / 合并流程
1. 功能分支开发完成后通过 go build / go vet / go test。
2. 本地 git commit，由 post-commit hook 自动 push。

## 冲突处理
- Worktree 隔离场景下冲突仅在 merge 阶段出现，用 git merge --no-ff 合并。
`

// cmdRichBody 是带反引号命令的真实技能正文（raw string 不能含反引号，故拼接补充）。
const cmdRichBody = gitFlowLikeBody + "\n## 命令示例\n- 执行 `git worktree add <dir> -b taskrun/<id>` 与 `go build ./...` 完成隔离开发与构建验证。\n"

// TestGenerateCases_FromSkillBody 验收三种用例生成路径（保底/标题/命令）全部命中。
func TestGenerateCases_FromSkillBody(t *testing.T) {
	cand := &model.SkillCandidate{Name: "git-flow", Description: "Git 分支模型、提交规范与 PR/合并协作流程", Body: cmdRichBody}
	cases, err := GenerateCases(cand)
	if err != nil {
		t.Fatalf("GenerateCases: %v", err)
	}
	if len(cases) < 4 {
		t.Fatalf("完整技能应生成 ≥4 条用例（保底+标题+命令），实际 %d", len(cases))
	}
	if len(cases) > MaxCasesPerSkill {
		t.Fatalf("用例数 %d 超过上限 %d", len(cases), MaxCasesPerSkill)
	}

	// 保底知识用例：Expected=技能名。
	var hasBaseline bool
	for _, c := range cases {
		if c.Expected == "git-flow" {
			hasBaseline = true
		}
	}
	if !hasBaseline {
		t.Fatal("缺少保底知识用例（Expected=技能名）")
	}

	// 标题用例：Expected 取标题的最长 CJK 片段（如「分支策略」「合并流程」）。
	var hasHeading bool
	for _, c := range cases {
		if c.Expected == "分支策略" || c.Expected == "合并流程" || c.Expected == "冲突处理" {
			hasHeading = true
		}
	}
	if !hasHeading {
		t.Fatal("缺少标题用例（Expected 应为标题的 CJK 关键词）")
	}

	// 命令用例：Expected 取命令词干（git worktree / git commit / git merge / go build）。
	var hasCmd bool
	for _, c := range cases {
		if c.Expected == "git worktree" || c.Expected == "git merge" || c.Expected == "go build" {
			hasCmd = true
		}
	}
	if !hasCmd {
		t.Fatal("缺少命令用例（Expected 应为命令词干）")
	}

	// 全部用例约束：Input/Expected 非空、Grader=contains（召回式，防 flaky）。
	for i := range cases {
		c := cases[i]
		if strings.TrimSpace(c.Input) == "" || strings.TrimSpace(c.Expected) == "" {
			t.Fatalf("用例 #%d Input/Expected 不能为空: %+v", i, c)
		}
		if c.Grader != model.GraderContains {
			t.Fatalf("用例 #%d Grader 应为 contains，实际 %q", i, c.Grader)
		}
	}
}

// TestGenerateCases_TrivialBody 验收琐碎正文（无标题/命令）也至少产出保底用例，
// 保证「自动进评估集」不因正文格式异常而中断。
func TestGenerateCases_TrivialBody(t *testing.T) {
	cand := &model.SkillCandidate{Name: "deploy_docker", Description: "部署到 docker", Body: "x"}
	cases, err := GenerateCases(cand)
	if err != nil {
		t.Fatalf("GenerateCases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("琐碎正文应只生成保底 1 条用例，实际 %d", len(cases))
	}
	if cases[0].Expected != "deploy_docker" {
		t.Fatalf("保底用例 Expected 应为技能名，实际 %q", cases[0].Expected)
	}
}

// TestGenerateCases_CaseLimit 验收超长技能被截断到上限（防回归时长失控）。
func TestGenerateCases_CaseLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("## 阶段甲甲乙乙丙丙\n- 执行 `git 命令" + string(rune('a'+i)) + "` 完成操作。\n")
	}
	cand := &model.SkillCandidate{Name: "big-skill", Description: "d", Body: b.String()}
	cases, err := GenerateCases(cand)
	if err != nil {
		t.Fatalf("GenerateCases: %v", err)
	}
	if len(cases) > MaxCasesPerSkill {
		t.Fatalf("超长技能用例数 %d 超过上限 %d", len(cases), MaxCasesPerSkill)
	}
}

// TestGenerateCases_NilOrEmptyName 验收非法输入返回明确错误。
func TestGenerateCases_NilOrEmptyName(t *testing.T) {
	if _, err := GenerateCases(nil); err == nil {
		t.Fatal("nil 候选应返回错误")
	}
	if _, err := GenerateCases(&model.SkillCandidate{Name: "  ", Body: "x"}); err == nil {
		t.Fatal("空技能名应返回错误")
	}
}

// TestCommandStem 验收命令词干提取：去掉尖括号参数、只取前两个字母开头的词、
// 单命令词（太泛）返回空。
func TestCommandStem(t *testing.T) {
	tests := []struct{ in, want string }{
		{"`git worktree add <dir> -b taskrun/<id>`", "git worktree"},
		{"`git merge --no-ff`", "git merge"},
		{"`go build ./...`", "go build"},
		{"`main`", ""},        // 单词太泛
		{"`<dir>`", ""},       // 纯参数占位
		{"`rm -rf`", ""},      // 第二词是 flag（-rf），非子命令
	}
	for _, tt := range tests {
		code := strings.Trim(tt.in, "`")
		got := commandStem(code)
		if got != tt.want {
			t.Errorf("commandStem(%q) = %q，期望 %q", code, got, tt.want)
		}
	}
}

// TestHeadingKey 验收标题关键词：优先最长 CJK 片段，纯英文取首英文词，兜底原文。
func TestHeadingKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"分支策略", "分支策略"},
		{"PR / 合并流程", "合并流程"}, // 最长 CJK 优先（4 字 > PR）
		{"Setup", "Setup"},
		{"API 设计", "设计"},
	}
	for _, tt := range tests {
		if got := headingKey(tt.in); got != tt.want {
			t.Errorf("headingKey(%q) = %q，期望 %q", tt.in, got, tt.want)
		}
	}
}
