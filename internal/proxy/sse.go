package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamSSE reads from upstream body and writes SSE chunks to the client.
// Returns the tokens parsed from the stream.
// resp.Body is closed via defer.
// For "anthropic" mode: deduplicates message_start events and strips provider
// prefix from model names (e.g., "glm/glm-5-turbo" → "glm-5-turbo").
func StreamSSE(w http.ResponseWriter, body io.ReadCloser, mode string) TokenResult {
	defer body.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		io.Copy(w, body)
		return TokenResult{}
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var result TokenResult
	var (
		seenMessageStart bool  // track first message_start for dedup
		lastEvent        string
		skipNextData     bool  // skip the data line following a skipped duplicate event
	)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			fmt.Fprintf(w, "\n")
			flusher.Flush()
			continue
		}

		// Track event type for the data line that follows
		if strings.HasPrefix(line, "event: ") {
			lastEvent = strings.TrimSpace(line[7:])

			// For anthropic mode, skip duplicate message_start event lines
			if mode == "anthropic" && lastEvent == "message_start" && seenMessageStart {
				skipNextData = true
				continue
			}

			fmt.Fprintf(w, "%s\n", line)
			flusher.Flush()
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := line[6:]

			// Skip data line for a skipped duplicate event
			if skipNextData {
				skipNextData = false
				continue
			}

			if data == "[DONE]" {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				break
			}

			// Anthropic mode fixes
			if mode == "anthropic" && lastEvent == "message_start" {
				seenMessageStart = true
				data = stripModelPrefix(data)
			}

			chunk := parseSSETokens(data, mode)
			result.Total += chunk.Total
			result.Cached += chunk.Cached

			fmt.Fprintf(w, "data: %s\n", data)
		} else {
			fmt.Fprintf(w, "%s\n", line)
		}

		flusher.Flush()
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		fmt.Fprint(w, "event: error\ndata: {\"error\": \"stream interrupted\"}\n\n")
		flusher.Flush()
	}

	return result
}

// stripModelPrefix removes the provider prefix from the model field in SSE data.
// Only modifies "message_start" events. Example: "glm/glm-5-turbo" → "glm-5-turbo".
func stripModelPrefix(data string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data
	}

	// Only modify message_start events
	if m["type"] != "message_start" {
		return data
	}

	msg, ok := m["message"].(map[string]interface{})
	if !ok {
		return data
	}

	model, ok := msg["model"].(string)
	if !ok || model == "" {
		return data
	}

	// Strip provider prefix (e.g., "glm/glm-5-turbo" → "glm-5-turbo")
	if idx := strings.Index(model, "/"); idx >= 0 {
		msg["model"] = model[idx+1:]
	}

	updated, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(updated)
}

// parseSSETokens attempts to extract token counts from a single SSE data chunk.
func parseSSETokens(data string, mode string) TokenResult {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return TokenResult{}
	}

	usage, ok := m["usage"].(map[string]interface{})
	if !ok {
		return TokenResult{}
	}

	switch mode {
	case "openai":
		if total, ok := usage["total_tokens"].(float64); ok {
			cached := 0
			if details, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
				if ct, ok := details["cached_tokens"].(float64); ok {
					cached = int(ct)
				}
			}
			return TokenResult{Total: int(total), Cached: cached}
		}
	case "anthropic":
		input, _ := usage["input_tokens"].(float64)
		output, _ := usage["output_tokens"].(float64)
		cacheRead, _ := usage["cache_read_input_tokens"].(float64)
		cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)
		return TokenResult{
			Total:  int(input) + int(output) + int(cacheRead) + int(cacheCreation),
			Cached: int(cacheRead) + int(cacheCreation),
		}
	}

	return TokenResult{}
}
