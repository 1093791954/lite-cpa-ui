package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestChannelAffinityYAMLForms(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		enabled bool
		models  []string
	}{
		{
			name:    "omitted defaults enabled",
			yaml:    "port: 1\napi-keys: [k]\nopenai-completions: [{name: x, base-url: http://x, api-key: a, models: [{name: m}]}]\n",
			enabled: true,
		},
		{
			name:    "bool true",
			yaml:    "port: 1\napi-keys: [k]\nchannel-affinity: true\nopenai-completions: [{name: x, base-url: http://x, api-key: a, models: [{name: m}]}]\n",
			enabled: true,
		},
		{
			name:    "bool false",
			yaml:    "port: 1\napi-keys: [k]\nchannel-affinity: false\nopenai-completions: [{name: x, base-url: http://x, api-key: a, models: [{name: m}]}]\n",
			enabled: false,
		},
		{
			name:    "family list",
			yaml:    "port: 1\napi-keys: [k]\nchannel-affinity: [claude, grok]\nopenai-completions: [{name: x, base-url: http://x, api-key: a, models: [{name: m}]}]\n",
			enabled: true,
			models:  []string{"claude", "grok"},
		},
		{
			name:    "nested list sugar",
			yaml:    "port: 1\napi-keys: [k]\nchannel-affinity:\n  - [claude, gpt, grok]\nopenai-completions: [{name: x, base-url: http://x, api-key: a, models: [{name: m}]}]\n",
			enabled: true,
			models:  []string{"claude", "gpt", "grok"},
		},
		{
			name:    "mapping models",
			yaml:    "port: 1\napi-keys: [k]\nchannel-affinity:\n  models: [kimi, qwen]\n  default-ttl-seconds: 120\nopenai-completions: [{name: x, base-url: http://x, api-key: a, models: [{name: m}]}]\n",
			enabled: true,
			models:  []string{"kimi", "qwen"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			if err := yaml.Unmarshal([]byte(tc.yaml), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			cfg.applyDefaults()
			if got := cfg.ChannelAffinity.EnabledOrDefault(); got != tc.enabled {
				t.Fatalf("enabled=%v want %v", got, tc.enabled)
			}
			if tc.enabled {
				if len(cfg.ChannelAffinity.Rules) == 0 {
					t.Fatal("expected expanded rules")
				}
			} else if len(cfg.ChannelAffinity.Rules) != 0 {
				t.Fatalf("disabled should not expand rules, got %d", len(cfg.ChannelAffinity.Rules))
			}
			if tc.models != nil {
				if strings.Join(cfg.ChannelAffinity.Models, ",") != strings.Join(tc.models, ",") {
					t.Fatalf("models=%v want %v", cfg.ChannelAffinity.Models, tc.models)
				}
			}
		})
	}
}

func TestExpandAffinityModelsCoversGrok(t *testing.T) {
	rules := ExpandAffinityModels([]string{"grok", "claude"})
	if len(rules) != 2 {
		t.Fatalf("rules=%d", len(rules))
	}
	if rules[0].Name != "grok sticky" || rules[0].SkipRetryOnFailure {
		t.Fatalf("grok rule %+v", rules[0])
	}
	if rules[1].Name != "claude sticky" || rules[1].SkipRetryOnFailure {
		t.Fatalf("claude rule %+v", rules[1])
	}
}

func TestProviderFailoverModeNormalize(t *testing.T) {
	var cfg Config
	raw := `
port: 1
api-keys: [k]
openai-completions:
  - name: relay
    base-url: http://x
    api-key: a
    failover-mode: site
    models: [{name: m}]
  - name: official
    base-url: http://y
    api-key: b
    models: [{name: m}]
`
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	if cfg.OpenAICompletions[0].FailoverMode != "provider" {
		t.Fatalf("site alias -> %q", cfg.OpenAICompletions[0].FailoverMode)
	}
	if cfg.OpenAICompletions[1].FailoverMode != "key" {
		t.Fatalf("default -> %q", cfg.OpenAICompletions[1].FailoverMode)
	}
}
