package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

// AnthropicProxy proxies requests to the Anthropic-compatible upstream endpoint.
type AnthropicProxy struct {
	Config *config.Config
	Store  *storage.KeyStore
}

// Proxy handles a single Anthropic-compatible proxy request.
func (p *AnthropicProxy) Proxy(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey) {
	model := GetModelForKey(apiKey, p.Config.DefaultModel)
	upstreamKey := apiKey.GetUpstreamKey(p.Config.MasterKey)

	// Anthropic uses path as-is (e.g., /v1/messages)
	upstreamURL := p.Config.AnthropicUpstream + r.URL.Path

	// Read and inject model, then check if client wants streaming
	body, bodyMap, err := readAndInjectModelWithMap(r.Body, r.URL.Path, r.Method, model)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Determine if client wants streaming — if stream is false or missing, we need to force stream
	clientWantsStream := false
	if stream, ok := bodyMap["stream"]; ok {
		if b, ok := stream.(bool); ok {
			clientWantsStream = b
		}
	}

	if !clientWantsStream {
		p.proxyNonStreaming(w, r, apiKey, body, bodyMap, upstreamURL, upstreamKey)
		return
	}

	// Streaming: use existing behavior
	p.proxyStreaming(w, r, apiKey, body, upstreamURL, upstreamKey)
}

// proxyStreaming handles streaming requests — passes through to upstream and relays SSE.
func (p *AnthropicProxy) proxyStreaming(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey, body io.ReadCloser, upstreamURL, upstreamKey string) {
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

	// Non-streaming: relay response and track tokens + cost
	relayResponse(w, resp, p.Store, apiKey.Key, extractAnthropicTokens)
}

// proxyNonStreaming handles non-streaming requests by forcing stream:true upstream,
// buffering the SSE response, and converting it to a single Anthropic JSON response.
func (p *AnthropicProxy) proxyNonStreaming(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey, originalBody io.ReadCloser, bodyMap map[string]interface{}, upstreamURL, upstreamKey string) {
	// Force stream: true in the body for upstream
	bodyMap["stream"] = true
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to marshal request body")
		return
	}

	upstreamReq, err := http.NewRequest(r.Method, upstreamURL, strings.NewReader(string(bodyBytes)))
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
	defer resp.Body.Close()

	// Extract cost from upstream response headers (LiteLLM)
	cost := parseCostFromHeader(resp.Header)

	// Check if upstream actually returned SSE (it should since we forced stream:true)
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		// Buffer the SSE stream and convert to a single Anthropic message
		buffer := NewAnthropicStreamBuffer()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			buffer.ProcessLine(scanner.Text())
		}

		message := buffer.ToAnthropicMessage()
		respJSON, err := json.Marshal(message)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to marshal response")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respJSON)

		// Track tokens and cost
		p.Store.UpdateUsage(apiKey.Key, buffer.InputTokens+buffer.OutputTokens+buffer.CacheCreation+buffer.CacheRead, buffer.CacheCreation+buffer.CacheRead, cost)
		return
	}

	// Fallback: upstream returned non-SSE (shouldn't happen but handle gracefully)
	// Try to parse as Anthropic JSON first, then fall back to OpenAI conversion
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Failed to read upstream response")
		return
	}

	// Try parsing as Anthropic format
	var anthroResult map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &anthroResult); err == nil {
		// Check if it's actually Anthropic format
		if anthroResult["type"] == "message" || anthroResult["content"] != nil {
			// Already Anthropic format, relay as-is
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(bodyBytes)
			result := extractAnthropicTokens(bodyBytes)
			p.Store.UpdateUsage(apiKey.Key, result.Total, result.Cached, cost)
			return
		}
		// Might be an error response from LiteLLM — try OpenAI conversion
		converted, err := ConvertOpenAIToAnthropic(bodyBytes)
		if err == nil && converted["content"] != nil {
			respJSON, _ := json.Marshal(converted)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(respJSON)
			result := extractAnthropicTokens(respJSON)
			p.Store.UpdateUsage(apiKey.Key, result.Total, result.Cached, cost)
			return
		}
	}

	// Last resort: return the raw response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(bodyBytes)
}

// readAndInjectModelWithMap reads the request body, injects the model field,
// and returns both the modified body as a ReadCloser and the parsed body map.
func readAndInjectModelWithMap(body io.ReadCloser, path, method, model string) (io.ReadCloser, map[string]interface{}, error) {
	if body == nil || (method != "POST" && method != "PUT" && method != "PATCH") {
		return body, nil, nil
	}

	var bodyMap map[string]interface{}
	if err := json.NewDecoder(body).Decode(&bodyMap); err != nil {
		return body, nil, nil // not JSON, pass through
	}
	body.Close()

	// Only inject model for relevant paths, and only if client didn't specify one
	if strings.Contains(path, "/chat/completions") || strings.Contains(path, "/completions") || strings.Contains(path, "/messages") {
		if _, hasModel := bodyMap["model"]; !hasModel {
			bodyMap["model"] = model
		}
	}

	b, err := json.Marshal(bodyMap)
	if err != nil {
		return io.NopCloser(strings.NewReader("{}")), bodyMap, err
	}
	return io.NopCloser(strings.NewReader(string(b))), bodyMap, nil
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
	input := toFloat64(usage["input_tokens"])
	output := toFloat64(usage["output_tokens"])
	cacheRead := toFloat64(usage["cache_read_input_tokens"])
	cacheCreation := toFloat64(usage["cache_creation_input_tokens"])
	return TokenResult{
		Total:  int(input) + int(output) + int(cacheRead) + int(cacheCreation),
		Cached: int(cacheRead) + int(cacheCreation),
	}
}

// streamSSE proxies an SSE stream with inline token counting and cost tracking.
func (p *AnthropicProxy) streamSSE(w http.ResponseWriter, resp *http.Response, keyValue string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	// Extract cost from upstream response headers (LiteLLM)
	cost := parseCostFromHeader(resp.Header)

	totalTokens := StreamSSE(w, resp.Body, "anthropic")
	// resp.Body is closed by StreamSSE via defer

	// Always update usage — records the request and any partial tokens collected
	p.Store.UpdateUsage(keyValue, totalTokens.Total, totalTokens.Cached, cost)
}
