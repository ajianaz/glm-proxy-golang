package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamSSE_ExtractsTokensFromLastChunk(t *testing.T) {
	sseData := "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"total_tokens\":42}}\n" +
		"data: [DONE]\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	result := StreamSSE(w, ioReadCloser(body), "openai")

	if result.Total != 42 {
		t.Fatalf("expected 42 tokens from SSE stream, got %d", result.Total)
	}

	out := w.Body.String()
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatal("expected [DONE] to be forwarded")
	}
}

func TestStreamSSE_AnthropicUsageInLastChunk(t *testing.T) {
	sseData := "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":25}}\n" +
		"data: [DONE]\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	result := StreamSSE(w, ioReadCloser(body), "anthropic")

	if result.Total != 35 {
		t.Fatalf("expected 35 tokens (10+25) from Anthropic SSE, got %d", result.Total)
	}
}

func TestStreamSSE_AnthropicWithCache(t *testing.T) {
	sseData := "data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":20,\"cache_read_input_tokens\":30,\"cache_creation_input_tokens\":5}}\n" +
		"data: [DONE]\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	result := StreamSSE(w, ioReadCloser(body), "anthropic")

	if result.Total != 65 {
		t.Fatalf("expected 65 total (10+20+30+5), got %d", result.Total)
	}
	if result.Cached != 35 {
		t.Fatalf("expected 35 cached (30+5), got %d", result.Cached)
	}
}

func TestParseSSETokens_OpenAI(t *testing.T) {
	data := `{"id":"chatcmpl-123","choices":[],"usage":{"total_tokens":42}}`
	result := parseSSETokens(data, "openai")
	if result.Total != 42 {
		t.Fatalf("expected 42, got %d", result.Total)
	}
}

func TestParseSSETokens_OpenAI_WithCache(t *testing.T) {
	data := `{"usage":{"total_tokens":150,"prompt_tokens_details":{"cached_tokens":80}}}`
	result := parseSSETokens(data, "openai")
	if result.Total != 150 {
		t.Fatalf("expected 150 total, got %d", result.Total)
	}
	if result.Cached != 80 {
		t.Fatalf("expected 80 cached, got %d", result.Cached)
	}
}

func TestParseSSETokens_Anthropic(t *testing.T) {
	data := `{"type":"message_delta","usage":{"input_tokens":10,"output_tokens":20}}`
	result := parseSSETokens(data, "anthropic")
	if result.Total != 30 {
		t.Fatalf("expected 30, got %d", result.Total)
	}
}

func TestParseSSETokens_InvalidJSON(t *testing.T) {
	result := parseSSETokens("not json", "openai")
	if result.Total != 0 {
		t.Fatalf("expected 0 for invalid json, got %d", result.Total)
	}
}

func TestStreamSSE_BasicForwarding(t *testing.T) {
	body := strings.NewReader("data: hello\ndata: world\ndata: [DONE]\n")
	w := httptest.NewRecorder()
	result := StreamSSE(w, ioReadCloser(body), "openai")

	out := w.Body.String()
	if !strings.Contains(out, "data: hello") || !strings.Contains(out, "data: world") {
		t.Fatalf("expected SSE data to be forwarded, got: %s", out)
	}
	if result.Total != 0 {
		t.Fatalf("expected 0 tokens, got %d", result.Total)
	}
}

func TestStreamSSE_AnthropicDedupMessageStart(t *testing.T) {
	sseData := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm/glm-5-turbo\"}}\n" +
		"\n" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm/glm-5-turbo\"}}\n" +
		"\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hi\"}}\n" +
		"\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n" +
		"\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	StreamSSE(w, ioReadCloser(body), "anthropic")

	out := w.Body.String()

	// Should only have one message_start
	msgStartCount := strings.Count(out, "event: message_start")
	if msgStartCount != 1 {
		t.Fatalf("expected 1 message_start event, got %d. Output:\n%s", msgStartCount, out)
	}

	// Model prefix should be stripped
	if strings.Contains(out, "glm/glm-5-turbo") {
		t.Fatal("expected model prefix to be stripped, still contains 'glm/glm-5-turbo'")
	}
	if !strings.Contains(out, "glm-5-turbo") {
		t.Fatal("expected 'glm-5-turbo' (stripped prefix) in output")
	}
}

func TestExtractTextDeltaLen(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "text_delta with text",
			input:    `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			expected: 5,
		},
		{
			name:     "text_delta with empty text",
			input:    `{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`,
			expected: 0,
		},
		{
			name:     "non-text delta",
			input:    `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}`,
			expected: 0,
		},
		{
			name:     "invalid JSON",
			input:    `not json`,
			expected: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTextDeltaLen(tt.input)
			if got != tt.expected {
				t.Fatalf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}

func TestInjectEstimatedTokens(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		charCount   int
		wantOutput  int
		wantInput   int
	}{
		{
			name:       "injects when output_tokens is 0",
			input:      `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":0}}`,
			charCount: 80, // 80/4 = 20 tokens
			wantOutput: 20,
			wantInput:  1000, // default estimate when input is also 0
		},
		{
			name:       "respects existing non-zero output_tokens",
			input:      `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":25}}`,
			charCount: 80,
			wantOutput: 25, // unchanged
			wantInput:  10, // unchanged
		},
		{
			name:       "minimum 1 token estimate",
			input:      `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":0}}`,
			charCount: 2, // 2/4 = 0, rounds up to 1
			wantOutput: 1,
			wantInput:  1000,
		},
		{
			name:       "zero charCount returns unchanged",
			input:      `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":0}}`,
			charCount: 0,
			wantOutput: 0, // unchanged
			wantInput:  0, // unchanged
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectEstimatedTokens(tt.input, tt.charCount)
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(result), &m); err != nil {
				t.Fatalf("failed to parse result: %v", err)
			}
			usage := m["usage"].(map[string]interface{})
			gotOutput := int(usage["output_tokens"].(float64))
			gotInput := int(usage["input_tokens"].(float64))
			if gotOutput != tt.wantOutput {
				t.Fatalf("output_tokens: expected %d, got %d", tt.wantOutput, gotOutput)
			}
			if gotInput != tt.wantInput {
				t.Fatalf("input_tokens: expected %d, got %d", tt.wantInput, gotInput)
			}
		})
	}
}

