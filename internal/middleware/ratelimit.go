package middleware

import (
	"net/http"
	"strconv"

	"glm-proxy/internal/ratelimit"
)

// RateLimit middleware checks the rolling 5h token window.
func RateLimit() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := GetApiKey(r)
			if key == nil {
				next.ServeHTTP(w, r)
				return
			}

			info := ratelimit.CheckRateLimit(key)
			if !info.Allowed {
				h := w.Header()
				if info.RetryAfter > 0 {
					h.Set("Retry-After", strconv.Itoa(info.RetryAfter))
				}
				writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
					"error": map[string]interface{}{
						"message":        info.Reason,
						"type":           "rate_limit_exceeded",
						"tokens_used":    info.TokensUsed,
						"tokens_limit":   info.TokensLimit,
						"window_ends_at": info.WindowEnd,
					},
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
