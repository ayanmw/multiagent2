package goal

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseStatus(t *testing.T) {
	cases := map[string]Status{
		"pending":       StatusPending,
		"in_progress":   StatusInProgress,
		" COMPLETE ":    StatusComplete,
		"Blocked":       StatusBlocked,
		"in_progress\n": StatusInProgress,
	}
	for in, want := range cases {
		got, err := ParseStatus(in)
		if err != nil {
			t.Fatalf("ParseStatus(%q) 意外报错: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseStatus(%q)=%q want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "done", "finished", "完成"} {
		if _, err := ParseStatus(bad); err == nil {
			t.Fatalf("ParseStatus(%q) 应当报错", bad)
		}
	}
}

func TestStatusIsOpen(t *testing.T) {
	if !StatusPending.IsOpen() || !StatusInProgress.IsOpen() {
		t.Fatal("pending/in_progress 应当是未收敛状态")
	}
	if StatusComplete.IsOpen() || StatusBlocked.IsOpen() {
		t.Fatal("complete/blocked 应当是已收敛状态")
	}
	if !StatusComplete.IsTerminal() || StatusPending.IsTerminal() {
		t.Fatal("IsTerminal 与 IsOpen 应当互斥")
	}
}

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore(0)
	if _, err := s.Get("scope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空存储 Get 应当返回 ErrNotFound，got %v", err)
	}
	if s.IsOpen("scope") {
		t.Fatal("未立目标时 IsOpen 必须为 false（不能把 Agent 无故锁死）")
	}

	g, err := s.Create("scope", "  实现 M1-11  ", "目标契约", []string{"编译通过", "  ", "测试全绿"})
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if g.Title != "实现 M1-11" {
		t.Fatalf("标题未 trim: %q", g.Title)
	}
	if g.Status != StatusInProgress {
		t.Fatalf("新建目标状态应为 in_progress，got %q", g.Status)
	}
	if g.Revision != 1 {
		t.Fatalf("新建目标 revision 应为 1，got %d", g.Revision)
	}
	if len(g.AcceptanceCriteria) != 2 {
		t.Fatalf("空白验收标准应被剔除，got %v", g.AcceptanceCriteria)
	}
	if !s.IsOpen("scope") || !s.Has("scope") {
		t.Fatal("新建后 scope 应当存在且未收敛")
	}

	got, err := s.Get("scope")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.ID != g.ID {
		t.Fatalf("Get 返回的目标不一致: %q vs %q", got.ID, g.ID)
	}
}

func TestStore_CreateRejectsEmptyTitle(t *testing.T) {
	s := NewStore(0)
	if _, err := s.Create("scope", "   ", "", nil); !errors.Is(err, ErrEmptyTitle) {
		t.Fatalf("空标题应当返回 ErrEmptyTitle，got %v", err)
	}
}

func TestStore_CloneIsolation(t *testing.T) {
	s := NewStore(0)
	g, _ := s.Create("scope", "t", "", []string{"c1"})
	g.Title = "被篡改"
	g.Status = StatusComplete
	g.AcceptanceCriteria[0] = "被篡改"

	got, _ := s.Get("scope")
	if got.Title != "t" || got.Status != StatusInProgress || got.AcceptanceCriteria[0] != "c1" {
		t.Fatalf("返回值必须是深拷贝，存储不应被外部改写: %+v", got)
	}
}

func TestStore_Update(t *testing.T) {
	s := NewStore(0)
	if _, err := s.Update("scope", Patch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未建目标时 Update 应返回 ErrNotFound，got %v", err)
	}
	if _, err := s.Create("scope", "t", "", nil); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	prog := "已完成一半"
	g, err := s.Update("scope", Patch{Progress: &prog})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if g.Progress != prog {
		t.Fatalf("progress 未生效: %q", g.Progress)
	}
	if g.Revision != 2 {
		t.Fatalf("revision 应自增到 2，got %d", g.Revision)
	}
	if !g.IsOpen() {
		t.Fatal("只更新 progress 不应改变收敛状态")
	}

	done := StatusComplete
	g, err = s.Update("scope", Patch{Status: &done})
	if err != nil {
		t.Fatalf("Update(complete) 失败: %v", err)
	}
	if g.IsOpen() || s.IsOpen("scope") {
		t.Fatal("complete 之后必须视为已收敛")
	}
}

