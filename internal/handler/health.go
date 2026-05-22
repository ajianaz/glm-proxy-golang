package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"glm-proxy/internal/config"
)

// Health handles GET /health
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// Index handles GET /
func Index(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":    "Proxy Gateway",
			"version": cfg.Version,
			"endpoints": map[string]string{
				"health":              "GET /health",
				"stats":               "GET /stats",
				"openai_compatible":   "ALL /v1/* (except /v1/messages)",
				"anthropic_compatible": "POST /v1/messages",
			},
		})
	}
}
