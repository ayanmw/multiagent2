package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/skillrepo"
)

// repoSkillsDir 解析仓库根目录的共享技能库（默认 SKILLS_ROOT = <cwd>/skills）。
// Go 测试以包目录为 CWD（server/internal/engine），向上三级即到仓库根。
// 用 runtime.Caller 兜底，确保无论从何处运行都能定位真实技能库；找不到则 Skip。
func repoSkillsDir(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "..", "skills"),
	}
	if _, srcFile, _, ok := runtime.Caller(0); ok {
		root := filepath.Join(filepath.Dir(srcFile), "..", "..", "..")
		candidates = append(candidates, filepath.Join(root, "skills"))
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
				return abs
			}
		}
	}
	t.Skip("仓库 skills/ 目录不可达（非标准布局），跳过真实命中 E2E")
	return ""
}

// TestEngine_SkillWarmStart_RealHit_E2E 验证「种子技能库 + warm-start 真实命中」：
//  1. 仓库 skills/ 确实入库 ≥3 个真实技能；
//  2. 开启 warm-start 后，相关 SKILL.md 正文被注入根 Agent 系统指令，并真实抵达 LLM
//     请求边界（mock 捕获 system 消息断言）；
//  3. 关键词命中真实生效：提供 "git" 关键词时只展开 git-flow，不展开 go-build；
//  4. 端到端交付：mock 依据 system 提示中的技能标记返回差异化响应，证明技能上下文
//     确实送达模型（真实 LLM 下即「模型遵循」；此处以 delivery 链路证明）。
func TestEngine_SkillWarmStart_RealHit_E2E(t *testing.T) {
	skillsDir := repoSkillsDir(t)

	// (1) 仓库技能库必须有 ≥3 个真实技能（强制约束，缺失即失败）。
	mgr := skillrepo.NewManager(skillsDir, t.TempDir())
	all, err := mgr.List("seed-check-user")
	if err != nil {
		t.Fatalf("列技能失败: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("仓库技能库应 ≥3 个真实技能，实际 %d: %+v", len(all), all)
	}
	t.Logf("✅ 仓库技能库已入库 %d 个技能", len(all))

	// mock LLM：捕获 system 消息，并按是否含 git-flow 标记差异化回复（端到端交付证明）。
	var capturedSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body map[string]any
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		if msgs, ok := body["messages"].([]any); ok {
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok {
					if mm["role"] == "system" {
						if c, ok := mm["content"].(string); ok {
							capturedSystem = c
						}
					}
				}
			}
		}
		// 差异化回复：system 提示含 git-flow 技能 → 返回 git 工作流感知回复。
		reply := "我已了解你的请求"
		if strings.Contains(capturedSystem, "git-flow") {
			reply = "我将按 git-flow 工作流：在 feature 分支开发、提交遵循约定式规范、经评审后 merge 回 main。"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for _, ch := range []string{
			`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"` + reply + `"},"finish_reason":null}]}`,
			`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			fmt.Fprintf(w, "%s\n\n", ch)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	// (2)(4) 无关键词：注入全部技能，捕获 system 断言含技能块与某技能正文；
	// 并验证 mock 因 git-flow 标记返回 git 工作流感知回复（端到端交付）。
	capturedSystem = ""
	eng, err := New(ModelConfig{
		ModelID:        "mock-model",
		BaseURL:        srv.URL,
		APIKey:         "k",
		Protocol:       "openai",
		SkillWarmStart: true,
		SkillRoots:     []string{skillsDir},
		SkillMaxChars:  6000,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reply, err := eng.Chat(context.Background(), "sess-ws", "帮我初始化一个新功能分支", nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	eng.Close()

	if !strings.Contains(capturedSystem, "【可用技能 Skills（warm-start）】") {
		t.Fatalf("system 指令未注入 warm-start 技能块: %s", capturedSystem)
	}
	if !strings.Contains(capturedSystem, "git-flow") {
		t.Fatalf("system 指令未含 git-flow 索引: %s", capturedSystem)
	}
	if !strings.Contains(capturedSystem, "Git 工作流规范") {
		t.Fatalf("system 指令未含 git-flow 技能正文（注入未真实抵达 LLM）: %s", capturedSystem)
	}
	if !strings.Contains(reply, "git-flow 工作流") {
		t.Fatalf("模型未收到技能上下文（端到端交付失败）: reply=%q", reply)
	}
	t.Logf("✅ 无关键词：全部技能注入并抵达 LLM，回复遵循技能: %q", reply)

	// (3) 关键词命中：仅 "git" → 展开 git-flow，不展开 go-build 正文。
	capturedSystem = ""
	eng2, err := New(ModelConfig{
		ModelID:        "mock-model",
		BaseURL:        srv.URL,
		APIKey:         "k",
		Protocol:       "openai",
		SkillWarmStart: true,
		SkillRoots:     []string{skillsDir},
		SkillKeywords:  []string{"git"},
		SkillMaxChars:  6000,
	})
	if err != nil {
		t.Fatalf("New kw: %v", err)
	}
	if _, err := eng2.Chat(context.Background(), "sess-ws2", "提交代码", nil); err != nil {
		t.Fatalf("Chat kw: %v", err)
	}
	eng2.Close()
	if !strings.Contains(capturedSystem, "Git 工作流规范") {
		t.Fatalf("关键词 git 应展开 git-flow 正文: %s", capturedSystem)
	}
	if strings.Contains(capturedSystem, "Go 构建与测试规范") {
		t.Fatalf("关键词 git 不应展开 go-build 正文: %s", capturedSystem)
	}
	t.Logf("✅ 关键词 git 真实命中：仅展开 git-flow，go-build 未展开")
}
