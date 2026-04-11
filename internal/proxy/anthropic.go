package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

// AnthropicProxy proxies requests to the Anthropic-compatible BigModel endpoint.
type AnthropicProxy struct {
	Config *config.Config
	Store  *storage.KeyStore
}

// Proxy handles a single Anthropic-compatible proxy request.
func (p *AnthropicProxy) Proxy(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey) {
	model := GetModelForKey(apiKey, p.Config.DefaultModel)
	upstreamKey := apiKey.UpstreamKey(p.Config.ZaiApiKey)

	// Anthropic uses path as-is (e.g., /v1/messages)
	upstreamURL := AnthropicUpstream + r.URL.Path

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

	// Auth: x-api-key header (Anthropic convention)
	upstreamReq.Header.Set("x-api-key", upstreamKey)
	forwardHeaders(upstreamReq.Header, r.Header,
		"authorization", "x-api-key")

	// Forward anthropic-version header
	if av := r.Header.Get("anthropic-version"); av != "" {
		upstreamReq.Header.Set("anthropic-version", av)
	} else {
		upstreamReq.Header.Set("anthropic-version", "2023-06-01")
	}

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

	// Non-streaming: relay response and track tokens
	relayResponse(w, resp, p.Store, apiKey.Key, extractAnthropicTokens)
}

// extractAnthropicTokens parses all token fields from Anthropic usage response.
// Anthropic cache tokens are separate from input_tokens, must sum all for accuracy.
func extractAnthropicTokens(body []byte) TokenResult {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return TokenResult{}
	}
	usage, ok := result["usage"].(map[string]interface{})
	if !ok {
		return TokenResult{}
	}
	input, _ := usage["input_tokens"].(float64)
	output, _ := usage["output_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)
	return TokenResult{
		Total:  int(input) + int(output) + int(cacheRead) + int(cacheCreation),
		Cached: int(cacheRead) + int(cacheCreation),
	}
}

// streamSSE proxies an SSE stream with inline token counting.
func (p *AnthropicProxy) streamSSE(w http.ResponseWriter, resp *http.Response, keyValue string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	totalTokens := StreamSSE(w, resp.Body, "anthropic")
	// resp.Body is closed by StreamSSE via defer

	// Always update usage — records the request and any partial tokens collected
	p.Store.UpdateUsage(keyValue, totalTokens.Total, totalTokens.Cached)
}
