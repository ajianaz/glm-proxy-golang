package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"glm-proxy/internal/storage"
)

func makeTestStore(t *testing.T) *storage.KeyStore {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := storage.ApiKeysData{Keys: []storage.ApiKey{{
		Key:             "pk_test1",
		Name:            "Test",
		TokenLimitPer5h: 1000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		CreatedAt:       time.Now().Format(time.RFC3339),
		LastUsed:        time.Now().Format(time.RFC3339),
		UsageWindows:    []storage.UsageWindow{},
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

func TestAuth_ValidBearerToken(t *testing.T) {
	store := makeTestStore(t)
	handler := Auth(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := GetApiKey(r)
		if key == nil || key.Key != "pk_test1" {
			t.Fatal("expected api key in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer pk_test1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuth_ValidXApiKey(t *testing.T) {
	store := makeTestStore(t)
	handler := Auth(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := GetApiKey(r)
		if key == nil || key.Key != "pk_test1" {
			t.Fatal("expected api key in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("x-api-key", "pk_test1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuth_MissingKey(t *testing.T) {
	store := makeTestStore(t)
	handler := Auth(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuth_InvalidKey(t *testing.T) {
	store := makeTestStore(t)
	handler := Auth(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer pk_invalid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
