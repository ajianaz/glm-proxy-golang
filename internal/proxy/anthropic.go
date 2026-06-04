package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"glm-proxy/internal/config"
	"glm-proxy/internal/modelmap"
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

	// Read, map, and inject model, then check if client wants streaming
	body, bodyMap, clientModel, thinkingRequested, err := readAndInjectModelWithMap(r.Body, r.URL.Path, r.Method, model, p.Config.ModelMap)
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
		p.proxyNonStreaming(w, r, apiKey, body, bodyMap, upstreamURL, upstreamKey, clientModel, thinkingRequested)
		return
	}

	// Streaming: use existing behavior — clientModel is the original model name (before mapping)
	p.proxyStreaming(w, r, apiKey, body, upstreamURL, upstreamKey, clientModel)
}

// proxyStreaming handles streaming requests — passes through to upstream and relays SSE.
func (p *AnthropicProxy) proxyStreaming(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey, body io.ReadCloser, upstreamURL, upstreamKey, clientModel string) {
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
		p.streamSSE(w, resp, apiKey.Key, clientModel)
		return
	}
	defer resp.Body.Close()

	// Non-streaming: relay response and track tokens + cost
	relayResponse(w, resp, p.Store, apiKey.Key, extractAnthropicTokens)
}

// proxyNonStreaming handles non-streaming requests by forcing stream:true upstream,
// buffering the SSE response, and converting it to a single Anthropic JSON response.
func (p *AnthropicProxy) proxyNonStreaming(w http.ResponseWriter, r *http.Request, apiKey *storage.ApiKey, originalBody io.ReadCloser, bodyMap map[string]interface{}, upstreamURL, upstreamKey, clientModel string, thinkingRequested bool) {
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
		buffer.ThinkingRequested = thinkingRequested
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			buffer.ProcessLine(scanner.Text())
		}

		message := buffer.ToAnthropicMessage(clientModel)
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

// readAndInjectModelWithMap reads the request body, applies model mapping,
// injects the model field (if missing), and returns the modified body
// as a ReadCloser, the parsed body map, the original client-requested model
// (before mapping), and whether thinking was requested.
func readAndInjectModelWithMap(body io.ReadCloser, path, method, model string, mm *modelmap.ModelMap) (io.ReadCloser, map[string]interface{}, string, bool, error) {
	if body == nil || (method != "POST" && method != "PUT" && method != "PATCH") {
		return body, nil, "", false, nil
	}

	var bodyMap map[string]interface{}
	if err := json.NewDecoder(body).Decode(&bodyMap); err != nil {
		return body, nil, "", false, nil // not JSON, pass through
	}
	body.Close()

	originalClientModel := ""
	thinkingRequested := false

	// Only inject model for relevant paths
	if strings.Contains(path, "/chat/completions") || strings.Contains(path, "/completions") || strings.Contains(path, "/messages") {
		if clientModel, hasModel := bodyMap["model"].(string); hasModel {
			originalClientModel = clientModel // save before mapping
			// Apply model mapping to client-specified model
			if mm != nil && mm.Enabled() {
				bodyMap["model"] = mm.Resolve(clientModel)
			}
		} else {
			// No model specified — inject default
			bodyMap["model"] = model
		}

		// Strip extended thinking parameters — upstream (LiteLLM/GLM) doesn't support them.
		// Save a flag before stripping so the response can synthesize thinking blocks.
		if _, hasThinking := bodyMap["thinking"]; hasThinking {
			thinkingRequested = true
		}
		delete(bodyMap, "thinking")
		delete(bodyMap, "budget_tokens")

		// Claude Code v2.1.154+ regression: injects role:"system" inside messages[]
		// which violates the Anthropic API schema (only user/assistant allowed).
		// Extract system messages and merge into the top-level "system" field.
		extractSystemMessages(bodyMap)
	}

	b, err := json.Marshal(bodyMap)
	if err != nil {
		return io.NopCloser(strings.NewReader("{}")), bodyMap, originalClientModel, thinkingRequested, err
	}
	return io.NopCloser(strings.NewReader(string(b))), bodyMap, originalClientModel, thinkingRequested, nil
}

// extractSystemMessages finds messages with role:"system" in the messages array,
// moves their content to the top-level "system" field, and removes them from messages.
// This is needed because Claude Code v2.1.154+ sends system role inside messages[]
// which breaks Anthropic-compatible providers that enforce strict schema validation.
func extractSystemMessages(bodyMap map[string]interface{}) {
	msgsRaw, ok := bodyMap["messages"]
	if !ok {
		return
	}
	msgs, ok := msgsRaw.([]interface{})
	if !ok {
		return
	}

	var userMsgs []interface{}
	var systemParts []interface{}

	for _, m := range msgs {
		msg, ok := m.(map[string]interface{})
		if !ok {
			userMsgs = append(userMsgs, m)
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" {
			userMsgs = append(userMsgs, m)
			continue
		}
		// Extract content from system message
		if content, ok := msg["content"]; ok {
			systemParts = append(systemParts, content)
		}
	}

	if len(systemParts) == 0 {
		return
	}

	// Build the top-level system field
	switch len(systemParts) {
	case 1:
		bodyMap["system"] = systemParts[0]
	default:
		bodyMap["system"] = systemParts
	}

	bodyMap["messages"] = userMsgs
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
func (p *AnthropicProxy) streamSSE(w http.ResponseWriter, resp *http.Response, keyValue, clientModel string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	// Extract cost from upstream response headers (LiteLLM)
	cost := parseCostFromHeader(resp.Header)

	totalTokens := StreamSSE(w, resp.Body, "anthropic", clientModel)
	// resp.Body is closed by StreamSSE via defer

	// Always update usage — records the request and any partial tokens collected
	p.Store.UpdateUsage(keyValue, totalTokens.Total, totalTokens.Cached, cost)
}
