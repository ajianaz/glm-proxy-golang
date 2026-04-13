package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"glm-proxy/internal/config"
	"glm-proxy/internal/storage"
)

func TestModelValidate_EmptyAllowed_AllowsAll(t *testing.T) {
	cfg := &config.Config{AllowedModels: nil}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"any-model"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestModelValidate_AllowedModel_Passes(t *testing.T) {
	cfg := &config.Config{AllowedModels: []string{"glm-5", "glm-5-turbo"}}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"glm-5"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestModelValidate_DisallowedModel_Blocked(t *testing.T) {
	cfg := &config.Config{AllowedModels: []string{"glm-5", "glm-5-turbo"}}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["error"] != "model not allowed" {
		t.Fatalf("expected error 'model not allowed', got %v", resp["error"])
	}
}

func TestModelValidate_BodyRestored(t *testing.T) {
	cfg := &config.Config{AllowedModels: []string{"glm-5"}}
	var bodyRead bool
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]interface{}
		json.NewDecoder(r.Body).Decode(&m)
		bodyRead = true
		if m["model"] != "glm-5" {
			t.Fatalf("expected model glm-5 in downstream handler, got %v", m["model"])
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"glm-5","messages":[]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !bodyRead {
		t.Fatal("downstream handler should be able to read the body")
	}
}

func TestModelValidate_NoModelField_ResolvedFromApiKey(t *testing.T) {
	cfg := &config.Config{
		AllowedModels: []string{"glm-5", "glm-4.7"},
		DefaultModel:  "glm-4.7",
	}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Simulate request with API key in context but no model in body
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	apiKey := &storage.ApiKey{Key: "test", Model: "glm-4.7"}
	req = SetApiKey(req, apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (glm-4.7 is allowed), got %d", rec.Code)
	}
}

func TestModelValidate_NoModelField_DisallowedDefault(t *testing.T) {
	cfg := &config.Config{
		AllowedModels: []string{"glm-5"},
		DefaultModel:  "glm-4.7",
	}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	apiKey := &storage.ApiKey{Key: "test", Model: ""}
	req = SetApiKey(req, apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (glm-4.7 not allowed), got %d", rec.Code)
	}
}

func TestModelValidate_NonJSONBody_Passes(t *testing.T) {
	cfg := &config.Config{AllowedModels: []string{"glm-5"}}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`not-json`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-JSON body, got %d", rec.Code)
	}
}

func TestModelValidate_CommaSeparatedWithSpaces(t *testing.T) {
	cfg := &config.Config{AllowedModels: []string{"glm-5", "glm-5-turbo", "glm-5.1"}}
	handler := ModelValidate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"glm-5.1"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
