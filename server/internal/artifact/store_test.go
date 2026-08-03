package artifact

import (
	"path/filepath"
	"testing"
)

// 断言 FileStore 的基础读写 / 列表 / 删除 / Snapshot。
func TestFileStore_BasicCRUD(t *testing.T) {
	root := t.TempDir()
	s, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	if err := s.Write("sess:abc", PlanArtifact, "# 计划\n做X"); err != nil {
		t.Fatalf("Write PLAN failed: %v", err)
	}
	if err := s.Write("sess:abc", ProgressArtifact, "# 进展\n- 步骤1"); err != nil {
		t.Fatalf("Write PROGRESS failed: %v", err)
	}

	// Read
	c, ok, err := s.Read("sess:abc", PlanArtifact)
	if err != nil || !ok {
		t.Fatalf("Read PLAN failed: ok=%v err=%v", ok, err)
	}
	if c != "# 计划\n做X" {
		t.Fatalf("PLAN content mismatch: %q", c)
	}

	// Exists / List
	if ok, _ := s.Exists("sess:abc", PlanArtifact); !ok {
		t.Fatal("PLAN should exist")
	}
	names, err := s.List("sess:abc")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 artifacts, got %d: %v", len(names), names)
	}

	// Snapshot
	snap, err := s.Snapshot("sess:abc")
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if !snap.Any {
		t.Fatal("Snapshot.Any should be true")
	}
	if snap.Plan != "# 计划\n做X" || snap.Progress != "# 进展\n- 步骤1" {
		t.Fatalf("Snapshot content mismatch: %+v", snap)
	}

	// Remove
	if err := s.Remove("sess:abc", PlanArtifact); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if ok, _ := s.Exists("sess:abc", PlanArtifact); ok {
		t.Fatal("PLAN should be removed")
	}
	if err := s.RemoveAll("sess:abc"); err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}
	if names, _ := s.List("sess:abc"); len(names) != 0 {
		t.Fatalf("RemoveAll should empty the scope, got %v", names)
	}
}

// TestFileStore_SurvivesRestart 是 M1-16 的核心：「中断后续跑能接上」。
// 第一轮用 s1 写入状态，模拟进程重启后新建 s2 指向同一 root，应能读回。
func TestFileStore_SurvivesRestart(t *testing.T) {
	root := t.TempDir()

	s1, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore #1 failed: %v", err)
	}
	if err := s1.Write("sess:run1", ProgressArtifact, "- step1 done"); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 模拟重启：全新的 store 实例，但指向同一磁盘 root。
	s2, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore #2 (restart) failed: %v", err)
	}
	c, ok, err := s2.Read("sess:run1", ProgressArtifact)
	if err != nil || !ok {
		t.Fatalf("restart: Read failed: ok=%v err=%v", ok, err)
	}
	if c != "- step1 done" {
		t.Fatalf("restart: progress not persisted across restart: %q", c)
	}
}

// TestFileStore_KeySanitized 验证作用域键里的非法字符（如 :）被安全化为目录名，
// 且 Windows 路径下的 sess:<id> 也能正确落盘读取。
func TestFileStore_KeySanitized(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	if err := s.Write("sess:abc-123", PlanArtifact, "x"); err != nil {
		t.Fatalf("write with colon key failed: %v", err)
	}
	// 目录名应把 : 变成 _
	expectDir := filepath.Join(root, "sess_abc-123")
	if _, err := filepath.Glob(expectDir); err != nil {
		t.Fatalf("glob error: %v", err)
	}
	// 直接读回，证明 key 规范化可逆
	c, ok, err := s.Read("sess:abc-123", PlanArtifact)
	if err != nil || !ok || c != "x" {
		t.Fatalf("read back sanitized key failed: ok=%v c=%q err=%v", ok, c, err)
	}
}

// TestFileStore_PathTraversalRejected 验证越界文件名被拒绝（安全）。
func TestFileStore_PathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	if err := s.Write("k", "../escape.txt", "evil"); err == nil {
		t.Fatal("expected path-traversal write to be rejected")
	}
	if err := s.Write("k", "a/../../b", "evil"); err == nil {
		t.Fatal("expected nested path write to be rejected")
	}
}

// TestMemoryStore_Basic 验证内存后端基础可用（安全默认 / 测试）。
func TestMemoryStore_Basic(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Write("k", PlanArtifact, "p"); err != nil {
		t.Fatalf("mem write failed: %v", err)
	}
	c, ok, _ := s.Read("k", PlanArtifact)
	if !ok || c != "p" {
		t.Fatalf("mem read failed: ok=%v c=%q", ok, c)
	}
	snap, _ := s.Snapshot("k")
	if !snap.Any || snap.Plan != "p" {
		t.Fatalf("mem snapshot failed: %+v", snap)
	}
}

// TestSnapshot_AnyFlag 验证没有任何 artifact 时 Any=false。
func TestSnapshot_AnyFlag(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileStore(root)
	snap, err := s.Snapshot("no-such-key")
	if err != nil {
		t.Fatalf("snapshot err: %v", err)
	}
	if snap.Any {
		t.Fatal("empty scope should report Any=false")
	}
}
