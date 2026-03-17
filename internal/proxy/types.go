package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"glm-proxy/internal/storage"
)

var (
	OpenAIUpstream    = "https://api.z.ai/api/coding/paas/v4"
	AnthropicUpstream = "https://open.bigmodel.cn/api/anthropic"
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

// readAndInjectModel reads the request body, injects the model field, and returns
// the modified body as a new ReadCloser. Returns nil if no injection is needed.
func readAndInjectModel(body io.ReadCloser, path, method, model string) (io.ReadCloser, error) {
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
		bodyMap["model"] = model
	}

	b, err := json.Marshal(bodyMap)
	if err != nil {
		return io.NopCloser(strings.NewReader("{}")), err
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}
