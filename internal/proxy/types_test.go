package proxy

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"glm-proxy/internal/storage"
)

type nopCloser struct {
	io.Reader
}

func (nopCloser) Close() error { return nil }

func ioReadCloser(r io.Reader) io.ReadCloser {
	return nopCloser{r}
}

func TestGetModelForKey(t *testing.T) {
	key := &storage.ApiKey{}

	key.Model = "my-model"
	if got := GetModelForKey(key, "env-model"); got != "my-model" {
		t.Fatalf("expected my-model, got %s", got)
	}

	key.Model = ""
	if got := GetModelForKey(key, "env-model"); got != "env-model" {
		t.Fatalf("expected env-model, got %s", got)
	}

	if got := GetModelForKey(key, ""); got != "glm-4.7" {
		t.Fatalf("expected glm-4.7, got %s", got)
	}
}

func TestUpstreamKey(t *testing.T) {
	key := &storage.ApiKey{}
	if got := key.UpstreamKey("master"); got != "master" {
		t.Fatalf("expected master, got %s", got)
	}

	key.GlmKey = "custom"
	if got := key.UpstreamKey("master"); got != "custom" {
		t.Fatalf("expected custom, got %s", got)
	}
}

func TestReadAndInjectModel(t *testing.T) {
	body := strings.NewReader(`{"model": "gpt-4", "messages": []}`)
	rc := ioReadCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/chat/completions", "POST", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)
	if result["model"] != "glm-4.7" {
		t.Fatalf("expected model to be injected, got %v", result["model"])
	}
}

func TestReadAndInjectModel_NoInjection(t *testing.T) {
	// GET requests should not inject
	body := strings.NewReader(`{"model": "gpt-4"}`)
	rc := ioReadCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/chat/completions", "GET", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	// Body should be unchanged (not consumed)
	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)
	if result["model"] != "gpt-4" {
		t.Fatalf("expected model unchanged for GET, got %v", result["model"])
	}
}
