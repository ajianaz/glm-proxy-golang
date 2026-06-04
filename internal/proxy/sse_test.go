package proxy

import (
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
