package openai

import (
	"encoding/json"
	"net/http"

	"workbuddyllmapi/internal/config"
)

// NewServer 构造路由：提供 OpenAI 兼容的 chat/completions、models 以及健康检查。
func NewServer(b Backend, cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		list := ModelList{Object: "list"}
		for _, id := range cfg.Models {
			list.Data = append(list.Data, Model{ID: id, Object: "model", OwnedBy: "workbuddy"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			req.Model = cfg.DefaultModel
		}
		b.Chat(w, r, &req)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"service":   "workbuddyLLMAPI",
			"backend":   b.Name(),
			"endpoints": []string{"/v1/chat/completions", "/v1/models", "/healthz"},
		})
	})

	return mux
}
