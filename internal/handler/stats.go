package handler

import (
	"encoding/json"
	"net/http"

	"glm-proxy/internal/middleware"
	"glm-proxy/internal/proxy"
	"glm-proxy/internal/ratelimit"
	"glm-proxy/internal/storage"
)

// Stats handles GET /stats
func Stats(store *storage.KeyStore, defaultModel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := middleware.GetApiKey(r)
		if key == nil {
			writeStatsError(w, http.StatusUnauthorized, "API key required")
			return
		}

		model := proxy.GetModelForKey(key, defaultModel)
		info := ratelimit.CheckRateLimit(key)
		stats := store.GetStats(key, info, model)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

func writeStatsError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
