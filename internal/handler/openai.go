package handler

import (
	"net/http"

	"glm-proxy/internal/middleware"
	"glm-proxy/internal/proxy"
)

// OpenAI handles ALL /v1/* requests (except /v1/messages which is Anthropic).
func OpenAI(p *proxy.OpenAIProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := middleware.GetApiKey(r)
		if key == nil {
			proxy.WriteError(w, http.StatusUnauthorized, "API key required")
			return
		}
		p.Proxy(w, r, key)
	}
}
