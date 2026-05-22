package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Port           string
	DataFile       string
	ZaiApiKey      string
	AdminAPIKey    string
	DefaultModel   string
	AllowedModels  []string
	FlushInterval  time.Duration
	Version        string
}

func Load(version string) *Config {
	return &Config{
		Port:           getEnv("PORT", "3000"),
		DataFile:       getEnv("DATA_FILE", "data/apikeys.json"),
		ZaiApiKey:      os.Getenv("ZAI_API_KEY"),
		AdminAPIKey:    os.Getenv("ADMIN_API_KEY"),
		DefaultModel:   getEnv("DEFAULT_MODEL", "glm-4.7"),
		AllowedModels:  parseAllowedModels(os.Getenv("ALLOWED_MODELS")),
		FlushInterval:  30 * time.Second,
		Version:        version,
	}
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
