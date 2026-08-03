package command

import (
	"testing"
)

func TestBuiltin_ContainsRequiredCommands(t *testing.T) {
	cmds := Builtin()
	want := map[string]bool{
		"clear":     false,
		"model":     false,
		"workspace": false,
		"run":       false,
		"review":    false,
		"plan":      false,
	}
	for _, c := range cmds {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
		if c.Name == "" || c.Description == "" || c.Usage == "" || c.Kind == "" || c.Category == "" {
			t.Errorf("command %q has empty required metadata fields", c.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("builtin command %q missing from registry", name)
		}
	}
}

func TestBuiltin_NoDuplicateNames(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Builtin() {
		if seen[c.Name] {
			t.Errorf("duplicate command name %q", c.Name)
		}
		seen[c.Name] = true
	}
}

func TestFind(t *testing.T) {
	cmd, ok := Find("run")
	if !ok {
		t.Fatal("expected to find 'run'")
	}
	if cmd.Kind != KindPrompt {
		t.Errorf("run kind = %q, want %q", cmd.Kind, KindPrompt)
	}
	if _, ok := Find("nope"); ok {
		t.Error("expected Find to fail for unknown command")
	}
}

func TestRenderPrompt(t *testing.T) {
	run, _ := Find("run")
	got := RenderPrompt(run, "ls -la")
	want := "请在当前工作区执行以下 shell 命令，并汇报执行结果与输出：\nls -la"
	if got != want {
		t.Errorf("RenderPrompt(run) = %q, want %q", got, want)
	}

	review, _ := Find("review")
	if got := RenderPrompt(review, ""); got != review.Template {
		t.Errorf("RenderPrompt(review) should return template verbatim, got %q", got)
	}

	// 非 prompt 类不渲染
	clear, _ := Find("clear")
	if got := RenderPrompt(clear, "x"); got != "" {
		t.Errorf("RenderPrompt(clear) should be empty for non-prompt kind, got %q", got)
	}
}
