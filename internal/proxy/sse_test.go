package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamSSE_ExtractsTokensFromLastChunk(t *testing.T) {
	// Simulate an OpenAI SSE stream where usage comes in the final chunk
	sseData := "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"total_tokens\":42}}\n" +
		"data: [DONE]\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	total := StreamSSE(w, ioReadCloser(body), "openai")

	if total != 42 {
		t.Fatalf("expected 42 tokens from SSE stream, got %d", total)
	}

	result := w.Body.String()
	if !strings.Contains(result, "data: [DONE]") {
		t.Fatal("expected [DONE] to be forwarded")
	}
}

func TestStreamSSE_AnthropicUsageInLastChunk(t *testing.T) {
	sseData := "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":25}}\n" +
		"data: [DONE]\n"

	body := strings.NewReader(sseData)
	w := httptest.NewRecorder()
	total := StreamSSE(w, ioReadCloser(body), "anthropic")

	if total != 35 {
		t.Fatalf("expected 35 tokens (10+25) from Anthropic SSE, got %d", total)
	}
}

func TestParseSSETokens_OpenAI(t *testing.T) {
	data := `{"id":"chatcmpl-123","choices":[],"usage":{"total_tokens":42}}`
	tokens := parseSSETokens(data, "openai")
	if tokens != 42 {
		t.Fatalf("expected 42, got %d", tokens)
	}
}

func TestParseSSETokens_Anthropic(t *testing.T) {
	data := `{"type":"message_delta","usage":{"input_tokens":10,"output_tokens":20}}`
	tokens := parseSSETokens(data, "anthropic")
	if tokens != 30 {
		t.Fatalf("expected 30, got %d", tokens)
	}
}

func TestParseSSETokens_InvalidJSON(t *testing.T) {
	tokens := parseSSETokens("not json", "openai")
	if tokens != 0 {
		t.Fatalf("expected 0 for invalid json, got %d", tokens)
	}
}

func TestStreamSSE_BasicForwarding(t *testing.T) {
	// Test that SSE data is forwarded correctly through the flusher path
	body := strings.NewReader("data: hello\ndata: world\ndata: [DONE]\n")
	w := httptest.NewRecorder()
	total := StreamSSE(w, ioReadCloser(body), "openai")

	if w.Code != 0 {
		// ResponseRecorder may not set status if WriteHeader wasn't called
	}
	// Verify data was forwarded
	result := w.Body.String()
	if !strings.Contains(result, "data: hello") || !strings.Contains(result, "data: world") {
		t.Fatalf("expected SSE data to be forwarded, got: %s", result)
	}
	// No tokens in test data, so should be 0
	if total != 0 {
		t.Fatalf("expected 0 tokens, got %d", total)
	}
}
