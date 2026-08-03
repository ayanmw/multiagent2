package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/command"
	"github.com/gin-gonic/gin"
)

func TestListCommandsHandler_ReturnsRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/commands", ListCommandsHandler())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/commands", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Commands []command.Command `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Commands) == 0 {
		t.Fatal("expected non-empty command list")
	}

	// 校验内置命令齐全，且 prompt 类命令都带模板。
	wantNames := map[string]bool{"clear": false, "model": false, "workspace": false, "run": false, "review": false, "plan": false}
	for _, c := range body.Commands {
		if _, ok := wantNames[c.Name]; ok {
			wantNames[c.Name] = true
		}
		if c.Kind == command.KindPrompt && c.Template == "" {
			t.Errorf("prompt command %q missing template", c.Name)
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("builtin command %q missing from endpoint response", name)
		}
	}
}
