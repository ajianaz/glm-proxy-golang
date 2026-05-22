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

	store := makeTestKeyStore(t)

	p := &AnthropicProxy{
		Config: &config.Config{
			MasterKey:         "master-key",
			DefaultModel:      "glm-4.7",
			AnthropicUpstream: upstream.URL,
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

func TestAnthropicProxy_CostHeaderParsed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-litellm-response-cost", "0.005")
		resp := map[string]interface{}{
			"id":   "msg_cost",
			"type": "message",
			"usage": map[string]interface{}{
				"input_tokens":  float64(10),
				"output_tokens": float64(20),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	store := makeTestKeyStoreWithKey(t, "pk_cost")

	p := &AnthropicProxy{
		Config: &config.Config{
			MasterKey:         "master-key",
			DefaultModel:      "glm-4.7",
			AnthropicUpstream: upstream.URL,
		},
		Store: store,
	}

	key := &storage.ApiKey{
		Key:             "pk_cost",
		Name:            "CostTest",
		TokenLimitPer5h: 100000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
	}

	body := strings.NewReader(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	req := httptest.NewRequest("POST", "/v1/messages", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	p.Proxy(rec, req, key)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify cost was tracked in the key store
	updated, ok := store.FindKey("pk_cost")
	if !ok {
		t.Fatal("key not found after proxy")
	}
	if updated.TotalSpendUSD != 0.005 {
		t.Fatalf("expected total spend 0.005, got %f", updated.TotalSpendUSD)
	}
}

func TestExtractAnthropicTokens(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":10,"output_tokens":20}}`)
	if got := extractAnthropicTokens(body); got.Total != 30 {
		t.Fatalf("expected 30, got %d", got.Total)
	}
}

func TestExtractAnthropicTokens_WithCache(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":5}}`)
	result := extractAnthropicTokens(body)
	if result.Total != 65 {
		t.Fatalf("expected 65 total (10+20+30+5), got %d", result.Total)
	}
	if result.Cached != 35 {
		t.Fatalf("expected 35 cached (30+5), got %d", result.Cached)
	}
}
