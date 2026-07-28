package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/crypto"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
)

// testKey is a 32-byte key used only for tests.
const testKey = "0123456789abcdef0123456789abcdef"

func mustEncrypt(t *testing.T, plaintext string, key []byte) string {
	t.Helper()
	e, err := crypto.Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return e
}

func TestFetchModelsOpenAI(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("missing auth header: %v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "gpt-4o", "owned_by": "openai"},
				{"id": "gpt-4o-mini", "owned_by": "openai"},
			},
		})
	}))
	defer srv.Close()

	key := []byte(testKey)
	d := NewDiscoverer(key, 5*time.Minute)
	p := &model.Provider{
		Protocol:  model.ProtocolOpenAI,
		BaseURL:   srv.URL + "/v1",
		APIKeyEnc: mustEncrypt(t, "sk-test", key),
	}
	p.ID = 1

	models, cached, err := d.FetchModels(p)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if cached {
		t.Fatalf("expected cache miss on first fetch")
	}
	if len(models) != 2 || models[0].ID != "gpt-4o" || models[1].OwnedBy != "openai" {
		t.Fatalf("unexpected models: %+v", models)
	}

	// Second call must hit the cache (no extra upstream request).
	if _, cached2, err := d.FetchModels(p); err != nil || !cached2 {
		t.Fatalf("expected cache hit on second fetch (cached=%v err=%v)", cached2, err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected exactly 1 upstream call, got %d", got)
	}
}

func TestFetchModelsNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("did not expect auth header, got %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"id": "local-model", "name": "Local Model"}},
		})
	}))
	defer srv.Close()

	d := NewDiscoverer([]byte(testKey), time.Minute)
	p := &model.Provider{Protocol: model.ProtocolOpenAI, BaseURL: srv.URL + "/v1"}
	p.ID = 2

	models, _, err := d.FetchModels(p)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "local-model" || models[0].Name != "Local Model" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestFetchModelsAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ak-test" {
			t.Errorf("missing x-api-key: %v", r.Header)
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"id": "claude-3-5-sonnet-20241022"}},
		})
	}))
	defer srv.Close()

	d := NewDiscoverer([]byte(testKey), time.Minute)
	p := &model.Provider{
		Protocol:  model.ProtocolAnthropic,
		BaseURL:   srv.URL + "/v1",
		APIKeyEnc: mustEncrypt(t, "ak-test", []byte(testKey)),
	}
	p.ID = 3

	models, _, err := d.FetchModels(p)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "claude-3-5-sonnet-20241022" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestFetchModelsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid key"})
	}))
	defer srv.Close()

	d := NewDiscoverer([]byte(testKey), time.Minute)
	p := &model.Provider{
		Protocol:  model.ProtocolOpenAI,
		BaseURL:   srv.URL + "/v1",
		APIKeyEnc: mustEncrypt(t, "bad", []byte(testKey)),
	}
	p.ID = 4

	if _, _, err := d.FetchModels(p); err == nil {
		t.Fatalf("expected error on 401 upstream response")
	}
}
