package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

// sharedClient is reused across requests for connection pooling.
var sharedClient = &http.Client{
	Timeout: 300 * time.Second, // match server write timeout
}

// OpenAIProxy proxies requests to the OpenAI-compatible upstream endpoint.
type OpenAIProxy struct {
	Config *config.Config
	Store  *storage.KeyStore
}

// Proxy handles a single OpenAI-compatible proxy request.
func (p *OpenAIProxy) Proxy(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey) {
	model := GetModelForKey(apiKey, p.Config.DefaultModel)
	upstreamKey := apiKey.GetUpstreamKey(p.Config.MasterKey)

	// Build upstream URL: strip /v1/ prefix
	cleanPath := strings.TrimPrefix(r.URL.Path, "/v1")
	upstreamURL := p.Config.OpenAIUpstream + cleanPath

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
	resp, err := sharedClient.Do(upstreamReq)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Upstream request failed")
		return
	}

	// Check for SSE streaming
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		p.streamSSE(w, resp, apiKey.Key)
		return
	}
	defer resp.Body.Close()

	// Non-streaming: relay response and track tokens + cost
	relayResponse(w, resp, p.Store, apiKey.Key, extractOpenAITokens)
}

// extractOpenAITokens parses total_tokens from OpenAI usage response.
// OpenAI total_tokens already includes cached tokens in the count.
// prompt_tokens_details.cached_tokens is tracked separately for visibility.
func extractOpenAITokens(body []byte) TokenResult {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return TokenResult{}
	}
	usage, ok := result["usage"].(map[string]interface{})
	if !ok {
		return TokenResult{}
	}
	total, ok := usage["total_tokens"].(float64)
	if !ok {
		return TokenResult{}
	}
	cached := 0
	if details, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
		if ct, ok := details["cached_tokens"].(float64); ok {
			cached = int(ct)
		}
	}
	return TokenResult{Total: int(total), Cached: cached}
}

// streamSSE proxies an SSE stream with inline token counting and cost tracking.
func (p *OpenAIProxy) streamSSE(w http.ResponseWriter, resp *http.Response, keyValue string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	// Extract cost from upstream response headers (LiteLLM)
	cost := parseCostFromHeader(resp.Header)

	totalTokens := StreamSSE(w, resp.Body, "openai")
	// resp.Body is closed by StreamSSE via defer

	// Always update usage — records the request and any partial tokens collected
	p.Store.UpdateUsage(keyValue, totalTokens.Total, totalTokens.Cached, cost)
}