func TestStore_UpdateBlockedRequiresReason(t *testing.T) {
	s := NewStore(0)
	if _, err := s.Create("scope", "t", "", nil); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	blocked := StatusBlocked
	if _, err := s.Update("scope", Patch{Status: &blocked}); !errors.Is(err, ErrBlockedNoReason) {
		t.Fatalf("blocked 缺少原因时应报错，got %v", err)
	}
	// 原目标不应被污染。
	if cur, _ := s.Get("scope"); cur.Status != StatusInProgress {
		t.Fatalf("失败的 Update 不应改动存储，got %q", cur.Status)
	}

	reason := "缺少数据库凭据"
	g, err := s.Update("scope", Patch{Status: &blocked, Blocker: &reason})
	if err != nil {
		t.Fatalf("Update(blocked+reason) 失败: %v", err)
	}
	if g.Status != StatusBlocked || g.Blocker != reason {
		t.Fatalf("blocked 状态未正确写入: %+v", g)
	}

	// 从 blocked 恢复为 in_progress 时应清理陈旧的阻塞原因。
	running := StatusInProgress
	g, err = s.Update("scope", Patch{Status: &running})
	if err != nil {
		t.Fatalf("Update(in_progress) 失败: %v", err)
	}
	if g.Blocker != "" {
		t.Fatalf("离开 blocked 后应清空 blocker，got %q", g.Blocker)
	}
}

func TestStore_CreateOverwritesAndDelete(t *testing.T) {
	s := NewStore(0)
	first, _ := s.Create("scope", "第一个目标", "", nil)
	second, _ := s.Create("scope", "第二个目标", "", nil)
	if first.ID == second.ID {
		t.Fatal("重新立目标应当生成新的 ID")
	}
	if cur, _ := s.Get("scope"); cur.Title != "第二个目标" {
		t.Fatalf("重新立目标应当覆盖旧目标，got %q", cur.Title)
	}
	s.Delete("scope")
	if s.Has("scope") {
		t.Fatal("Delete 之后不应再存在")
	}
}

func TestStore_EvictsOldestScope(t *testing.T) {
	s := NewStore(2)
	base := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	i := 0
	s.now = func() time.Time { i++; return base.Add(time.Duration(i) * time.Minute) }

	_, _ = s.Create("a", "a", "", nil)
	_, _ = s.Create("b", "b", "", nil)
	_, _ = s.Create("c", "c", "", nil)

	if s.Len() != 2 {
		t.Fatalf("作用域数量应被限制为 2，got %d", s.Len())
	}
	if s.Has("a") {
		t.Fatal("最久未更新的作用域 a 应被淘汰")
	}
	if !s.Has("b") || !s.Has("c") {
		t.Fatal("较新的作用域不应被淘汰")
	}
}

func TestGoalRender(t *testing.T) {
	var nilGoal *Goal
	if !strings.Contains(nilGoal.Render(), "没有目标契约") {
		t.Fatal("nil 目标应当有可读兜底文案")
	}
	s := NewStore(0)
	g, _ := s.Create("scope", "跑通端到端", "M1-11 验收", []string{"编译通过", "测试全绿"})
	blocked := StatusBlocked
	reason := "缺少凭据"
	prog := "已完成编译"
	g, _ = s.Update("scope", Patch{Status: &blocked, Blocker: &reason, Progress: &prog})

	out := g.Render()
	for _, want := range []string{"跑通端到端", "blocked", "编译通过", "测试全绿", "已完成编译", "缺少凭据"} {
		if !strings.Contains(out, want) {
			t.Fatalf("渲染结果缺少 %q:\n%s", want, out)
		}
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore(0)
	if _, err := s.Create("scope", "t", "", nil); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			p := "progress"
			for j := 0; j < 50; j++ {
				_, _ = s.Update("scope", Patch{Progress: &p})
				_, _ = s.Get("scope")
				_ = s.IsOpen("scope")
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if cur, err := s.Get("scope"); err != nil || cur.Revision < 2 {
		t.Fatalf("并发更新后状态异常: %+v err=%v", cur, err)
	}
}
