package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

func TestAnthropicProxy_NonStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify x-api-key auth
		if r.Header.Get("x-api-key") != "master-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Path should be as-is (e.g., /v1/messages)
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Verify anthropic-version
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"id":    "msg_123",
			"type":  "message",
			"usage": map[string]interface{}{
				"input_tokens":  float64(10),
				"output_tokens": float64(20),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	origUpstream := AnthropicUpstream
	AnthropicUpstream = upstream.URL
	defer func() { AnthropicUpstream = origUpstream }()

	store := makeTestKeyStore(t)

	p := &AnthropicProxy{
		Config: &config.Config{
			ZaiApiKey:    "master-key",
			DefaultModel: "glm-4.7",
		},
		Store: store,
	}

	key := &storage.ApiKey{
		Key:             "pk_test",
		Name:            "Test",
		Model:           "",
		TokenLimitPer5h: 100000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
	}

	body := strings.NewReader(`{"model":"claude-3","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`)
	req := httptest.NewRequest("POST", "/v1/messages", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	p.Proxy(rec, req, key)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["id"] != "msg_123" {
		t.Fatalf("unexpected response: %v", resp)
	}
}

func TestExtractAnthropicTokens(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":10,"output_tokens":20}}`)
	if got := extractAnthropicTokens(body); got != 30 {
		t.Fatalf("expected 30, got %d", got)
	}
}
