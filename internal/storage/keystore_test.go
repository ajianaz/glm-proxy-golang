package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyStore_FindKey(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	// Create initial data
	data := ApiKeysData{Keys: []ApiKey{{
		Key:             "pk_test1",
		Name:            "Test",
		TokenLimitPer5h: 1000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		CreatedAt:       "2026-01-01T00:00:00Z",
		LastUsed:        "2026-01-01T00:00:00Z",
		UsageWindows:    []UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	key, ok := ks.FindKey("pk_test1")
	if !ok || key.Name != "Test" {
		t.Fatal("expected to find key")
	}

	_, ok = ks.FindKey("nonexistent")
	if ok {
		t.Fatal("should not find nonexistent key")
	}
}

func TestKeyStore_UpdateUsage(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:             "pk_test1",
		Name:            "Test",
		TokenLimitPer5h: 100000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		CreatedAt:       "2026-01-01T00:00:00Z",
		LastUsed:        "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 0,
		UsageWindows:    []UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	ks.UpdateUsage("pk_test1", 500)

	key, _ := ks.FindKey("pk_test1")
	if key.TotalLifetimeTokens != 500 {
		t.Fatalf("expected 500, got %d", key.TotalLifetimeTokens)
	}
	if len(key.UsageWindows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(key.UsageWindows))
	}
	if key.UsageWindows[0].TokensUsed != 500 {
		t.Fatalf("expected 500 tokens in window, got %d", key.UsageWindows[0].TokensUsed)
	}
}

func TestKeyStore_UpstreamKey(t *testing.T) {
	k := &ApiKey{Key: "pk_test"}
	if k.UpstreamKey("master") != "master" {
		t.Fatal("expected master key when glmkey is empty")
	}

	k2 := &ApiKey{Key: "pk_test", GlmKey: "custom_key"}
	if k2.UpstreamKey("master") != "custom_key" {
		t.Fatal("expected custom glmkey when set")
	}
}
