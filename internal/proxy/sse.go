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
// Returns the total tokens parsed from the stream.
func StreamSSE(w http.ResponseWriter, body io.ReadCloser, mode string) int {
	flusher, ok := w.(http.Flusher)
	if !ok {
		io.Copy(w, body)
		body.Close()
		return 0
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	totalTokens := 0

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

			tokens := parseSSETokens(data, mode)
			totalTokens += tokens

			fmt.Fprintf(w, "data: %s\n", data)
		} else {
			fmt.Fprintf(w, "%s\n", line)
		}

		flusher.Flush()
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		fmt.Fprintf(w, "data: {\"error\": \"stream interrupted\"}\n\n")
		flusher.Flush()
	}

	body.Close()
	return totalTokens
}

// parseSSETokens attempts to extract token counts from a single SSE data chunk.
func parseSSETokens(data string, mode string) int {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return 0
	}

	usage, ok := m["usage"].(map[string]interface{})
	if !ok {
		return 0
	}

	switch mode {
	case "openai":
		if total, ok := usage["total_tokens"].(float64); ok {
			return int(total)
		}
	case "anthropic":
		input, _ := usage["input_tokens"].(float64)
		output, _ := usage["output_tokens"].(float64)
		return int(input) + int(output)
	}

	return 0
}
