package pool

import (
	"strings"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/registry"
)

// BuildRegistry constructs a model registry from config upstream sections.
func BuildRegistry(cfg *config.Config) *registry.Registry {
	r := registry.New()
	now := time.Now().Unix()

	for i, p := range cfg.AnthropicMessages {
		name := providerName(p.Name, "anthropic", i)
		keys := expandProvider("claude", name, p.BaseURL, p.APIKey, p.ProxyURL, p.Priority, p.Headers, p.APIKeyEntries, cfg.ProxyURL)
		for _, m := range p.Models {
			alias := m.ResolvedAlias()
			if alias == "" {
				continue
			}
			r.RegisterModel(alias, &registry.ModelInfo{
				ID: alias, Created: now, Type: "claude",
			}, keysForModel(keys, m.Name))
		}
	}

	for i, p := range cfg.OpenAIResponses {
		name := providerName(p.Name, "responses", i)
		keys := expandProvider("openai-response", name, p.BaseURL, p.APIKey, p.ProxyURL, p.Priority, p.Headers, p.APIKeyEntries, cfg.ProxyURL)
		for _, m := range p.Models {
			alias := m.ResolvedAlias()
			if alias == "" {
				continue
			}
			r.RegisterModel(alias, &registry.ModelInfo{
				ID: alias, Created: now, Type: "openai-response",
			}, keysForModel(keys, m.Name))
		}
	}

	for i, p := range cfg.OpenAICompletions {
		name := providerName(p.Name, "compat", i)
		keys := expandProvider("openai", name, p.BaseURL, p.APIKey, p.ProxyURL, p.Priority, p.Headers, p.APIKeyEntries, cfg.ProxyURL)
		for _, m := range p.Models {
			alias := m.ResolvedAlias()
			if alias == "" {
				continue
			}
			r.RegisterModel(alias, &registry.ModelInfo{
				ID: alias, Created: now, Type: "openai",
			}, keysForModel(keys, m.Name))
		}
	}
	return r
}

func providerName(name, fallbackPrefix string, index int) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s-%d", fallbackPrefix, index)
}

func expandProvider(provider, name, baseURL, flatKey, flatProxy string, flatPriority int, headers map[string]string, entries []config.APIKeyEntry, globalProxy string) []registry.UpstreamKey {
	baseURL = trimSlash(baseURL)
	expanded := config.ExpandKeys(flatKey, flatProxy, flatPriority, entries)
	out := make([]registry.UpstreamKey, 0, len(expanded))
	for i, e := range expanded {
		proxy := e.ProxyURL
		if proxy == "" {
			proxy = flatProxy
		}
		if proxy == "" {
			proxy = globalProxy
		}
		priority := e.Priority
		if priority == 0 {
			priority = flatPriority
		}
		h := map[string]string{}
		for k, v := range headers {
			h[k] = v
		}
		out = append(out, registry.UpstreamKey{
			ID:       fmt.Sprintf("%s-%d", name, i),
			Name:     name,
			Provider: provider,
			BaseURL:  baseURL,
			APIKey:   e.APIKey,
			Priority: priority,
			Headers:  h,
			ProxyURL: proxy,
		})
	}
	return out
}

func keysForModel(keys []registry.UpstreamKey, upstreamModel string) []registry.UpstreamKey {
	// Attach upstream model name into a copy via ID suffix is not needed;
	// handlers resolve alias separately. Store upstream model in a synthetic header.
	out := make([]registry.UpstreamKey, len(keys))
	for i, k := range keys {
		out[i] = k
		if out[i].Headers == nil {
			out[i].Headers = map[string]string{}
		} else {
			h := make(map[string]string, len(k.Headers)+1)
			for kk, vv := range k.Headers {
				h[kk] = vv
			}
			out[i].Headers = h
		}
		if upstreamModel != "" {
			out[i].Headers["x-lite-upstream-model"] = upstreamModel
		}
	}
	return out
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// Selector does round-robin across keys for a model with simple failure skip.
type Selector struct {
	reg   *registry.Registry
	rr    sync.Map // model -> *uint64
	retry int
}

func NewSelector(reg *registry.Registry, retry int) *Selector {
	return &Selector{reg: reg, retry: retry}
}

func (s *Selector) Pick(model string, tried map[string]struct{}) (registry.UpstreamKey, string, error) {
	info, keys, ok := s.reg.Resolve(model)
	if !ok || len(keys) == 0 {
		return registry.UpstreamKey{}, "", fmt.Errorf("model not found: %s", model)
	}
	_ = info
	upstreamModel := model
	if len(keys) > 0 && keys[0].Headers != nil {
		if m := keys[0].Headers["x-lite-upstream-model"]; m != "" {
			upstreamModel = m
		}
	}

	// Sort-stable by priority (lower number = higher priority) via two-pass.
	// Simple: try all starting from RR index, prefer lower priority value.
	start := s.nextIndex(model, len(keys))
	var best *registry.UpstreamKey
	bestPri := int(^uint(0) >> 1)
	for i := 0; i < len(keys); i++ {
		k := keys[(start+i)%len(keys)]
		if tried != nil {
			if _, used := tried[k.ID]; used {
				continue
			}
		}
		if k.Priority < bestPri {
			cp := k
			best = &cp
			bestPri = k.Priority
			// keep scanning for same-start preference of equal priority first hit
			if i == 0 {
				break
			}
		}
	}
	// fallback: first unused
	if best == nil {
		for i := 0; i < len(keys); i++ {
			k := keys[(start+i)%len(keys)]
			if tried != nil {
				if _, used := tried[k.ID]; used {
					continue
				}
			}
			cp := k
			best = &cp
			break
		}
	}
	if best == nil {
		return registry.UpstreamKey{}, "", fmt.Errorf("no available credentials for model %s", model)
	}
	if best.Headers != nil {
		if m := best.Headers["x-lite-upstream-model"]; m != "" {
			upstreamModel = m
		}
	}
	return *best, upstreamModel, nil
}

func (s *Selector) nextIndex(model string, n int) int {
	if n <= 0 {
		return 0
	}
	v, _ := s.rr.LoadOrStore(model, new(uint64))
	counter := v.(*uint64)
	return int(atomic.AddUint64(counter, 1)-1) % n
}

func (s *Selector) MaxAttempts(model string) int {
	_, keys, ok := s.reg.Resolve(model)
	if !ok {
		return 1
	}
	max := 1 + s.retry
	if max > len(keys) {
		max = len(keys)
	}
	if max < 1 {
		max = 1
	}
	return max
}
