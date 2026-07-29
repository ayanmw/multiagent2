package engine

import "strings"
import "testing"

// TestDeltaState_TextStream 验证流式场景：只累加增量，跳过终帧重复 Message。
// 模拟真实框架行为：前两段为流式增量（Delta），最终响应把完整文本放进
// Message.Content（即增量之和，重复），必须被跳过。
func TestDeltaState_Stream(t *testing.T) {
	ds := NewDeltaState()
	var sb strings.Builder
	// 增量序列
	sb.WriteString(ds.Text("你", ""))
	sb.WriteString(ds.Text("好", ""))
	// 终帧：Message.Content = 增量之和（重复，应跳过）
	sb.WriteString(ds.Text("", "你好"))
	if sb.String() != "你好" {
		t.Fatalf("流式累积文本错误（不应重复）: %q", sb.String())
	}
}

// TestDeltaState_TextNonStreaming 验证非流式场景：单响应无增量，
// 整块文本在 Message.Content，应正常输出。
func TestDeltaState_TextNonStreaming(t *testing.T) {
	ds := NewDeltaState()
	if got := ds.Text("", "你好，世界"); got != "你好，世界" {
		t.Fatalf("非流式回退文本错误: %q", got)
	}
}

// TestDeltaState_Mixed 验证混合场景：先出现增量后，即便是后续 choice 携带的
// Message.Content 也必须被跳过（sawDelta 已置位），杜绝跨 choice/event 重复。
func TestDeltaState_Mixed(t *testing.T) {
	ds := NewDeltaState()
	var sb strings.Builder
	sb.WriteString(ds.Text("快", ""))       // 增量
	sb.WriteString(ds.Text("", "快"))        // 中段非流式整块，sawDelta 已 true → 跳过
	sb.WriteString(ds.Text("乐", ""))       // 增量
	if sb.String() != "快乐" {
		t.Fatalf("混合流累积文本错误: %q", sb.String())
	}
}

// TestDeltaState_NoContent 验证空增量+空整块返回空串（不产生脏输出）。
func TestDeltaState_NoContent(t *testing.T) {
	ds := NewDeltaState()
	if got := ds.Text("", ""); got != "" {
		t.Fatalf("空文本应返回空串，实际 %q", got)
	}
}
