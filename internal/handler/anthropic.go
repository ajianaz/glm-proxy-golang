package handler

import (
	"net/http"

	"glm-proxy/internal/middleware"
	"glm-proxy/internal/proxy"
)

// Anthropic handles POST /v1/messages requests.
func Anthropic(p *proxy.AnthropicProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := middleware.GetApiKey(r)
		if key == nil {
			proxy.WriteError(w, http.StatusUnauthorized, "API key required")
			return
		}
		p.Proxy(w, r, key)
	}
}
