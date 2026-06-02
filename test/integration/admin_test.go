package integration

import (
	"bytes"
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
	"glm-proxy/internal/storage"
)

func newAdminTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := storage.ApiKeysData{Keys: []storage.ApiKey{
		{
			Key:             "sk-test-existing",
			Name:            "existing-key",
			Model:           "glm-5-turbo",
			UpstreamKey:     "sk-upstream-existing",
			TokenLimitPer5h: 15000000,
			ExpiryDate:      "2099-01-01T00:00:00Z",
			CreatedAt:       time.Now().Format(time.RFC3339),
			LastUsed:        time.Now().Format(time.RFC3339),
			UsageWindows:    []storage.UsageWindow{},
		},
	}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	store, err := storage.NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	adminKey := "test-admin-key"
	cfg := &config.Config{
		Port:         "0",
		DataFile:     f,
		MasterKey:    "master_key",
		DefaultModel: "glm-4.7",
		AdminAPIKey:  adminKey,
	}

	router := handler.NewRouter(cfg, store)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server, adminKey
}

func adminJSON(t *testing.T, method, url string, body interface{}, adminKey string) (*http.Response, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, bodyReader)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return resp, result
}

func adminJSONArray(t *testing.T, method, url string, body interface{}, adminKey string) (*http.Response, []interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, bodyReader)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var result []interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return resp, result
}

// TestCreateKey_ReturnsFullKey — POST /admin/keys must return unmasked key
func TestCreateKey_ReturnsFullKey(t *testing.T) {
	server, adminKey := newAdminTestServer(t)

	payload := map[string]interface{}{
		"name":               "new-test-key",
		"model":              "glm-5-turbo",
		"token_limit_per_5h": 100000,
		"expiry_date":        "2099-12-31T23:59:59Z",
	}

	resp, result := adminJSON(t, "POST", server.URL+"/admin/keys", payload, adminKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", resp.StatusCode, result)
	}

	key, ok := result["key"].(string)
	if !ok || key == "" {
		t.Fatalf("expected non-empty key, got %v", result["key"])
	}

	// Full key should NOT contain "..." (mask indicator)
	if strings.Contains(key, "...") {
		t.Fatalf("expected full key, got masked key: %s", key)
	}

	// Key should start with "sk-" prefix
	if !strings.HasPrefix(key, "sk-") {
		t.Fatalf("expected sk- prefix, got: %s", key)
	}

	// Key should be long enough (generateAPIKey produces 24 bytes = 48 hex chars + "sk-" = 51 chars)
	if len(key) != 51 {
		t.Fatalf("expected 51 char key, got %d chars: %s", len(key), key)
	}

	t.Logf("Full key returned: %s", key)
}

// TestListKeys_ReturnsMaskedKeys — GET /admin/keys must mask keys
func TestListKeys_ReturnsMaskedKeys(t *testing.T) {
	server, adminKey := newAdminTestServer(t)

	resp, keys := adminJSONArray(t, "GET", server.URL+"/admin/keys", nil, adminKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if len(keys) == 0 {
		t.Fatalf("expected key list, got empty")
	}

	// All keys should be masked
	for _, k := range keys {
		keyMap := k.(map[string]interface{})
		key := keyMap["key"].(string)
		if !strings.Contains(key, "...") {
			t.Fatalf("expected masked key, got full key: %s", key)
		}
	}
}

// TestGetKey_ReturnsMaskedKey — GET /admin/keys/:id must mask key
func TestGetKey_ReturnsMaskedKey(t *testing.T) {
	server, adminKey := newAdminTestServer(t)

	resp, result := adminJSON(t, "GET", server.URL+"/admin/keys/1", nil, adminKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, result)
	}

	key, _ := result["key"].(string)
	if !strings.Contains(key, "...") {
		t.Fatalf("expected masked key, got full key: %s", key)
	}
}

// TestRegenerateKey_ReturnsFullKey — POST /admin/keys/:id/regenerate must return full key
func TestRegenerateKey_ReturnsFullKey(t *testing.T) {
	server, adminKey := newAdminTestServer(t)

	resp, result := adminJSON(t, "POST", server.URL+"/admin/keys/1/regenerate", nil, adminKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, result)
	}

	key, ok := result["key"].(string)
	if !ok || key == "" {
		t.Fatalf("expected non-empty key, got %v", result["key"])
	}

	// Full key should NOT contain "..."
	if strings.Contains(key, "...") {
		t.Fatalf("expected full key after regenerate, got masked: %s", key)
	}

	// Key should start with "sk-"
	if !strings.HasPrefix(key, "sk-") {
		t.Fatalf("expected sk- prefix, got: %s", key)
	}

	if len(key) != 51 {
		t.Fatalf("expected 51 char key, got %d chars: %s", len(key), key)
	}

	// Should also have other metadata fields
	if _, ok := result["name"]; !ok {
		t.Fatalf("expected name field in response, got %v", result)
	}
	if _, ok := result["model"]; !ok {
		t.Fatalf("expected model field in response, got %v", result)
	}

	t.Logf("Regenerated full key: %s", key)
}

// TestUpdateKey_ReturnsMaskedKey — PUT /admin/keys/:id must mask key
func TestUpdateKey_ReturnsMaskedKey(t *testing.T) {
	server, adminKey := newAdminTestServer(t)

	payload := map[string]interface{}{
		"model": "glm-5.1",
	}

	resp, result := adminJSON(t, "PUT", server.URL+"/admin/keys/1", payload, adminKey)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, result)
	}

	key, _ := result["key"].(string)
	if !strings.Contains(key, "...") {
		t.Fatalf("expected masked key on update, got full key: %s", key)
	}

	// Verify model was updated
	model, _ := result["model"].(string)
	if model != "glm-5.1" {
		t.Fatalf("expected model glm-5.1, got %s", model)
	}
}

// TestAdminAuth_Required — admin endpoints require admin API key
func TestAdminAuth_Required(t *testing.T) {
	server, _ := newAdminTestServer(t)

	// No auth header
	resp, _ := adminJSON(t, "GET", server.URL+"/admin/keys", nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	// Wrong auth key
	resp, _ = adminJSON(t, "GET", server.URL+"/admin/keys", nil, "wrong-key")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
