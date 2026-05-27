package litellm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEnsureTeam(t *testing.T) {
	// Test: successful team creation
	t.Run("creates team successfully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/team/new" {
				t.Errorf("expected POST /team/new, got %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer test-master-key" {
				t.Errorf("expected Bearer auth, got %s", r.Header.Get("Authorization"))
			}

			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["team_alias"] != "glm-proxy" {
				t.Errorf("expected team_alias glm-proxy, got %s", body["team_alias"])
			}

			json.NewEncoder(w).Encode(TeamResponse{
				TeamID:    "test-team-123",
				TeamAlias: "glm-proxy",
			})
		}))
		defer srv.Close()

		client := NewClient(srv.URL, "test-master-key", "")
		teamID, err := client.EnsureTeam("glm-proxy")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if teamID != "test-team-123" {
			t.Errorf("expected team_id test-team-123, got %s", teamID)
		}
	})

	// Test: team already exists (idempotent)
	t.Run("team already exists is idempotent", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// LiteLLM returns 200 with existing team when team_alias exists
			json.NewEncoder(w).Encode(TeamResponse{
				TeamID:    "existing-team-id",
				TeamAlias: "glm-proxy",
			})
		}))
		defer srv.Close()

		client := NewClient(srv.URL, "test-master-key", "")
		teamID, err := client.EnsureTeam("glm-proxy")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if teamID != "existing-team-id" {
			t.Errorf("expected existing-team-id, got %s", teamID)
		}
	})

	// Test: server error
	t.Run("handles server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
		}))
		defer srv.Close()

		client := NewClient(srv.URL, "test-master-key", "")
		_, err := client.EnsureTeam("glm-proxy")
		if err == nil {
			t.Fatal("expected error for 500 response")
		}
	})
}

func TestGenerateKey(t *testing.T) {
	t.Run("generates key successfully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/key/generate" {
				t.Errorf("expected POST /key/generate, got %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer test-master-key" {
				t.Errorf("expected Bearer auth")
			}

			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["team_id"] != "team-123" {
				t.Errorf("expected team_id team-123, got %s", body["team_id"])
			}
			if body["key_alias"] != "test-key" {
				t.Errorf("expected key_alias test-key, got %s", body["key_alias"])
			}

			json.NewEncoder(w).Encode(KeyResponse{
				Key:      "sk-litellm-generated-key-abc123",
				KeyAlias: "test-key",
			})
		}))
		defer srv.Close()

		client := NewClient(srv.URL, "test-master-key", "")
		key, err := client.GenerateKey("team-123", "test-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key != "sk-litellm-generated-key-abc123" {
			t.Errorf("expected sk-litellm-generated-key-abc123, got %s", key)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": "key generation failed"})
		}))
		defer srv.Close()

		client := NewClient(srv.URL, "test-master-key", "")
		_, err := client.GenerateKey("team-123", "test-key")
		if err == nil {
			t.Fatal("expected error for 500 response")
		}
	})
}

func TestNewClient(t *testing.T) {
	client := NewClient("http://litellm:4000", "sk-master", "prod")
	if client.BaseURL != "http://litellm:4000" {
		t.Errorf("expected base URL http://litellm:4000, got %s", client.BaseURL)
	}
	if client.MasterKey != "sk-master" {
		t.Errorf("expected master key sk-master, got %s", client.MasterKey)
	}
	if client.EnvMode != "prod" {
		t.Errorf("expected env mode prod, got %s", client.EnvMode)
	}
	if client.HTTP == nil {
		t.Error("expected HTTP client to be initialized")
	}
	if client.HTTP.Timeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %s", client.HTTP.Timeout)
	}
}

func TestTeamName(t *testing.T) {
	tests := []struct {
		envMode string
		want    string
	}{
		{"prod", "glm-proxy"},
		{"dev", "glm-proxy-dev"},
		{"staging", "glm-proxy-staging"},
		{"", "glm-proxy"}, // empty treated as prod
	}
	for _, tt := range tests {
		c := NewClient("http://litellm:4000", "sk-master", tt.envMode)
		if got := c.TeamName(); got != tt.want {
			t.Errorf("EnvMode=%q TeamName()=%q, want %q", tt.envMode, got, tt.want)
		}
	}
}

func TestKeyAlias(t *testing.T) {
	tests := []struct {
		envMode string
		name    string
		want    string
	}{
		{"prod", "asa", "asa"},
		{"dev", "asa", "asa_dev"},
		{"staging", "asa", "asa_staging"},
		{"", "asa", "asa"},        // empty treated as prod
		{"dev", "", "_dev"},      // empty name still gets suffix
		{"prod", "test-key", "test-key"},
	}
	for _, tt := range tests {
		c := NewClient("http://litellm:4000", "sk-master", tt.envMode)
		if got := c.KeyAlias(tt.name); got != tt.want {
			t.Errorf("EnvMode=%q KeyAlias(%q)=%q, want %q", tt.envMode, tt.name, got, tt.want)
		}
	}
}
