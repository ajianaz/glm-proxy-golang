package middleware

import (
	"net/http"
	"strings"

	"glm-proxy/internal/storage"
)

// Auth middleware validates API key from Authorization or x-api-key headers.
func Auth(store *storage.KeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keyStr := extractApiKey(r.Header)

			if keyStr == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "API key required. Provide it via Authorization: Bearer <key> or x-api-key header.",
				})
				return
			}

			keyStr = strings.TrimPrefix(keyStr, "Bearer ")
			keyStr = strings.TrimSpace(keyStr)

			if keyStr == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "API key cannot be empty",
				})
				return
			}

			apiKey, found := store.FindKey(keyStr)
			if !found {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "Invalid API key",
				})
				return
			}

			if apiKey.IsExpired() {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error": "API key expired on " + apiKey.ExpiryDate,
				})
				return
			}

			next.ServeHTTP(w, SetApiKey(r, apiKey))
		})
	}
}

// extractApiKey returns the API key from Authorization or x-api-key header.
func extractApiKey(h http.Header) string {
	if auth := h.Get("Authorization"); auth != "" {
		return auth
	}
	return h.Get("x-api-key")
}
