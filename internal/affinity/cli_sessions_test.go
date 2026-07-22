package affinity_test

import (
	"net/http"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/affinity"
	"github.com/Mieluoxxx/lite-cpa/internal/config"
)

func TestCLICatalogPriorityCLIsPresent(t *testing.T) {
	want := []string{
		"claude-code", "codex", "pi", "oh-my-pi", "opencode",
		"kimi-code", "mimo-code", "zcode",
	}
	for _, id := range want {
		if _, ok := affinity.CLISessionSourceByID(id); !ok {
			t.Fatalf("missing CLI catalog entry %q", id)
		}
	}
}

func TestStickyHeadersCoverPriorityCLIs(t *testing.T) {
	// Spot-check headers that must be in the runtime list.
	must := []string{
		"X-Claude-Code-Session-Id",
		"x-opencode-session",
		"session-id",
		"session_id",
		"thread-id",
		"X-Session-Id",
		"x-session-affinity",
		"X-Client-Request-Id",
		"X-Amp-Thread-Id",
	}
	set := map[string]struct{}{}
	for _, h := range affinity.StickySessionHeaders {
		set[http.CanonicalHeaderKey(h)] = struct{}{}
		set[h] = struct{}{}
		set[stringsLower(h)] = struct{}{}
	}
	for _, h := range must {
		if _, ok := set[h]; ok {
			continue
		}
		if _, ok := set[http.CanonicalHeaderKey(h)]; ok {
			continue
		}
		if _, ok := set[stringsLower(h)]; ok {
			continue
		}
		// StickySessionHeaders stores original casing; Header.Get is case-insensitive,
		// so presence under any equal-fold match is enough.
		found := false
		for _, have := range affinity.StickySessionHeaders {
			if equalFold(have, h) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("StickySessionHeaders missing %q; have %v", h, affinity.StickySessionHeaders)
		}
	}
}

func TestOpenCodeSessionHeaderSticky(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"claude"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("x-opencode-session", "oc-sess-1")
	body := []byte(`{"metadata":{"user_id":"body-should-lose"}}`)
	match := m.Lookup("claude-sonnet", "/v1/messages", h, body)
	if !match.Matched {
		t.Fatal("expected match")
	}
	if match.CacheKey != "claude sticky:oc-sess-1" {
		t.Fatalf("want opencode header first, got %q", match.CacheKey)
	}
}

func TestClaudeCodeSessionHeaderSticky(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"claude"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("X-Claude-Code-Session-Id", "cc-sess")
	match := m.Lookup("claude-3", "/v1/messages", h, []byte(`{}`))
	if !match.Matched || match.CacheKey != "claude sticky:cc-sess" {
		t.Fatalf("%+v", match)
	}
}

func TestClaudeUserIDLegacyNormalized(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"claude"},
	})
	t.Cleanup(m.Close)

	body := []byte(`{"metadata":{"user_id":"user_abc_account__session_ac980658-63bd-4fb3-97ba-8da64cb1e344"}}`)
	match := m.Lookup("claude-3", "/v1/messages", nil, body)
	if !match.Matched {
		t.Fatal("expected match")
	}
	if match.CacheKey != "claude sticky:ac980658-63bd-4fb3-97ba-8da64cb1e344" {
		t.Fatalf("want normalized session uuid, got %q", match.CacheKey)
	}
}

func TestClaudeUserIDJSONNormalized(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"claude"},
	})
	t.Cleanup(m.Close)

	// metadata.user_id is a JSON object string (escaped in real payloads).
	body := []byte(`{"metadata":{"user_id":"{\"device_id\":\"d1\",\"session_id\":\"e26d4046-0f88-4b09-bb5b-f863ab5fb24e\"}"}}`)
	match := m.Lookup("claude-3", "/v1/messages", nil, body)
	if !match.Matched {
		t.Fatal("expected match")
	}
	if match.CacheKey != "claude sticky:e26d4046-0f88-4b09-bb5b-f863ab5fb24e" {
		t.Fatalf("want json session_id, got %q", match.CacheKey)
	}
}

func TestCodexSessionIDHeader(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"gpt"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("session_id", "codex-sess")
	match := m.Lookup("gpt-5", "/v1/responses", h, []byte(`{"prompt_cache_key":"pc-should-lose"}`))
	if !match.Matched || match.CacheKey != "gpt sticky:codex-sess" {
		t.Fatalf("%+v", match)
	}
}

func TestPiClientRequestIDHeader(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"gpt"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("X-Client-Request-Id", "pi-sess")
	match := m.Lookup("gpt-5", "/v1/responses", h, []byte(`{}`))
	if !match.Matched || match.CacheKey != "gpt sticky:pi-sess" {
		t.Fatalf("%+v", match)
	}
}

func TestSessionAffinityHeader(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"gpt"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("x-session-affinity", "aff-1")
	match := m.Lookup("gpt-5", "/v1/chat/completions", h, []byte(`{}`))
	if !match.Matched || match.CacheKey != "gpt sticky:aff-1" {
		t.Fatalf("%+v", match)
	}
}

func TestMiMoSessionAffinityHeader(t *testing.T) {
	// MiMo-Code packages/opencode/src/session/llm.ts:
	//   headers["x-session-affinity"] = input.sessionID
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"gpt"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("x-session-affinity", "ses_mimo_abc")
	h.Set("User-Agent", "mimocode/0.1.0")
	match := m.Lookup("gpt-5", "/v1/chat/completions", h, []byte(`{}`))
	if !match.Matched || match.CacheKey != "gpt sticky:ses_mimo_abc" {
		t.Fatalf("%+v", match)
	}
}

func TestKimiPromptCacheKeyBody(t *testing.T) {
	// MoonshotAI/kimi-code: sessionId mapped to prompt_cache_key
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"kimi"},
	})
	t.Cleanup(m.Close)

	body := []byte(`{"model":"kimi-for-coding","prompt_cache_key":"kimi-sess-uuid"}`)
	match := m.Lookup("kimi-for-coding", "/v1/chat/completions", nil, body)
	if !match.Matched || match.CacheKey != "kimi sticky:kimi-sess-uuid" {
		t.Fatalf("%+v", match)
	}
}

func TestCodexSessionBeatsClientRequestID(t *testing.T) {
	m := affinity.New(config.ChannelAffinitySetting{
		Enabled: boolPtr(true),
		Models:  []string{"gpt"},
	})
	t.Cleanup(m.Close)

	h := make(http.Header)
	h.Set("session_id", "stable-sess")
	h.Set("X-Client-Request-Id", "maybe-per-request")
	match := m.Lookup("gpt-5", "/v1/responses", h, []byte(`{}`))
	if !match.Matched || match.CacheKey != "gpt sticky:stable-sess" {
		t.Fatalf("session_id should win: %+v", match)
	}
}

func stringsLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func equalFold(a, b string) bool {
	return stringsLower(a) == stringsLower(b)
}
