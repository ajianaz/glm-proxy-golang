package proxy

import (
	"encoding/json"
	"testing"
)

func TestOpenAIToAnthropic_Basic(t *testing.T) {
	openaiJSON := `{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"choices": [{"message": {"role": "assistant", "content": "Hello, world!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	result, err := OpenAIToAnthropic([]byte(openaiJSON))
	if err != nil {
		t.Fatal(err)
	}

	if result["id"] != "msg_abc123" {
		t.Fatalf("expected id msg_abc123, got %v", result["id"])
	}
	if result["type"] != "message" {
		t.Fatalf("expected type message, got %v", result["type"])
	}
	if result["role"] != "assistant" {
		t.Fatalf("expected role assistant, got %v", result["role"])
	}
	if result["model"] != "" {
		t.Fatalf("expected empty model, got %v", result["model"])
	}
	if result["stop_reason"] != "end_turn" {
		t.Fatalf("expected stop_reason end_turn, got %v", result["stop_reason"])
	}

	// Check content
	content, ok := result["content"].([]map[string]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("expected 1 content block, got %v", result["content"])
	}
	if content[0]["type"] != "text" {
		t.Fatalf("expected text type, got %v", content[0]["type"])
	}
	if content[0]["text"] != "Hello, world!" {
		t.Fatalf("expected 'Hello, world!', got %v", content[0]["text"])
	}

	// Check usage
	usage, ok := result["usage"].(map[string]interface{})
	if !ok {
		t.Fatal("expected usage map")
	}
	if int(usage["input_tokens"].(float64)) != 10 {
		t.Fatalf("expected input_tokens 10, got %v", usage["input_tokens"])
	}
	if int(usage["output_tokens"].(float64)) != 5 {
		t.Fatalf("expected output_tokens 5, got %v", usage["output_tokens"])
	}
}

func TestOpenAIToAnthropic_WithModelPrefix(t *testing.T) {
	openaiJSON := `{
		"id": "chatcmpl-xxx",
		"object": "chat.completion",
		"choices": [{"message": {"role": "assistant", "content": "Hi"}, "finish_reason": "stop"}],
		"model": "glm/glm-5.1",
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	result, err := OpenAIToAnthropic([]byte(openaiJSON))
	if err != nil {
		t.Fatal(err)
	}

	if result["model"] != "glm-5.1" {
		t.Fatalf("expected model glm-5.1, got %v", result["model"])
	}
}

func TestOpenAIToAnthropic_FinishReasonLength(t *testing.T) {
	openaiJSON := `{
		"id": "chatcmpl-xxx",
		"object": "chat.completion",
		"choices": [{"message": {"role": "assistant", "content": "Long response..."}, "finish_reason": "length"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 100, "total_tokens": 110}
	}`

	result, err := OpenAIToAnthropic([]byte(openaiJSON))
	if err != nil {
		t.Fatal(err)
	}

	if result["stop_reason"] != "max_tokens" {
		t.Fatalf("expected stop_reason max_tokens, got %v", result["stop_reason"])
	}
}

func TestConvertOpenAIToAnthropic_Alias(t *testing.T) {
	openaiJSON := `{
		"id": "chatcmpl-alias",
		"object": "chat.completion",
		"choices": [{"message": {"role": "assistant", "content": "test"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	}`

	result, err := ConvertOpenAIToAnthropic([]byte(openaiJSON))
	if err != nil {
		t.Fatal(err)
	}

	if result["id"] != "msg_alias" {
		t.Fatalf("expected msg_alias, got %v", result["id"])
	}
}

func TestOpenAIToAnthropic_WithCacheTokens(t *testing.T) {
	openaiJSON := `{
		"id": "chatcmpl-cache",
		"object": "chat.completion",
		"choices": [{"message": {"role": "assistant", "content": "cached reply"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110, "prompt_tokens_details": {"cached_tokens": 80}}
	}`

	result, err := OpenAIToAnthropic([]byte(openaiJSON))
	if err != nil {
		t.Fatal(err)
	}

	usage, _ := result["usage"].(map[string]interface{})
	if int(usage["cache_read_input_tokens"].(float64)) != 80 {
		t.Fatalf("expected cache_read_input_tokens 80, got %v", usage["cache_read_input_tokens"])
	}
}

func TestOpenAIToAnthropic_InvalidJSON(t *testing.T) {
	_, err := OpenAIToAnthropic([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestAnthropicStreamBuffer_FullLifecycle(t *testing.T) {
	buf := NewAnthropicStreamBuffer()

	// Simulate a complete Anthropic SSE stream
	events := []string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test123","type":"message","role":"assistant","content":[],"model":"glm/glm-5.1","usage":{"input_tokens":2020,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":2020,"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
	}

	for _, line := range events {
		buf.ProcessLine(line)
	}

	// Verify collected data
	if buf.ID != "msg_test123" {
		t.Fatalf("expected ID msg_test123, got %s", buf.ID)
	}
	if buf.Model != "glm-5.1" {
		t.Fatalf("expected model glm-5.1, got %s", buf.Model)
	}
	if buf.Content.String() != "Hello world" {
		t.Fatalf("expected 'Hello world', got '%s'", buf.Content.String())
	}
	if buf.StopReason != "end_turn" {
		t.Fatalf("expected stop_reason end_turn, got %s", buf.StopReason)
	}
	if buf.InputTokens != 2020 {
		t.Fatalf("expected input_tokens 2020, got %d", buf.InputTokens)
	}
	if buf.OutputTokens != 5 {
		t.Fatalf("expected output_tokens 5, got %d", buf.OutputTokens)
	}

	// Convert to Anthropic message
	msg := buf.ToAnthropicMessage()

	if msg["id"] != "msg_test123" {
		t.Fatalf("expected id msg_test123, got %v", msg["id"])
	}
	if msg["type"] != "message" {
		t.Fatalf("expected type message, got %v", msg["type"])
	}
	if msg["role"] != "assistant" {
		t.Fatalf("expected role assistant, got %v", msg["role"])
	}
	if msg["model"] != "glm-5.1" {
		t.Fatalf("expected model glm-5.1, got %v", msg["model"])
	}

	// Verify content is serializable and correct
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(msgJSON, &parsed)

	content, ok := parsed["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("expected 1 content block, got %v", parsed["content"])
	}
	block := content[0].(map[string]interface{})
	if block["type"] != "text" {
		t.Fatalf("expected text block type, got %v", block["type"])
	}
	if block["text"] != "Hello world" {
		t.Fatalf("expected 'Hello world', got %v", block["text"])
	}

	// Verify usage
	usage := parsed["usage"].(map[string]interface{})
	if int(usage["input_tokens"].(float64)) != 2020 {
		t.Fatalf("expected input_tokens 2020, got %v", usage["input_tokens"])
	}
	if int(usage["output_tokens"].(float64)) != 5 {
		t.Fatalf("expected output_tokens 5, got %v", usage["output_tokens"])
	}
}

func TestAnthropicStreamBuffer_WithCacheTokens(t *testing.T) {
	buf := NewAnthropicStreamBuffer()

	buf.ProcessLine(`data: {"type":"message_start","message":{"id":"msg_cache","type":"message","role":"assistant","content":[],"model":"glm-5.1","usage":{"input_tokens":100,"output_tokens":0}}}`)
	buf.ProcessLine(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"cached response"}}`)
	buf.ProcessLine(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":50,"cache_creation_input_tokens":20}}`)

	msg := buf.ToAnthropicMessage()

	msgJSON, _ := json.Marshal(msg)
	var parsed map[string]interface{}
	json.Unmarshal(msgJSON, &parsed)

	usage := parsed["usage"].(map[string]interface{})
	if int(usage["cache_read_input_tokens"].(float64)) != 50 {
		t.Fatalf("expected cache_read 50, got %v", usage["cache_read_input_tokens"])
	}
	if int(usage["cache_creation_input_tokens"].(float64)) != 20 {
		t.Fatalf("expected cache_creation 20, got %v", usage["cache_creation_input_tokens"])
	}
}

func TestAnthropicStreamBuffer_EmptyStream(t *testing.T) {
	buf := NewAnthropicStreamBuffer()

	msg := buf.ToAnthropicMessage()

	if msg["id"] != "msg_unknown" {
		t.Fatalf("expected msg_unknown for empty stream, got %v", msg["id"])
	}
	if msg["model"] != "unknown" {
		t.Fatalf("expected unknown model for empty stream, got %v", msg["model"])
	}

	// Content should still be present but empty
	content := msg["content"].([]map[string]interface{})
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}
	if content[0]["text"] != "" {
		t.Fatalf("expected empty text, got %v", content[0]["text"])
	}
}

func TestAnthropicStreamBuffer_IgnoresNonDataLines(t *testing.T) {
	buf := NewAnthropicStreamBuffer()

	// These should be ignored (no "data: " prefix)
	buf.ProcessLine("event: message_start")
	buf.ProcessLine(": this is a comment")
	buf.ProcessLine("")
	buf.ProcessLine("event: ping")
	buf.ProcessLine("data: [DONE]")

	msg := buf.ToAnthropicMessage()
	if msg["id"] != "msg_unknown" {
		t.Fatalf("expected msg_unknown, got %v", msg["id"])
	}
}

func TestMapOpenAIFinishReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"content_filter", "end_turn"},
		{"unknown", "end_turn"},
		{"", "end_turn"},
	}
	for _, tt := range tests {
		got := mapOpenAIFinishReason(tt.input)
		if got != tt.expected {
			t.Errorf("mapOpenAIFinishReason(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
