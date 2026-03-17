package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glm-proxy/internal/config"
	"glm-proxy/internal/handler"
	"glm-proxy/internal/proxy"
	"glm-proxy/internal/storage"
)

func createKeyStore(t *testing.T, extraKeys ...storage.ApiKey) *storage.KeyStore {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	keys := append([]storage.ApiKey{{
		Key:             "pk_test",
		Name:            "Test",
		TokenLimitPer5h: 100000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		CreatedAt:       time.Now().Format(time.RFC3339),
		LastUsed:        time.Now().Format(time.RFC3339),
		UsageWindows:    []storage.UsageWindow{},
	}}, extraKeys...)

	data := storage.ApiKeysData{Keys: keys}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	store, err := storage.NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestOpenAIProxy_NonStreamingIntegration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer master_key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"content": "hello"}}},
			"usage":   map[string]interface{}{"total_tokens": float64(50)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	origURL := proxy.OpenAIUpstream
	proxy.OpenAIUpstream = upstream.URL
	defer func() { proxy.OpenAIUpstream = origURL }()

	store := createKeyStore(t)
	cfg := &config.Config{
		Port:         "0",
		DataFile:     "",
		ZaiApiKey:    "master_key",
		DefaultModel: "glm-4.7",
	}

	server := httptest.NewServer(handler.NewRouter(cfg, store))
	defer server.Close()

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer pk_test")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["id"] != "chatcmpl-test" {
		t.Fatalf("unexpected response id: %v", result["id"])
	}
}

func TestAnthropicProxy_NonStreamingIntegration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "master_key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := map[string]interface{}{
			"id":      "msg_test",
			"type":    "message",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}},
			"usage":   map[string]interface{}{"input_tokens": float64(10), "output_tokens": float64(5)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	origURL := proxy.AnthropicUpstream
	proxy.AnthropicUpstream = upstream.URL
	defer func() { proxy.AnthropicUpstream = origURL }()

	store := createKeyStore(t)
	cfg := &config.Config{
		Port:         "0",
		DataFile:     "",
		ZaiApiKey:    "master_key",
		DefaultModel: "glm-4.7",
	}

	server := httptest.NewServer(handler.NewRouter(cfg, store))
	defer server.Close()

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`
	req, _ := http.NewRequest("POST", server.URL+"/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer pk_test")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["id"] != "msg_test" {
		t.Fatalf("unexpected response id: %v", result["id"])
	}
}

func TestModelInjection(t *testing.T) {
	var receivedModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel, _ = body["model"].(string)
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"choices": []interface{}{},
			"usage":   map[string]interface{}{"total_tokens": float64(1)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	origURL := proxy.OpenAIUpstream
	proxy.OpenAIUpstream = upstream.URL
	defer func() { proxy.OpenAIUpstream = origURL }()

	store := createKeyStore(t)
	cfg := &config.Config{
		Port:         "0",
		DataFile:     "",
		ZaiApiKey:    "master_key",
		DefaultModel: "glm-4.7",
	}

	server := httptest.NewServer(handler.NewRouter(cfg, store))
	defer server.Close()

	// Client requests gpt-4, but server should inject glm-4.7
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer pk_test")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if receivedModel != "glm-4.7" {
		t.Fatalf("expected model to be injected as glm-4.7, got %s", receivedModel)
	}
}

func TestGlmKey_PerKey(t *testing.T) {
	var receivedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"choices": []interface{}{},
			"usage":   map[string]interface{}{"total_tokens": float64(1)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	origURL := proxy.OpenAIUpstream
	proxy.OpenAIUpstream = upstream.URL
	defer func() { proxy.OpenAIUpstream = origURL }()

	store := createKeyStore(t, storage.ApiKey{
		Key:             "pk_glmkey_user",
		Name:            "GlmKey User",
		GlmKey:          "user_custom_zai_key",
		TokenLimitPer5h: 100000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		CreatedAt:       time.Now().Format(time.RFC3339),
		LastUsed:        time.Now().Format(time.RFC3339),
		UsageWindows:    []storage.UsageWindow{},
	})
	cfg := &config.Config{
		Port:         "0",
		DataFile:     "",
		ZaiApiKey:    "master_key",
		DefaultModel: "glm-4.7",
	}

	server := httptest.NewServer(handler.NewRouter(cfg, store))
	defer server.Close()

	body := `{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer pk_glmkey_user")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Should use per-key GlmKey, not master key
	if receivedAuth != "Bearer user_custom_zai_key" {
		t.Fatalf("expected Bearer user_custom_zai_key, got %s", receivedAuth)
	}
}
