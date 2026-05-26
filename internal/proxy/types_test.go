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
	if got := key.GetUpstreamKey("master"); got != "master" {
		t.Fatalf("expected master, got %s", got)
	}

	// Non-sk- prefix should fall back to master key
	key.UpstreamKey = "custom"
	if got := key.GetUpstreamKey("master"); got != "master" {
		t.Fatalf("expected master (non-sk- key should fallback), got %s", got)
	}

	// Valid sk- prefix should be used
	key.UpstreamKey = "sk-custom-key"
	if got := key.GetUpstreamKey("master"); got != "sk-custom-key" {
		t.Fatalf("expected sk-custom-key, got %s", got)
	}

	// Empty upstream_key should fall back to master
	key.UpstreamKey = ""
	if got := key.GetUpstreamKey("master"); got != "master" {
		t.Fatalf("expected master, got %s", got)
	}
}

func TestReadAndInjectModel_ClientModelPreserved(t *testing.T) {
	// When client sends a model, it should be preserved (not overwritten)
	body := strings.NewReader(`{"model": "gpt-4", "messages": []}`)
	rc := ioReadCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/chat/completions", "POST", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)
	if result["model"] != "gpt-4" {
		t.Fatalf("expected client model gpt-4 to be preserved, got %v", result["model"])
	}
}

func TestReadAndInjectModel_FallbackWhenNoModel(t *testing.T) {
	// When client doesn't send a model, fallback should be used
	body := strings.NewReader(`{"messages": []}`)
	rc := ioReadCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/chat/completions", "POST", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)
	if result["model"] != "glm-4.7" {
		t.Fatalf("expected fallback model glm-4.7, got %v", result["model"])
	}
}

func TestReadAndInjectModel_StreamOptionsInjected(t *testing.T) {
	// When stream:true, stream_options should be injected for OpenAI paths
	// Client model should be preserved (not overwritten)
	body := strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	rc := io.NopCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/chat/completions", "POST", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)

	// Client model should be preserved
	if result["model"] != "gpt-4" {
		t.Fatalf("expected client model gpt-4 to be preserved, got %v", result["model"])
	}

	so, ok := result["stream_options"].(map[string]interface{})
	if !ok {
		t.Fatal("expected stream_options to be injected for streaming OpenAI requests")
	}
	if so["include_usage"] != true {
		t.Fatalf("expected include_usage=true, got %v", so["include_usage"])
	}
}

func TestReadAndInjectModel_StreamOptionsNotInjectedForAnthropic(t *testing.T) {
	// Anthropic /messages path should NOT get stream_options
	body := strings.NewReader(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	rc := ioReadCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/messages", "POST", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)

	if result["stream_options"] != nil {
		t.Fatal("stream_options should NOT be injected for Anthropic /messages path")
	}
}

func TestReadAndInjectModel_StreamOptionsNotInjectedForNonStreaming(t *testing.T) {
	// Non-streaming requests should NOT get stream_options
	body := strings.NewReader(`{"model":"gpt-4","messages":[],"stream":false}`)
	rc := ioReadCloser(body)

	injected, err := readAndInjectModel(rc, "/v1/chat/completions", "POST", "glm-4.7")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	json.NewDecoder(injected).Decode(&result)

	if result["stream_options"] != nil {
		t.Fatal("stream_options should NOT be injected when stream is false")
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