func TestStreamSSE_TokenEstimationE2E(t *testing.T) {
	// Full SSE stream simulating what Claude Code would receive
	sseData := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm-5-turbo\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n" +
		"\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
		"\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello, world!\"}}\n" +
		"\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" How are you?\"}}\n" +
		"\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n" +
		"\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}\n" +
		"\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n" +
		"\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	StreamSSE(w, ioReadCloser(body), "anthropic")

	out := w.Body.String()

	// "Hello, world!" (13) + " How are you?" (12) = 25 chars → 25/4 = 6 tokens
	if !strings.Contains(out, `"output_tokens":6`) {
		t.Fatalf("expected output_tokens:6 in output, got:\\n%s", out)
	}
	if !strings.Contains(out, `"input_tokens":1000`) {
		t.Fatalf("expected input_tokens:1000 in output, got:\\n%s", out)
	}
}

func TestStripModelPrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strips provider prefix",
			input:    `{"type":"message_start","message":{"id":"msg_1","model":"glm/glm-5-turbo"}}`,
			expected: `glm-5-turbo`,
		},
		{
			name:     "no prefix — unchanged",
			input:    `{"type":"message_start","message":{"id":"msg_1","model":"glm-5-turbo"}}`,
			expected: `glm-5-turbo`,
		},
		{
			name:     "non-message_start — unchanged",
			input:    `{"type":"message_delta","model":"glm/glm-5-turbo"}`,
			expected: `glm/glm-5-turbo`,
		},
		{
			name:     "invalid JSON — unchanged",
			input:    `not json`,
			expected: `not json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripModelPrefix(tt.input)
			if !strings.Contains(result, tt.expected) {
				t.Fatalf("expected %q in result, got: %s", tt.expected, result)
			}
		})
	}
}

func TestReplaceResponseModel(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		clientModel string
		expected    string
	}{
		{
			name:        "replaces model in message_start",
			input:       `{"type":"message_start","message":{"id":"msg_1","model":"glm-5-turbo"}}`,
			clientModel: "claude-sonnet-4-6",
			expected:    "claude-opus-4-8", // sonnet → opus (Claude Code compat)
		},
		{
			name:        "replaces model with prefix",
			input:       `{"type":"message_start","message":{"id":"msg_1","model":"glm/glm-5-turbo"}}`,
			clientModel: "claude-opus-4-8",
			expected:    "claude-opus-4-8",
		},
		{
			name:        "non-message_start — unchanged",
			input:       `{"type":"message_delta","model":"glm-5-turbo"}`,
			clientModel: "claude-sonnet-4-6",
			expected:    "glm-5-turbo", // not replaced
		},
		{
			name:        "invalid JSON — unchanged",
			input:       `not json`,
			clientModel: "claude-sonnet-4-6",
			expected:    "not json",
		},
		{
			name:        "empty clientModel — unchanged",
			input:       `{"type":"message_start","message":{"id":"msg_1","model":"glm-5-turbo"}}`,
			clientModel: "",
			expected:    "glm-5-turbo",
		},
		{
			name:        "sonnet model mapped to opus",
			input:       `{"type":"message_start","message":{"id":"msg_1","model":"glm-5.1"}}`,
			clientModel: "claude-sonnet-4-20250514",
			expected:    "claude-opus-4-8", // sonnet always → opus
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceResponseModel(tt.input, tt.clientModel)
			if !strings.Contains(result, tt.expected) {
				t.Fatalf("expected %q in result, got: %s", tt.expected, result)
			}
		})
	}
}

func TestStreamSSE_ModelReplacementE2E(t *testing.T) {
	sseData := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm-5-turbo\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n" +
		"\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
		"\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n" +
		"\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n" +
		"\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}\n" +
		"\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n" +
		"\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	StreamSSE(w, ioReadCloser(body), "anthropic", "claude-sonnet-4-6")

	out := w.Body.String()

	// Verify model name replaced (sonnet → opus for Claude Code compat)
	if !strings.Contains(out, `"model":"claude-opus-4-8"`) {
		t.Fatalf("expected model:claude-opus-4-8 in output, got:\\n%s", out)
	}
	// Verify glm model gone
	if strings.Contains(out, "glm-5-turbo") {
		t.Fatalf("glm-5-turbo should not appear in output:\\n%s", out)
	}
}
