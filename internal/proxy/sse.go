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

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			fmt.Fprintf(w, "\n")
			flusher.Flush()
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := line[6:]

			if data == "[DONE]" {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				break
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
