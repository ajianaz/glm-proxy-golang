package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"glm-proxy/internal/modelmap"
	"glm-proxy/internal/storage"
)

// GetModelForKey resolves the model for a key: per-key > env default > hardcoded.
func GetModelForKey(key *storage.ApiKey, defaultModel string) string {
	if key.Model != "" {
		return key.Model
	}
	if defaultModel != "" {
		return defaultModel
	}
	return "glm-4.7"
}

// TokenResult holds extracted token counts and cost from an upstream response.
type TokenResult struct {
	Total  int
	Cached int
	Cost   float64 // USD, parsed from x-litellm-response-cost header
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// forwardHeaders copies relevant client headers to the upstream request.
func forwardHeaders(dst, src http.Header, exclude ...string) {
	lowerExcl := make(map[string]bool, len(exclude))
	for _, h := range exclude {
		lowerExcl[strings.ToLower(h)] = true
	}

	for k, vals := range src {
		switch strings.ToLower(k) {
		case "content-type", "accept", "user-agent":
			if !lowerExcl[k] {
				for _, v := range vals {
					dst.Add(k, v)
				}
			}
		}
	}
}

// parseCostFromHeader extracts spend USD from x-litellm-response-cost header.
// Returns 0 if header is missing or unparseable (graceful degradation).
func parseCostFromHeader(h http.Header) float64 {
	v := h.Get("x-litellm-response-cost")
	if v == "" {
		return 0
	}
	cost := 0.0
	for _, c := range strings.Split(v, ",") {
		c = strings.TrimSpace(c)
		if n, err := parseFloat(c); err == nil {
			cost += n
		}
	}
	return cost
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// readAndInjectModel reads the request body, applies model mapping (if configured),
// injects the model field (if missing), and returns the modified body as a new ReadCloser.
// Returns nil if no injection is needed.
func readAndInjectModel(body io.ReadCloser, path, method, model string, mm *modelmap.ModelMap) (io.ReadCloser, error) {
	if body == nil || (method != "POST" && method != "PUT" && method != "PATCH") {
		return body, nil
	}

	var bodyMap map[string]interface{}
	if err := json.NewDecoder(body).Decode(&bodyMap); err != nil {
		return body, nil // not JSON, pass through
	}
	body.Close()

	// Only inject model for relevant paths
	if strings.Contains(path, "/chat/completions") || strings.Contains(path, "/completions") || strings.Contains(path, "/messages") {
		if clientModel, hasModel := bodyMap["model"].(string); hasModel {
			// Apply model mapping to client-specified model
			if mm != nil && mm.Enabled() {
				bodyMap["model"] = mm.Resolve(clientModel)
			}
			// If client specified model, use it (mapped or original)
		} else {
			// No model specified — inject default
			bodyMap["model"] = model
		}

		// Request usage stats in SSE streams so we can track tokens
		if stream, _ := bodyMap["stream"].(bool); stream {
			if strings.Contains(path, "/messages") {
				// Anthropic: stream already includes usage by default, no extra flag needed
			} else {
				// OpenAI-compatible: need stream_options to get usage in last chunk
				bodyMap["stream_options"] = map[string]interface{}{
					"include_usage": true,
				}
			}
		}
	}

	b, err := json.Marshal(bodyMap)
	if err != nil {
		return io.NopCloser(strings.NewReader("{}")), err
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}
