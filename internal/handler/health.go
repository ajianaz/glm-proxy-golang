package handler

import (
	"encoding/json"
	"io"
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

// Models handles GET /v1/models — returns upstream models + client-facing aliases.
// Claude Code CLI validates model names by calling this endpoint before sending requests.
// If the requested model isn't listed, Claude Code silently discards the API response.
func Models(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Fetch upstream models from LiteLLM
		upstreamModels := []map[string]interface{}{}
		if cfg.OpenAIUpstream != "" {
			req, _ := http.NewRequest("GET", cfg.OpenAIUpstream+"/v1/models", nil)
			req.Header.Set("Authorization", "Bearer "+cfg.MasterKey)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				defer resp.Body.Close()
				if body, err := io.ReadAll(resp.Body); err == nil {
					var result map[string]interface{}
					if json.Unmarshal(body, &result) == nil {
						if data, ok := result["data"].([]interface{}); ok {
							for _, m := range data {
								if modelMap, ok := m.(map[string]interface{}); ok {
									upstreamModels = append(upstreamModels, modelMap)
								}
							}
						}
					}
				}
			}
		}

		// Build the full model list: upstream + client-facing aliases
		allModels := make([]map[string]interface{}, 0, len(upstreamModels))

		// Add upstream models (with "provider" tag for identification)
		for _, m := range upstreamModels {
			allModels = append(allModels, m)
		}

		// Add client-facing model aliases from model mapping
		if cfg.ModelMap != nil && cfg.ModelMap.Enabled() {
			for _, name := range cfg.ModelMap.ClientModels() {
				allModels = append(allModels, map[string]interface{}{
					"id":       name,
					"object":   "model",
					"created":  0,
					"owned_by": "proxy-alias",
				})
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data":   allModels,
		})
	}
}
