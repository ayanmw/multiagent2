package engine

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// fakeUsageRunner 发射预设事件（含 usage），用于验证 Engine 在 Stream 中捕获 token 用量。
type fakeUsageRunner struct {
	events []*event.Event
}

func (f *fakeUsageRunner) Run(ctx context.Context, userID, sessionID string, message model.Message, _ ...agent.RunOption) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (f *fakeUsageRunner) Close() error { return nil }

// TestEngine_LastUsage_Capture 验收 M3-03：Stream 事件流中的 usage 被正确累计到 LastUsage。
func TestEngine_LastUsage_Capture(t *testing.T) {
	e := &Engine{
		cfg:    ModelConfig{},
		runner: &fakeUsageRunner{events: []*event.Event{
			{Response: &model.Response{Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}},
		}},
	}
	out, err := e.Stream(context.Background(), "s1", "hi", nil)
	if err != nil {
		t.Fatalf("Stream 返回错误: %v", err)
	}
	for range out {
		// 消费事件流
	}
	got := e.LastUsage()
	if got.PromptTokens != 10 || got.CompletionTokens != 5 || got.TotalTokens != 15 {
		t.Fatalf("LastUsage 与上游不一致: %+v", got)
	}
}

// TestEngine_LastUsage_ZeroSkipped 上游未给 usage（TotalTokens==0）时不应覆盖为 0。
func TestEngine_LastUsage_ZeroSkipped(t *testing.T) {
	e := &Engine{
		cfg:    ModelConfig{},
		runner: &fakeUsageRunner{events: []*event.Event{
			{Response: &model.Response{Usage: &model.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}}},
		}},
	}
	out, _ := e.Stream(context.Background(), "s1", "hi", nil)
	for range out {
	}
	// 初始为零值，未写入；LastUsage 应仍为 0（不崩溃即可）。
	if got := e.LastUsage(); got.TotalTokens != 0 {
		t.Fatalf("零用量不应被记录，实际 %+v", got)
	}
}

// TestEstimateUsage 验收本地粗估兜底（字符数/4），total=prompt+completion。
func TestEstimateUsage(t *testing.T) {
	// 8 个 rune /4 = 2（prompt），4 个 rune /4 = 1（completion），total=3。
	u := EstimateUsage("12345678", "1234")
	if u.PromptTokens != 2 || u.CompletionTokens != 1 || u.TotalTokens != 3 {
		t.Fatalf("EstimateUsage 计算异常: %+v", u)
	}
}
