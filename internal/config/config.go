package config

import (
	"os"
	"time"
)

type Config struct {
	Port           string
	DataFile       string
	ZaiApiKey      string
	DefaultModel   string
	FlushInterval  time.Duration
}

func Load() *Config {
	return &Config{
		Port:           getEnv("PORT", "3000"),
		DataFile:       getEnv("DATA_FILE", "data/apikeys.json"),
		ZaiApiKey:      os.Getenv("ZAI_API_KEY"),
		DefaultModel:   getEnv("DEFAULT_MODEL", "glm-4.7"),
		FlushInterval:  30 * time.Second,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
