package openai

import "net/http"

// Backend 是每个后端的统一接口，定义在本包以避免与 backend 实现包产生导入环。
// Chat 方法负责完整写出 HTTP 响应（JSON 或 SSE 流）。
type Backend interface {
	Name() string
	Chat(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest)
}
