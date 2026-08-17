package api

import (
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/gin-gonic/gin"
)

// capture 收集 converter 输出的 AG-UI 事件，便于断言。
type capture struct {
	types []string
	byID  map[string][]string // toolCallId -> 事件类型序列
	text  strings.Builder
	hadErr bool
}

// runConverter 驱动 aguiConverter.Convert：入参已是引擎归一化后的 StreamEvent DTO
// （M6-02：api 测试不再依赖框架 event/model 包）。
func runConverter(ch <-chan engine.StreamEvent) *capture {
	cap := &capture{byID: map[string][]string{}}
	conv := newAGUIConverter()
	_, err := conv.Convert(ch, func(t string, d gin.H) {
		cap.types = append(cap.types, t)
		if id, ok := d["toolCallId"].(string); ok {
			cap.byID[id] = append(cap.byID[id], t)
		}
		if delta, ok := d["delta"].(string); ok && t == "TEXT_MESSAGE_CONTENT" {
			cap.text.WriteString(delta)
		}
	})
	cap.hadErr = err != nil
	return cap
}

func TestAGUIConverter_TextStream(t *testing.T) {
	// 模拟真实框架流式行为：前两段为流式增量（DeltaContent），
	// 最终响应把完整文本放进 MessageContent（即增量之和，重复）。
	// converter 必须只累加增量，跳过最终重复的 MessageContent。
	ch := make(chan engine.StreamEvent, 3)
	ch <- engine.StreamEvent{Choices: []engine.StreamChoice{{DeltaContent: "你"}}}
	ch <- engine.StreamEvent{Choices: []engine.StreamChoice{{DeltaContent: "好"}}}
	ch <- engine.StreamEvent{Choices: []engine.StreamChoice{{MessageContent: "你好"}}} // 最终整块 = 增量之和（重复，应跳过）
	close(ch)

	cap := runConverter(ch)
	if cap.text.String() != "你好" {
		t.Fatalf("累积文本错误（不应重复）: %q", cap.text.String())
	}
	// 仅两段增量产生 TEXT_MESSAGE_CONTENT；最终的 MessageContent 不应再发一次。
	want := []string{"TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CONTENT"}
	if len(cap.types) != len(want) {
		t.Fatalf("事件数量错误: got %v want %v", cap.types, want)
	}
	for i := range want {
		if cap.types[i] != want[i] {
			t.Fatalf("第 %d 个事件类型错误: got %q want %q", i, cap.types[i], want[i])
		}
	}
}

func TestAGUIConverter_TextNonStreaming(t *testing.T) {
	// 非流式：单响应无增量，整块文本在 MessageContent，应正常输出。
	ch := make(chan engine.StreamEvent, 1)
	ch <- engine.StreamEvent{Choices: []engine.StreamChoice{{MessageContent: "你好，世界"}}}
	close(ch)

	cap := runConverter(ch)
	if cap.text.String() != "你好，世界" {
		t.Fatalf("非流式累积文本错误: %q", cap.text.String())
	}
	if len(cap.types) != 1 || cap.types[0] != "TEXT_MESSAGE_CONTENT" {
		t.Fatalf("非流式事件类型错误: %v", cap.types)
	}
}

func TestAGUIConverter_ToolCall(t *testing.T) {
	ch := make(chan engine.StreamEvent, 1)
	ch <- engine.StreamEvent{Choices: []engine.StreamChoice{{ToolCalls: []engine.StreamToolCall{
		{ID: "call_1", Name: "echo", Arguments: `{"text":"hi"}`},
	}}}}
	close(ch)

	cap := runConverter(ch)
	seq := cap.byID["call_1"]
	if len(seq) != 3 {
		t.Fatalf("工具调用事件序列错误: %v", seq)
	}
	if seq[0] != "TOOL_CALL_START" || seq[1] != "TOOL_CALL_ARGS" || seq[2] != "TOOL_CALL_END" {
		t.Fatalf("工具调用事件顺序错误: %v", seq)
	}
}

func TestAGUIConverter_Error(t *testing.T) {
	ch := make(chan engine.StreamEvent, 1)
	ch <- engine.StreamEvent{IsError: true, ErrorMsg: "上游故障", CircuitBreak: false}
	close(ch)

	cap := runConverter(ch)
	if len(cap.types) != 1 || cap.types[0] != "RUN_ERROR" {
		t.Fatalf("错误事件类型错误: %v", cap.types)
	}
	if !cap.hadErr {
		t.Fatalf("期望 converter 返回错误但未返回")
	}
}
