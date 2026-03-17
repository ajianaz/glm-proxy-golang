package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
