package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// charsPerToken is a rough estimate for English/mixed content.
// Anthropic's actual tokenizer averages ~3.5-4.5 chars/token for mixed content.
const charsPerToken = 4

// StreamSSE reads from upstream body and writes SSE chunks to the client.
// Returns the tokens parsed from the stream.
// resp.Body is closed via defer.
// For "anthropic" mode: deduplicates message_start events, strips provider
// prefix from model names, and injects estimated token counts when upstream
// reports zero (needed for Claude Code CLI compatibility).
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
		seenMessageStart bool // track first message_start for dedup
		lastEvent        string
		skipNextData     bool // skip the data line following a skipped duplicate event

		// Token estimation: accumulate text length from content_block_delta events
		textCharCount int
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
			if mode == "anthropic" {
				if lastEvent == "message_start" {
					seenMessageStart = true
					data = stripModelPrefix(data)
				}

				// Track text content length for token estimation
				if lastEvent == "content_block_delta" {
					textCharCount += extractTextDeltaLen(data)
				}

				// Inject estimated tokens into message_delta if upstream reports 0
				if lastEvent == "message_delta" {
					data = injectEstimatedTokens(data, textCharCount)
				}
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

// extractTextDeltaLen returns the length of text in a content_block_delta event.
// Returns 0 if the event is not a text_delta or cannot be parsed.
func extractTextDeltaLen(data string) int {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return 0
	}

	delta, ok := m["delta"].(map[string]interface{})
	if !ok {
		return 0
	}

	if delta["type"] != "text_delta" {
		return 0
	}

	text, ok := delta["text"].(string)
	if !ok {
		return 0
	}

	return len(text)
}

// injectEstimatedTokens patches message_delta SSE data to include estimated
// token counts when the upstream reports zero output_tokens.
// Claude Code CLI uses output_tokens to determine if the response has content;
// if it's 0, Claude Code returns an empty result even when text deltas were received.
func injectEstimatedTokens(data string, textCharCount int) string {
	if textCharCount <= 0 {
		return data
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data
	}

	if m["type"] != "message_delta" {
		return data
	}

	usage, ok := m["usage"].(map[string]interface{})
	if !ok {
		return data
	}

	// Only inject if upstream reports 0 output tokens
	outputTokens, _ := usage["output_tokens"].(float64)
	if outputTokens > 0 {
		return data
	}

	// Estimate tokens from accumulated text length
	estimated := textCharCount / charsPerToken
	if estimated < 1 {
		estimated = 1
	}

	usage["output_tokens"] = float64(estimated)

	// Also estimate input tokens if zero (rough: system prompt + user message)
	inputTokens, _ := usage["input_tokens"].(float64)
	if inputTokens == 0 {
		// Conservative estimate for a typical Claude Code request
		usage["input_tokens"] = float64(1000)
	}

	updated, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(updated)
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
