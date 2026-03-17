package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

// OpenAIProxy proxies requests to the OpenAI-compatible Z.AI endpoint.
type OpenAIProxy struct {
	Config *config.Config
	Store  *storage.KeyStore
}

// Proxy handles a single OpenAI-compatible proxy request.
func (p *OpenAIProxy) Proxy(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey) {
	model := GetModelForKey(apiKey, p.Config.DefaultModel)
	upstreamKey := apiKey.UpstreamKey(p.Config.ZaiApiKey)

	// Build upstream URL: strip /v1/ prefix
	cleanPath := strings.TrimPrefix(r.URL.Path, "/v1")
	upstreamURL := OpenAIUpstream + cleanPath

	// Read and inject model
	body, err := readAndInjectModel(r.Body, r.URL.Path, r.Method, model)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Create upstream request
	upstreamReq, err := http.NewRequest(r.Method, upstreamURL, body)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Failed to create upstream request")
		return
	}

	// Auth: Bearer token
	upstreamReq.Header.Set("Authorization", "Bearer "+upstreamKey)
	forwardHeaders(upstreamReq.Header, r.Header,
		"authorization", "x-api-key")

	// Execute
	client := &http.Client{}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Upstream request failed")
		return
	}
	defer resp.Body.Close()

	// Check for SSE streaming
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		p.streamSSE(w, resp, apiKey.Key)
		return
	}

	// Non-streaming: relay response and track tokens
	relayResponse(w, resp, p.Store, apiKey.Key, extractOpenAITokens)
}

// extractOpenAITokens parses total_tokens from OpenAI usage response.
func extractOpenAITokens(body []byte) int {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0
	}
	usage, ok := result["usage"].(map[string]interface{})
	if !ok {
		return 0
	}
	total, ok := usage["total_tokens"].(float64)
	if !ok {
		return 0
	}
	return int(total)
}

// streamSSE proxies an SSE stream with inline token counting.
func (p *OpenAIProxy) streamSSE(w http.ResponseWriter, resp *http.Response, keyValue string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	totalTokens := StreamSSE(w, resp.Body, "openai")

	if totalTokens > 0 {
		p.Store.UpdateUsage(keyValue, totalTokens)
	}
}
