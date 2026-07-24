package config

import (
	"os"
	"path/filepath"
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

func TestLoadOrCreateBootstrapConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg, data, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("config was not created")
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != "sk-lite-local" {
		t.Fatalf("api keys = %v", cfg.APIKeys)
	}
	if len(cfg.AnthropicMessages)+len(cfg.OpenAIResponses)+len(cfg.OpenAICompletions) != 0 {
		t.Fatal("bootstrap config should not contain providers")
	}
	if !strings.Contains(string(data), "Open http://127.0.0.1:8318/") {
		t.Fatalf("unexpected bootstrap config: %s", data)
	}

	_, secondData, secondCreated, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if secondCreated || string(secondData) != string(data) {
		t.Fatal("existing config was recreated or changed")
	}
}

func TestLoadOrCreateDoesNotReplaceInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	want := []byte("not: [valid")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := LoadOrCreate(path); err == nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("invalid config was overwritten: %q", got)
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

func TestProviderSpeedNormalizeAndValidate(t *testing.T) {
	var cfg Config
	raw := `
port: 1
api-keys: [k]
openai-completions:
  - name: official
    speed: " FAST "
    base-url: http://x
    api-key: a
    models: [{name: m}]
`
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	if cfg.OpenAICompletions[0].Speed != "fast" {
		t.Fatalf("speed = %q, want fast", cfg.OpenAICompletions[0].Speed)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate fast speed: %v", err)
	}

	cfg.OpenAICompletions[0].Speed = "turbo"
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "speed must be fast") {
		t.Fatalf("validate invalid speed = %v, want speed validation error", err)
	}
}
