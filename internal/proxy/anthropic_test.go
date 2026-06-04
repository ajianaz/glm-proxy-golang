package proxy

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestReadAndInjectModelWithMap_StripsThinking(t *testing.T) {
	// Claude Code sends thinking parameters that upstream doesn't support
	body := strings.NewReader(`{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 16000,
		"thinking": {"type": "enabled", "budget_tokens": 10000},
		"budget_tokens": 10000
	}`)

	reader, bodyMap, _, _, err := readAndInjectModelWithMap(io.NopCloser(body), "/v1/messages", "POST", "glm-5-turbo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer reader.Close()

	// With nil ModelMap, model stays as-is (no mapping)
	if bodyMap["model"] != "claude-sonnet-4-6" {
		t.Fatalf("expected model to be unchanged (nil ModelMap), got %v", bodyMap["model"])
	}

	// Thinking parameters must be replaced with disabled (not stripped)
	thinking, exists := bodyMap["thinking"]
	if !exists {
		t.Fatal("expected 'thinking' to still exist in body")
	}
	thinkingMap, ok := thinking.(map[string]interface{})
	if !ok || thinkingMap["type"] != "disabled" {
		t.Fatalf("expected thinking to be disabled, got %v", thinking)
	}
	if _, exists := bodyMap["budget_tokens"]; exists {
		t.Fatal("expected 'budget_tokens' to be stripped from body")
	}

	// Verify the serialized body contains thinking as disabled
	var buf bytes.Buffer
	buf.ReadFrom(reader)
	if !bytes.Contains(buf.Bytes(), []byte("\"thinking\"")) || !bytes.Contains(buf.Bytes(), []byte("\"disabled\"")) {
		t.Fatalf("serialized body still contains \"thinking\": %s", buf.String())
	}
}

func TestExtractSystemMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		wantSys  interface{}
		wantMsgs int
	}{
		{
			name: "single system message extracted to string",
			input: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "hi"},
					map[string]interface{}{"role": "system", "content": "You are helpful."},
					map[string]interface{}{"role": "user", "content": "bye"},
				},
			},
			wantSys:  "You are helpful.",
			wantMsgs: 2,
		},
		{
			name: "multiple system messages merged to array",
			input: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "Be concise."},
					map[string]interface{}{"role": "user", "content": "hi"},
					map[string]interface{}{"role": "system", "content": "Reply in JSON."},
				},
			},
			wantSys: []interface{}{
				"Be concise.",
				"Reply in JSON.",
			},
			wantMsgs: 1,
		},
		{
			name: "no system messages — unchanged",
			input: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "hi"},
					map[string]interface{}{"role": "assistant", "content": "hello"},
				},
			},
			wantSys:  nil,
			wantMsgs: 2,
		},
		{
			name: "system content as array (Claude Code format)",
			input: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": []interface{}{
						map[string]interface{}{"type": "text", "text": "System instructions."},
						map[string]interface{}{"type": "text", "text": "More instructions."},
					}},
					map[string]interface{}{"role": "user", "content": "hi"},
				},
			},
			wantSys: []interface{}{
				map[string]interface{}{"type": "text", "text": "System instructions."},
				map[string]interface{}{"type": "text", "text": "More instructions."},
			},
			wantMsgs: 1,
		},
		{
			name: "preserves existing top-level system",
			input: map[string]interface{}{
				"system":  "Existing system prompt",
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "Injected system."},
					map[string]interface{}{"role": "user", "content": "hi"},
				},
			},
			wantSys:  "Injected system.", // overwrites existing (extracted system takes priority)
			wantMsgs: 1,
		},
		{
			name:     "no messages field — no-op",
			input:    map[string]interface{}{"model": "test"},
			wantSys:  nil,
			wantMsgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractSystemMessages(tt.input)

			sys := tt.input["system"]
			if tt.wantSys == nil {
				if sys != nil {
					t.Fatalf("expected nil system, got %v", sys)
				}
			} else {
				switch s := tt.wantSys.(type) {
				case string:
					got, ok := sys.(string)
					if !ok || got != s {
						t.Fatalf("expected system=%q, got %v", s, sys)
					}
				case []interface{}:
					got, ok := sys.([]interface{})
					if !ok {
						t.Fatalf("expected system array, got %T", sys)
					}
					if len(got) != len(s) {
						t.Fatalf("expected %d system parts, got %d", len(s), len(got))
					}
				}
			}

			msgs, ok := tt.input["messages"].([]interface{})
			if tt.wantMsgs == 0 {
				if ok {
					t.Fatalf("expected no messages, got %d", len(msgs))
				}
			} else if !ok || len(msgs) != tt.wantMsgs {
				t.Fatalf("expected %d messages, got %v", tt.wantMsgs, tt.input["messages"])
			}

			// Verify no system role remains in messages
			if msgs != nil {
				for _, m := range msgs {
					msg, ok := m.(map[string]interface{})
					if !ok {
						continue
					}
					if role, _ := msg["role"].(string); role == "system" {
						t.Fatal("system role still in messages after extraction")
					}
				}
			}
		})
	}
}
