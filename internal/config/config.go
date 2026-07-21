package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the slim lite-cpa configuration surface.
type Config struct {
	Host    string   `yaml:"host"`
	Port    int      `yaml:"port"`
	APIKeys []string `yaml:"api-keys"`

	// MaxBodyBytes limits inbound request bodies (0 = 32MiB default).
	MaxBodyBytes int64 `yaml:"max-body-bytes"`

	// RequestRetry is credential rotation attempts after the first failure.
	RequestRetry int `yaml:"request-retry"`

	// ProxyURL is a global outbound proxy (optional).
	ProxyURL string `yaml:"proxy-url"`

	// Debug enables verbose logging to stderr.
	Debug bool `yaml:"debug"`

	// RequestLog is optional request recording (sqlite or postgres) with retention.
	RequestLog RequestLogConfig `yaml:"request-log"`

	AnthropicMessages []Provider `yaml:"anthropic-messages"`
	OpenAIResponses   []Provider `yaml:"openai-responses"`
	OpenAICompletions []Provider `yaml:"openai-completions"`
}

// RequestLogConfig controls optional request recording.
// Disabled by default. When enabled, metadata is stored; bodies are optional.
type RequestLogConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Backend   string `yaml:"backend"`   // "sqlite" | "postgres"
	Retention string `yaml:"retention"` // Go duration, e.g. "168h", "7d" not valid — use 168h
	StoreBody bool   `yaml:"store-body"`

	SQLite   SQLiteLogConfig   `yaml:"sqlite"`
	Postgres PostgresLogConfig `yaml:"postgres"`
}

type SQLiteLogConfig struct {
	Path string `yaml:"path"` // default logs/requests.db
}

type PostgresLogConfig struct {
	DSN string `yaml:"dsn"`
}

// Provider is a named upstream entry with multi-key support.
// Preferred config field order: name, headers, base-url, api-key, models.
// Headers may include User-Agent to override Go's default "Go-http-client/1.1".
type Provider struct {
	Name          string            `yaml:"name"`
	Headers       map[string]string `yaml:"headers"`
	BaseURL       string            `yaml:"base-url"`
	APIKey        string            `yaml:"api-key"`
	Models        []ModelAlias      `yaml:"models"`
	APIKeyEntries []APIKeyEntry     `yaml:"api-key-entries"`
	ProxyURL      string            `yaml:"proxy-url"`
	Priority      int               `yaml:"priority"`
}

type APIKeyEntry struct {
	APIKey   string `yaml:"api-key"`
	ProxyURL string `yaml:"proxy-url"`
	Priority int    `yaml:"priority"`
}

type ModelAlias struct {
	Name  string `yaml:"name"`
	Alias string `yaml:"alias"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Port == 0 {
		c.Port = 8317
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 32 << 20
	}
	if c.RequestRetry < 0 {
		c.RequestRetry = 0
	}
	if c.RequestLog.Backend == "" {
		c.RequestLog.Backend = "sqlite"
	}
	if c.RequestLog.Retention == "" {
		c.RequestLog.Retention = "168h" // 7 days
	}
	if c.RequestLog.SQLite.Path == "" {
		c.RequestLog.SQLite.Path = "logs/requests.db"
	}
}

func (c *Config) validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	if len(c.APIKeys) == 0 {
		return fmt.Errorf("api-keys must not be empty")
	}
	for i, k := range c.APIKeys {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("api-keys[%d] is empty", i)
		}
	}
	if len(c.AnthropicMessages) == 0 && len(c.OpenAIResponses) == 0 && len(c.OpenAICompletions) == 0 {
		return fmt.Errorf("at least one upstream provider is required")
	}
	if c.RequestLog.Enabled {
		switch strings.ToLower(strings.TrimSpace(c.RequestLog.Backend)) {
		case "sqlite":
			// ok
		case "postgres", "postgresql", "pg":
			if strings.TrimSpace(c.RequestLog.Postgres.DSN) == "" {
				return fmt.Errorf("request-log.postgres.dsn is required when backend is postgres")
			}
		default:
			return fmt.Errorf("request-log.backend must be sqlite or postgres")
		}
		if _, err := time.ParseDuration(c.RequestLog.Retention); err != nil {
			return fmt.Errorf("request-log.retention: %w", err)
		}
	}
	return nil
}

// ExpandKeys returns flat key entries for a provider (flat api-key or api-key-entries).
func ExpandKeys(flatKey, flatProxy string, flatPriority int, entries []APIKeyEntry) []APIKeyEntry {
	if len(entries) > 0 {
		out := make([]APIKeyEntry, 0, len(entries))
		for _, e := range entries {
			e.APIKey = strings.TrimSpace(e.APIKey)
			if e.APIKey == "" {
				continue
			}
			out = append(out, e)
		}
		return out
	}
	flatKey = strings.TrimSpace(flatKey)
	if flatKey == "" {
		return nil
	}
	return []APIKeyEntry{{APIKey: flatKey, ProxyURL: flatProxy, Priority: flatPriority}}
}

func (m ModelAlias) ResolvedAlias() string {
	if strings.TrimSpace(m.Alias) != "" {
		return strings.TrimSpace(m.Alias)
	}
	return strings.TrimSpace(m.Name)
}
