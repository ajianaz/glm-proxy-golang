package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Port              string
	DataFile          string
	MasterKey         string // upstream API key (fallback for keys without upstream_key)
	AdminAPIKey       string // admin auth key
	DefaultModel      string
	AllowedModels     []string
	FlushInterval     time.Duration
	OpenAIUpstream    string
	AnthropicUpstream string
}

func Load() *Config {
	return &Config{
		Port:              getEnv("PORT", "3000"),
		DataFile:          getEnv("DATA_FILE", "data/apikeys.json"),
		MasterKey:         getMasterKey(),
		AdminAPIKey:       os.Getenv("ADMIN_API_KEY"),
		DefaultModel:      getEnv("DEFAULT_MODEL", "glm-4.7"),
		AllowedModels:     parseAllowedModels(os.Getenv("ALLOWED_MODELS")),
		FlushInterval:     30 * time.Second,
		OpenAIUpstream:    getEnv("OPENAI_UPSTREAM", "http://litellm:4000"),
		AnthropicUpstream: getEnv("ANTHROPIC_UPSTREAM", "http://litellm:4000"),
	}
}

// getMasterKey returns the upstream API key from env.
// Supports MASTER_KEY (new) with fallback to ZAI_API_KEY (legacy) for backward compat.
func getMasterKey() string {
	if v := os.Getenv("MASTER_KEY"); v != "" {
		return v
	}
	return os.Getenv("ZAI_API_KEY")
}

func parseAllowedModels(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
