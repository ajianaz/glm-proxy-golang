package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"glm-proxy/internal/config"
	"glm-proxy/internal/handler"
	"glm-proxy/internal/storage"
)

func newTestServer(t *testing.T) (*httptest.Server, *storage.KeyStore) {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := storage.ApiKeysData{Keys: []storage.ApiKey{
		{
			Key:             "pk_test_valid",
			Name:            "Test Key",
			Model:           "glm-4.7",
			GlmKey:          "user_zai_key",
			TokenLimitPer5h: 100000,
			ExpiryDate:      "2099-01-01T00:00:00Z",
			CreatedAt:       time.Now().Format(time.RFC3339),
			LastUsed:        time.Now().Format(time.RFC3339),
			UsageWindows:    []storage.UsageWindow{},
		},
		{
			Key:             "pk_test_expired",
			Name:            "Expired Key",
			TokenLimitPer5h: 1000,
			ExpiryDate:      "2020-01-01T00:00:00Z",
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

	cfg := &config.Config{
		Port:         "0",
		DataFile:     f,
		ZaiApiKey:    "master_key",
		DefaultModel: "glm-4.7",
	}

	router := handler.NewRouter(cfg, store)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return server, store
}

func TestHealthEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", body["status"])
	}
}

func TestIndexEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["name"] != "Proxy Gateway" {
		t.Fatalf("expected Proxy Gateway, got %v", body["name"])
	}
}

func TestStats_AuthRequired(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestStats_ValidKey(t *testing.T) {
	server, _ := newTestServer(t)

	req, _ := http.NewRequest("GET", server.URL+"/stats", nil)
	req.Header.Set("Authorization", "Bearer pk_test_valid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, resp.Body)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["key"] != "pk_test_valid" {
		t.Fatalf("expected pk_test_valid, got %v", body["key"])
	}
	if body["is_expired"] != false {
		t.Fatalf("expected not expired, got %v", body["is_expired"])
	}
}

func TestStats_ExpiredKey(t *testing.T) {
	server, _ := newTestServer(t)

	req, _ := http.NewRequest("GET", server.URL+"/stats", nil)
	req.Header.Set("Authorization", "Bearer pk_test_expired")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestStats_XApiKey(t *testing.T) {
	server, _ := newTestServer(t)

	req, _ := http.NewRequest("GET", server.URL+"/stats", nil)
	req.Header.Set("x-api-key", "pk_test_valid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCORS_Headers(t *testing.T) {
	server, _ := newTestServer(t)

	req, _ := http.NewRequest("OPTIONS", server.URL+"/health", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("expected CORS origin *")
	}
}
