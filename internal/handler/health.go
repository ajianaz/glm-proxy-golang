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
		resp := map[string]interface{}{
			"name":    "Proxy Gateway",
			"version": cfg.Version,
			"endpoints": map[string]string{
				"health":              "GET /health",
				"stats":               "GET /stats",
				"model_map":           "GET /model-map",
				"openai_compatible":   "ALL /v1/* (except /v1/messages)",
				"anthropic_compatible": "POST /v1/messages",
			},
		}
		if cfg.ModelMap != nil && cfg.ModelMap.Enabled() {
			resp["model_mapping"] = cfg.ModelMap.Entries()
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// ModelMap handles GET /model-map — returns current model mapping configuration.
func ModelMap(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if cfg.ModelMap == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"enabled": false,
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"enabled": cfg.ModelMap.Enabled(),
			"mapping": cfg.ModelMap.Entries(),
		})
	}
}
