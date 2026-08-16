package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"glm-proxy/internal/config"
	"glm-proxy/internal/proxy"
)

// ModelValidate returns middleware that restricts which models can be used.
// If AllowedModels is empty, all models are allowed (backward compatible).
func ModelValidate(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(cfg.AllowedModels) == 0 {
			return next
		}

		allowed := make(map[string]bool, len(cfg.AllowedModels))
		for _, m := range cfg.AllowedModels {
			allowed[m] = true
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only validate POST/PUT/PATCH with JSON body
			if r.Method != "POST" && r.Method != "PUT" && r.Method != "PATCH" {
				next.ServeHTTP(w, r)
				return
			}

			// Read body
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			r.Body.Close()

			// Try to parse as JSON
			var bodyMap map[string]interface{}
			if json.Unmarshal(bodyBytes, &bodyMap) != nil {
				// Not JSON, pass through
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				next.ServeHTTP(w, r)
				return
			}

			// Determine the effective model
			model := ""
			if clientModel, ok := bodyMap["model"].(string); ok && clientModel != "" {
				model = clientModel
			} else {
				// Client didn't specify model — resolve what will be injected
				apiKey := GetApiKey(r)
				if apiKey != nil {
					model = proxy.GetModelForKey(apiKey, cfg.DefaultModel)
				}
			}

			// Validate
			if model != "" && !allowed[model] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":          "model not allowed",
					"model":          model,
					"allowed_models": cfg.AllowedModels,
				})
				return
			}

			// Restore body for downstream handler
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			next.ServeHTTP(w, r)
		})
	}
}
