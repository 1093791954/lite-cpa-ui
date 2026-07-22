package executor

import (
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/registry"
	"github.com/tidwall/gjson"
)

func TestApplyProviderSpeed(t *testing.T) {
	tests := []struct {
		name       string
		key        registry.UpstreamKey
		body       string
		wantPath   string
		wantValue  string
		wantHeader string
	}{
		{
			name: "Claude provider forces fast and merges beta",
			key: registry.UpstreamKey{
				Provider: "claude",
				Speed:    "fast",
				Headers: map[string]string{
					"anthropic-beta": "oauth-2025-04-20,fast-mode-2026-02-01",
				},
			},
			body:       `{"speed":"standard"}`,
			wantPath:   "speed",
			wantValue:  "fast",
			wantHeader: "oauth-2025-04-20,fast-mode-2026-02-01",
		},
		{
			name:      "Claude provider without fast removes client speed",
			key:       registry.UpstreamKey{Provider: "claude"},
			body:      `{"speed":"fast"}`,
			wantPath:  "speed",
			wantValue: "",
		},
		{
			name:      "OpenAI completions provider forces priority",
			key:       registry.UpstreamKey{Provider: "openai", Speed: "fast"},
			body:      `{"service_tier":"flex"}`,
			wantPath:  "service_tier",
			wantValue: "priority",
		},
		{
			name:      "OpenAI responses provider forces priority",
			key:       registry.UpstreamKey{Provider: "openai-response", Speed: "fast"},
			body:      `{"service_tier":"auto"}`,
			wantPath:  "service_tier",
			wantValue: "priority",
		},
		{
			name:      "OpenAI provider without fast removes client tier",
			key:       registry.UpstreamKey{Provider: "openai"},
			body:      `{"service_tier":"priority"}`,
			wantPath:  "service_tier",
			wantValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotBody := applyProviderSpeed(tt.key, []byte(tt.body))
			value := gjson.GetBytes(gotBody, tt.wantPath)
			if tt.wantValue == "" {
				if value.Exists() {
					t.Fatalf("%s = %q, want absent; body=%s", tt.wantPath, value.String(), gotBody)
				}
			} else if value.String() != tt.wantValue {
				t.Fatalf("%s = %q, want %q; body=%s", tt.wantPath, value.String(), tt.wantValue, gotBody)
			}
			if tt.wantHeader != "" && gotKey.Headers["Anthropic-Beta"] != tt.wantHeader {
				t.Fatalf("Anthropic-Beta = %q, want %q", gotKey.Headers["Anthropic-Beta"], tt.wantHeader)
			}
		})
	}
}
