package api

import (
	"strings"
	"testing"

	framework "trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"github.com/gin-gonic/gin"
)

// capture 收集 converter 输出的 AG-UI 事件，便于断言。
type capture struct {
	types []string
	byID  map[string][]string // toolCallId -> 事件类型序列
	text  strings.Builder
	hadErr bool
}

func runConverter(ch <-chan *event.Event) *capture {
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
	ch := make(chan *event.Event, 3)
	ch <- &event.Event{Response: &framework.Response{Choices: []framework.Choice{
		{Delta: framework.Message{Content: "你"}},
		{Delta: framework.Message{Content: "好"}},
	}}}
	ch <- &event.Event{Response: &framework.Response{Choices: []framework.Choice{
		{Message: framework.Message{Content: "！"}},
	}}}
	close(ch)

	cap := runConverter(ch)
	if cap.text.String() != "你好！" {
		t.Fatalf("累积文本错误: %q", cap.text.String())
	}
	// 必须包含 RUN_STARTED? 不——RUN_STARTED 由 handler 发送；converter 只发文本/工具/RUN_ERROR。
	want := []string{"TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CONTENT"}
	if len(cap.types) != len(want) {
		t.Fatalf("事件数量错误: got %v want %v", cap.types, want)
	}
	for i := range want {
		if cap.types[i] != want[i] {
			t.Fatalf("第 %d 个事件类型错误: got %q want %q", i, cap.types[i], want[i])
		}
	}
}

func TestAGUIConverter_ToolCall(t *testing.T) {
	ch := make(chan *event.Event, 2)
	ch <- &event.Event{Response: &framework.Response{Choices: []framework.Choice{
		{Message: framework.Message{ToolCalls: []framework.ToolCall{
			{ID: "call_1", Function: framework.FunctionDefinitionParam{
				Name:      "echo",
				Arguments: []byte(`{"text":"hi"}`),
			}},
		}}},
	}}}
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
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &framework.Response{
		Object: framework.ObjectTypeError,
		Error:  &framework.ResponseError{Message: "上游故障"},
	}}
	close(ch)

	cap := runConverter(ch)
	if len(cap.types) != 1 || cap.types[0] != "RUN_ERROR" {
		t.Fatalf("错误事件类型错误: %v", cap.types)
	}
	if !cap.hadErr {
		t.Fatalf("期望 converter 返回错误但未返回")
	}
}
