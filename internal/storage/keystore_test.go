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

func TestKeyStore_UpdateUsage_ZeroTokens_SetsDirtyAndLastUsed(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_test1",
		Name:                "Test",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 0,
		UsageWindows:        []UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	oldLastUsed := "2026-01-01T00:00:00Z"

	// Update with 0 tokens — this was the bug: previously skipped entirely
	ks.UpdateUsage("pk_test1", 0)

	key, _ := ks.FindKey("pk_test1")

	// last_used should be updated even with 0 tokens
	if key.LastUsed == oldLastUsed {
		t.Fatal("expected last_used to be updated even when tokens=0")
	}

	// lifetime tokens should stay 0
	if key.TotalLifetimeTokens != 0 {
		t.Fatalf("expected 0 lifetime tokens, got %d", key.TotalLifetimeTokens)
	}

	// dirty flag should be set so disk gets updated
	if !ks.dirty {
		t.Fatal("expected dirty flag to be true after UpdateUsage with 0 tokens")
	}
}

func TestKeyStore_FlushWritesToDisk(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_test1",
		Name:                "Test",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 0,
		UsageWindows:        []UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	// No defer Close — we call it manually

	// Update usage to make dirty
	ks.UpdateUsage("pk_test1", 500)

	// Force a save via Close (which does final flush)
	ks.Close()

	// Re-open and verify data persisted
	ks2, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks2.Close()

	key, _ := ks2.FindKey("pk_test1")
	if key.TotalLifetimeTokens != 500 {
		t.Fatalf("expected 500 lifetime tokens on disk, got %d", key.TotalLifetimeTokens)
	}
	if len(key.UsageWindows) != 1 {
		t.Fatalf("expected 1 usage window on disk, got %d", len(key.UsageWindows))
	}
	if key.UsageWindows[0].TokensUsed != 500 {
		t.Fatalf("expected 500 tokens in window on disk, got %d", key.UsageWindows[0].TokensUsed)
	}
}

func TestKeyStore_CloseFlushesDirtyData(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_flush",
		Name:                "FlushTest",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 0,
		UsageWindows:        []UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}

	// Add usage then immediately close (don't wait for 30s ticker)
	ks.UpdateUsage("pk_flush", 1234)
	ks.Close()

	// Read file directly from disk
	raw, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}

	var diskData ApiKeysData
	json.Unmarshal(raw, &diskData)

	if len(diskData.Keys) != 1 {
		t.Fatalf("expected 1 key on disk, got %d", len(diskData.Keys))
	}
	if diskData.Keys[0].TotalLifetimeTokens != 1234 {
		t.Fatalf("expected 1234 tokens on disk after Close, got %d", diskData.Keys[0].TotalLifetimeTokens)
	}
}

func TestKeyStore_UpdateUsage_MultipleRequests(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_multi",
		Name:                "MultiTest",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 0,
		UsageWindows:        []UsageWindow{},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	// Simulate multiple requests
	ks.UpdateUsage("pk_multi", 100)
	ks.UpdateUsage("pk_multi", 200)
	ks.UpdateUsage("pk_multi", 50)

	key, _ := ks.FindKey("pk_multi")
	if key.TotalLifetimeTokens != 350 {
		t.Fatalf("expected 350 lifetime tokens, got %d", key.TotalLifetimeTokens)
	}

	// All should be in the same window (within 5 hours)
	if len(key.UsageWindows) != 1 {
		t.Fatalf("expected 1 usage window (same 5h window), got %d", len(key.UsageWindows))
	}
	if key.UsageWindows[0].TokensUsed != 350 {
		t.Fatalf("expected 350 tokens in window, got %d", key.UsageWindows[0].TokensUsed)
	}
}
