package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

func makeTestKeyStore(t *testing.T) *storage.KeyStore {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := storage.ApiKeysData{Keys: []storage.ApiKey{{
		Key:              "pk_test",
		Name:             "Test",
		TokenLimitPer5h:  100000,
		ExpiryDate:       "2099-01-01T00:00:00Z",
		CreatedAt:        time.Now().Format(time.RFC3339),
		LastUsed:         time.Now().Format(time.RFC3339),
		UsageWindows:     []storage.UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := storage.NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ks.Close() })
	return ks
}

func TestOpenAIProxy_NonStreaming(t *testing.T) {
	// Create mock upstream
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth
		if r.Header.Get("Authorization") != "Bearer master-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Verify path (should have /v1 stripped)
		if r.URL.Path != "/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := map[string]interface{}{
			"id":      "chatcmpl-123",
			"choices": []interface{}{},
			"usage": map[string]interface{}{
				"total_tokens": float64(100),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	// Override upstream URL for test
	origUpstream := OpenAIUpstream
	OpenAIUpstream = upstream.URL
	defer func() { OpenAIUpstream = origUpstream }()

	store := makeTestKeyStore(t)
	p := &OpenAIProxy{
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

	body := strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	p.Proxy(rec, req, key)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["id"] != "chatcmpl-123" {
		t.Fatalf("unexpected response: %v", resp)
	}
}

func TestExtractOpenAITokens(t *testing.T) {
	body := []byte(`{"usage":{"total_tokens":42}}`)
	if got := extractOpenAITokens(body); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}

	body2 := []byte(`{"no":"usage"}`)
	if got := extractOpenAITokens(body2); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}
