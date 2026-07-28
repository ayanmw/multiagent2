// Package provider implements LLM provider model discovery: it calls a
// provider's model-list endpoint (OpenAI /v1/models, Anthropic /v1/models,
// Gemini models API) and caches the result for a short TTL so repeated UI
// refreshes do not hammer the upstream provider.
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/crypto"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
)

// ModelInfo is a normalized representation of a single discoverable model,
// independent of the upstream provider's response shape.
type ModelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type cacheEntry struct {
	models    []ModelInfo
	fetchedAt time.Time
}

// Discoverer fetches and caches model lists for providers. It is safe for
// concurrent use and is intended to be instantiated once at startup.
type Discoverer struct {
	encKey []byte
	client *http.Client
	ttl    time.Duration

	mu    sync.Mutex
	cache map[uint]cacheEntry
}

// NewDiscoverer creates a Discoverer. encKey is the AES-GCM key used to
// decrypt provider API keys at rest; ttl defaults to 5 minutes when <= 0.
func NewDiscoverer(encKey []byte, ttl time.Duration) *Discoverer {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Discoverer{
		encKey: encKey,
		client: &http.Client{Timeout: 15 * time.Second},
		ttl:    ttl,
		cache:  make(map[uint]cacheEntry),
	}
}

// FetchModels returns the list of models exposed by the provider. Results are
// cached per provider for the configured TTL. The boolean reports whether the
// returned value came from the in-memory cache (so callers can show freshness).
func (d *Discoverer) FetchModels(p *model.Provider) (models []ModelInfo, cached bool, err error) {
	d.mu.Lock()
	if e, ok := d.cache[p.ID]; ok && time.Since(e.fetchedAt) < d.ttl {
		entry := e
		d.mu.Unlock()
		return entry.models, true, nil
	}
	d.mu.Unlock()

	models, err = d.discover(p)
	if err != nil {
		return nil, false, err
	}

	d.mu.Lock()
	d.cache[p.ID] = cacheEntry{models: models, fetchedAt: time.Now()}
	d.mu.Unlock()
	return models, false, nil
}

func (d *Discoverer) discover(p *model.Provider) ([]ModelInfo, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("provider %d has no base_url configured", p.ID)
	}

	// Decrypt the API key (optional for some local proxies such as Ollama).
	var apiKey string
	if p.APIKeyEnc != "" {
		k, err := crypto.Decrypt(p.APIKeyEnc, d.encKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt provider api key: %w", err)
		}
		apiKey = k
	}

	switch p.Protocol {
	case model.ProtocolOpenAI:
		return d.fetchBearerModels(base, apiKey, "Authorization", "Bearer "+apiKey)
	case model.ProtocolAnthropic:
		return d.fetchAnthropicModels(base, apiKey)
	case model.ProtocolGemini:
		return d.fetchGeminiModels(base, apiKey)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", p.Protocol)
	}
}

// fetchBearerModels performs a GET to {base}/models with the given auth header
// set only when an apiKey is present, then parses the OpenAI/Anthropic shape.
func (d *Discoverer) fetchBearerModels(base, apiKey, headerKey, headerVal string) ([]ModelInfo, error) {
	url := base + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set(headerKey, headerVal)
	}
	return d.doFetch(req, parseOpenAIOrAnthropic)
}

func (d *Discoverer) fetchAnthropicModels(base, apiKey string) ([]ModelInfo, error) {
	url := base + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	return d.doFetch(req, parseOpenAIOrAnthropic)
}

func (d *Discoverer) fetchGeminiModels(base, apiKey string) ([]ModelInfo, error) {
	host := base
	if !strings.Contains(host, "generativelanguage.googleapis.com") {
		host = "https://generativelanguage.googleapis.com/v1beta"
	} else {
		host = strings.TrimRight(host, "/")
	}
	url := host + "/models?key=" + apiKey
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return d.doFetch(req, parseGemini)
}

func (d *Discoverer) doFetch(req *http.Request, parse func([]byte) ([]ModelInfo, error)) ([]ModelInfo, error) {
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider models request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read provider response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned status %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return parse(body)
}

// parseOpenAIOrAnthropic handles both OpenAI and Anthropic list shapes, which
// both wrap models in a top-level "data" array.
func parseOpenAIOrAnthropic(body []byte) ([]ModelInfo, error) {
	var raw struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse models response: %w", err)
	}
	out := make([]ModelInfo, 0, len(raw.Data))
	for _, m := range raw.Data {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, ModelInfo{ID: m.ID, Name: name, OwnedBy: m.OwnedBy})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no models found in provider response")
	}
	return out, nil
}

// parseGemini handles the Google Generative Language API shape, where models
// live under "models" and ids are prefixed with "models/".
func parseGemini(body []byte) ([]ModelInfo, error) {
	var raw struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse gemini models response: %w", err)
	}
	out := make([]ModelInfo, 0, len(raw.Models))
	for _, m := range raw.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		name := m.DisplayName
		if name == "" {
			name = id
		}
		out = append(out, ModelInfo{ID: id, Name: name, OwnedBy: "google"})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no models found in gemini response")
	}
	return out, nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n]
	}
	return s
}
