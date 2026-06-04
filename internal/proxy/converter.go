package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAIToAnthropic converts an OpenAI chat completion JSON response to an Anthropic message JSON response.
// This is used as a fallback when the upstream returns an OpenAI-format response instead of Anthropic.
func OpenAIToAnthropic(data []byte) (map[string]interface{}, error) {
	var openai map[string]interface{}
	if err := json.Unmarshal(data, &openai); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	// Extract content from choices
	var textContent string
	stopReason := "end_turn"
	if choices, ok := openai["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					textContent = content
				}
			}
			if fr, ok := choice["finish_reason"].(string); ok {
				stopReason = mapOpenAIFinishReason(fr)
			}
		}
	}

	// Extract model — strip provider prefix (e.g., "glm/glm-5.1" → "glm-5.1")
	model := ""
	if m, ok := openai["model"].(string); ok {
		model = m
		if idx := strings.Index(model, "/"); idx >= 0 {
			model = model[idx+1:]
		}
	}

	// Extract ID — map "chatcmpl-xxx" → "msg_xxx"
	id := ""
	if rawID, ok := openai["id"].(string); ok {
		id = strings.Replace(rawID, "chatcmpl-", "msg_", 1)
	} else {
		id = "msg_unknown"
	}

	// Extract usage
	var inputTokens, outputTokens, cacheCreation, cacheRead float64
	if usage, ok := openai["usage"].(map[string]interface{}); ok {
		inputTokens = toFloat64(usage["prompt_tokens"])
		outputTokens = toFloat64(usage["completion_tokens"])
		if details, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
			cacheRead = toFloat64(details["cached_tokens"])
		}
	}

	result := map[string]interface{}{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]interface{}{{"type": "text", "text": textContent}},
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":               inputTokens,
			"output_tokens":              outputTokens,
			"cache_creation_input_tokens": cacheCreation,
			"cache_read_input_tokens":    cacheRead,
		},
	}

	return result, nil
}

// ConvertOpenAIToAnthropic is an alias for OpenAIToAnthropic for clarity in usage.
// Both names convert OpenAI JSON to Anthropic JSON format.
func ConvertOpenAIToAnthropic(data []byte) (map[string]interface{}, error) {
	return OpenAIToAnthropic(data)
}

// toFloat64 safely extracts a float64 from an interface{} (handles both float64 and int).
func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// mapOpenAIFinishReason converts OpenAI finish_reason to Anthropic stop_reason.
func mapOpenAIFinishReason(fr string) string {
	switch fr {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// AnthropicStreamBuffer collects Anthropic SSE events into a final message.
// It processes line-by-line SSE data from an Anthropic streaming response.
type AnthropicStreamBuffer struct {
	Content    strings.Builder
	StopReason string
	Model      string
	ID         string
	InputTokens  int
	OutputTokens int
	CacheCreation int
	CacheRead    int
}

// NewAnthropicStreamBuffer creates a new buffer for collecting Anthropic SSE events.
func NewAnthropicStreamBuffer() *AnthropicStreamBuffer {
	return &AnthropicStreamBuffer{
		StopReason: "end_turn",
		Model:      "",
		ID:         "",
	}
}

// ProcessLine processes a single SSE line from an Anthropic streaming response.
// It handles both "event:" lines and "data:" lines.
func (b *AnthropicStreamBuffer) ProcessLine(line string) {
	// Only process data lines
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := line[6:]
	if data == "[DONE]" {
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "message_start":
		// Extract message metadata (id, model, initial usage)
		if msg, ok := event["message"].(map[string]interface{}); ok {
			if id, ok := msg["id"].(string); ok {
				b.ID = id
			}
			if model, ok := msg["model"].(string); ok {
				b.Model = model
				// Strip provider prefix (e.g., "glm/glm-5.1" → "glm-5.1")
				if idx := strings.Index(b.Model, "/"); idx >= 0 {
					b.Model = b.Model[idx+1:]
				}
			}
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				b.InputTokens = int(toFloat64(usage["input_tokens"]))
			}
		}

	case "content_block_delta":
		// Extract text content
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			if text, ok := delta["text"].(string); ok {
				b.Content.WriteString(text)
			}
		}

	case "message_delta":
		// Extract stop_reason and final usage
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			if sr, ok := delta["stop_reason"].(string); ok {
				b.StopReason = sr
			}
		}
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			b.InputTokens = int(toFloat64(usage["input_tokens"]))
			b.OutputTokens = int(toFloat64(usage["output_tokens"]))
			b.CacheCreation = int(toFloat64(usage["cache_creation_input_tokens"]))
			b.CacheRead = int(toFloat64(usage["cache_read_input_tokens"]))
		}
	}
}

// ToAnthropicMessage converts the collected stream data into a single Anthropic message response.
// If clientModel is non-empty, it overrides the model name from the upstream response
// (needed for Claude Code CLI compatibility).
func (b *AnthropicStreamBuffer) ToAnthropicMessage(clientModel ...string) map[string]interface{} {
	id := b.ID
	if id == "" {
		id = "msg_unknown"
	}
	model := b.Model
	if len(clientModel) > 0 && clientModel[0] != "" {
		model = clientModel[0]
	}
	if model == "" {
		model = "unknown"
	}

	// Estimate tokens if upstream reported zero
	inputTokens := b.InputTokens
	outputTokens := b.OutputTokens
	contentLen := b.Content.Len()
	if outputTokens == 0 && contentLen > 0 {
		outputTokens = contentLen / charsPerToken
		if outputTokens < 1 {
			outputTokens = 1
		}
	}
	if inputTokens == 0 && contentLen > 0 {
		inputTokens = 1000 // conservative estimate
	}

	return map[string]interface{}{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]interface{}{{"type": "text", "text": b.Content.String()}},
		"model":         model,
		"stop_reason":   b.StopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":               float64(inputTokens),
			"output_tokens":              float64(outputTokens),
			"cache_creation_input_tokens": float64(b.CacheCreation),
			"cache_read_input_tokens":    float64(b.CacheRead),
		},
	}
}
