package engine

// DeltaState 跨多个事件/choice 保持「是否见过流式增量」的状态，用于实现
// 「优先 Delta.Content，未出现过增量才回退 Message.Content」的去重规则，
// 避免框架在流式结束时把完整文本放进最终 Message.Content 导致文本重复一倍。
//
// engine.Chat 与 AG-UI converter（internal/api/sse.go）共用同一套规则，
// 故抽在此处作为公共逻辑（见 M0.5-04）。两处行为必须保持一致。
type DeltaState struct {
	sawDelta bool
}

// NewDeltaState 创建初始去重状态。
func NewDeltaState() *DeltaState {
	return &DeltaState{}
}

// Text 返回给定 choice 本次应累加/下发的文本片段（可能为空）。
//
// 规则：优先流式增量 deltaContent；仅当本轮从未出现过任何增量且
// messageContent 非空时，才回退到 messageContent（非流式整块）。
// 该规则保证流式终帧里重复的 Message.Content 被跳过，文本不会重复一倍。
func (s *DeltaState) Text(deltaContent, messageContent string) string {
	if deltaContent != "" {
		s.sawDelta = true
		return deltaContent
	}
	if messageContent != "" && !s.sawDelta {
		return messageContent
	}
	return ""
}
