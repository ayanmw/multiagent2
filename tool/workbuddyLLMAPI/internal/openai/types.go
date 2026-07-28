package openai

// 这里定义与 OpenAI Chat Completions API 兼容的请求/响应结构体。
// 仅覆盖本项目需要用到的最小字段集，足以对接 trpc-agent-go 的 OpenAI 协议 Provider。

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type ChatCompletionRequest struct {
	Model            string        `json:"model"`
	Messages         []ChatMessage `json:"messages"`
	Temperature      float64       `json:"temperature"`
	TopP             float64       `json:"top_p"`
	MaxTokens        int           `json:"max_tokens"`
	Stream           bool          `json:"stream"`
	Stop             []string      `json:"stop,omitempty"`
	FrequencyPenalty float64       `json:"frequency_penalty"`
	PresencePenalty  float64       `json:"presence_penalty"`
	User             string        `json:"user,omitempty"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   Usage                  `json:"usage"`
}

type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        ChatMessage `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type ChatCompletionStreamResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []StreamChoice  `json:"choices"`
}
