package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Mieluoxxx/lite-cpa/internal/httpx"
	"github.com/Mieluoxxx/lite-cpa/internal/registry"
)

const maxModelsResponseBytes = 4 << 20

// ModelDiscovery is a validated upstream model list and the API base URL that
// produced it. ResolvedBaseURL may add /v1 to a user-supplied site root.
type ModelDiscovery struct {
	Models          []string
	ResolvedBaseURL string
}

// DiscoverModels queries an OpenAI- or Anthropic-compatible model endpoint.
// It first treats BaseURL as the API base, then tries a /v1 fallback for users
// who pasted a provider's website root.
func DiscoverModels(ctx context.Context, key registry.UpstreamKey) (ModelDiscovery, error) {
	base := strings.TrimRight(strings.TrimSpace(key.BaseURL), "/")
	if base == "" {
		return ModelDiscovery{}, fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ModelDiscovery{}, fmt.Errorf("base URL must be an absolute http(s) URL")
	}

	candidates := []string{base + "/models"}
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1") {
		candidates = append(candidates, base+"/v1/models")
	}
	errorsByURL := make([]string, 0, len(candidates))
	for _, endpoint := range candidates {
		models, fetchErr := fetchModels(ctx, key, endpoint)
		if fetchErr == nil {
			return ModelDiscovery{Models: models, ResolvedBaseURL: strings.TrimSuffix(endpoint, "/models")}, nil
		}
		errorsByURL = append(errorsByURL, fmt.Sprintf("%s: %v", endpoint, fetchErr))
	}
	return ModelDiscovery{}, fmt.Errorf("model discovery failed: %s", strings.Join(errorsByURL, "; "))
}

func fetchModels(ctx context.Context, key registry.UpstreamKey, endpoint string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	switch key.Provider {
	case "claude":
		req.Header.Set("anthropic-version", "2023-06-01")
		if key.APIKey != "" {
			req.Header.Set("x-api-key", key.APIKey)
		}
	default:
		if key.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+key.APIKey)
		}
	}
	applyCustomHeaders(req, key.Headers)
	resp, err := httpx.Do(ctx, httpx.Client(key.ProxyURL), req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxModelsResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxModelsResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, shortBody(data))
	}
	models, err := decodeModelIDs(data)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("response contains no model IDs")
	}
	return models, nil
}

func decodeModelIDs(data []byte) ([]string, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("upstream returned non-JSON model response")
	}
	raw, ok := root["data"].([]any)
	if !ok {
		raw, ok = root["models"].([]any)
	}
	if !ok {
		return nil, fmt.Errorf("model response must contain a data or models array")
	}
	seen := make(map[string]struct{}, len(raw))
	models := make([]string, 0, len(raw))
	for _, item := range raw {
		id := ""
		switch value := item.(type) {
		case string:
			id = value
		case map[string]any:
			for _, field := range []string{"id", "name", "model"} {
				if text, ok := value[field].(string); ok && strings.TrimSpace(text) != "" {
					id = text
					break
				}
			}
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}

func shortBody(data []byte) string {
	const max = 512
	text := strings.TrimSpace(string(data))
	if len(text) > max {
		text = text[:max]
	}
	return text
}
