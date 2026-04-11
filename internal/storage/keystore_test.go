package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

	ks.UpdateUsage("pk_test1", 500, 0)

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
	ks.UpdateUsage("pk_test1", 0, 0)

	key, _ := ks.FindKey("pk_test1")

	// last_used should be updated even with 0 tokens
	if key.LastUsed == oldLastUsed {
		t.Fatal("expected last_used to be updated even when tokens=0")
	}

	// lifetime tokens should stay 0
	if key.TotalLifetimeTokens != 0 {
		t.Fatalf("expected 0 lifetime tokens, got %d", key.TotalLifetimeTokens)
	}

	// Since UpdateUsage saves immediately, dirty should be false after successful save.
	// The important thing is that last_used was updated and data persisted.
	if ks.dirty {
		t.Fatal("expected dirty flag to be false after UpdateUsage saved successfully")
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
	ks.UpdateUsage("pk_test1", 500, 0)

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
	ks.UpdateUsage("pk_flush", 1234, 0)
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
	ks.UpdateUsage("pk_multi", 100, 0)
	ks.UpdateUsage("pk_multi", 200, 0)
	ks.UpdateUsage("pk_multi", 50, 0)

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

// --- New tests for bug fixes ---

func TestKeyStore_UpdateUsage_WindowStartIsRFC3339(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_fmt",
		Name:                "FormatTest",
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

	ks.UpdateUsage("pk_fmt", 100, 0)

	key, _ := ks.FindKey("pk_fmt")

	// WindowStart must be parseable with RFC3339 (not just RFC3339Nano)
	_, err = time.Parse(time.RFC3339, key.UsageWindows[0].WindowStart)
	if err != nil {
		t.Fatalf("WindowStart %q is not valid RFC3339: %v", key.UsageWindows[0].WindowStart, err)
	}

	// LastUsed must also be RFC3339 parseable
	_, err = time.Parse(time.RFC3339, key.LastUsed)
	if err != nil {
		t.Fatalf("LastUsed %q is not valid RFC3339: %v", key.LastUsed, err)
	}
}

func TestKeyStore_UpdateUsage_ConsolidatesMultipleWindows(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	now := time.Now().UTC()
	oneHourAgo := now.Add(-1 * time.Hour).Format(time.RFC3339)
	twoHoursAgo := now.Add(-2 * time.Hour).Format(time.RFC3339)

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_consolidate",
		Name:                "ConsolidateTest",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 300,
		// Multiple active windows within 5 hours
		UsageWindows: []UsageWindow{
			{WindowStart: twoHoursAgo, TokensUsed: 100},
			{WindowStart: oneHourAgo, TokensUsed: 200},
		},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	// Update with 50 new tokens
	ks.UpdateUsage("pk_consolidate", 50, 0)

	key, _ := ks.FindKey("pk_consolidate")

	// All windows should be consolidated into 1
	if len(key.UsageWindows) != 1 {
		t.Fatalf("expected 1 consolidated window, got %d", len(key.UsageWindows))
	}

	// Tokens should be sum of all active windows + new tokens (100 + 200 + 50 = 350)
	if key.UsageWindows[0].TokensUsed != 350 {
		t.Fatalf("expected 350 tokens in consolidated window, got %d", key.UsageWindows[0].TokensUsed)
	}

	// Lifetime should be 300 (old) + 50 (new) = 350
	if key.TotalLifetimeTokens != 350 {
		t.Fatalf("expected 350 lifetime tokens, got %d", key.TotalLifetimeTokens)
	}

	// Window start should be the earliest of the active windows (twoHoursAgo)
	parsedStart, _ := time.Parse(time.RFC3339, key.UsageWindows[0].WindowStart)
	parsedTwoHoursAgo, _ := time.Parse(time.RFC3339, twoHoursAgo)
	diff := parsedStart.Sub(parsedTwoHoursAgo)
	if diff < -time.Second || diff > time.Second {
		t.Fatalf("expected window start ≈ %s, got %s", twoHoursAgo, key.UsageWindows[0].WindowStart)
	}
}

func TestKeyStore_UpdateUsage_CleansUpOldWindows(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	now := time.Now().UTC()
	sixHoursAgo := now.Add(-6 * time.Hour).Format(time.RFC3339)
	oneHourAgo := now.Add(-1 * time.Hour).Format(time.RFC3339)

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_cleanup",
		Name:                "CleanupTest",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 300,
		UsageWindows: []UsageWindow{
			{WindowStart: sixHoursAgo, TokensUsed: 100},  // expired, should be removed
			{WindowStart: oneHourAgo, TokensUsed: 200},    // active, should be kept
		},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	ks.UpdateUsage("pk_cleanup", 50, 0)

	key, _ := ks.FindKey("pk_cleanup")

	// Only 1 window should remain (old one cleaned up, active consolidated)
	if len(key.UsageWindows) != 1 {
		t.Fatalf("expected 1 window after cleanup, got %d", len(key.UsageWindows))
	}

	// Only active tokens should remain (200 + 50 = 250)
	if key.UsageWindows[0].TokensUsed != 250 {
		t.Fatalf("expected 250 tokens (active only), got %d", key.UsageWindows[0].TokensUsed)
	}

	// Lifetime includes old tokens (300 + 50 = 350)
	if key.TotalLifetimeTokens != 350 {
		t.Fatalf("expected 350 lifetime tokens, got %d", key.TotalLifetimeTokens)
	}
}

func TestKeyStore_UpdateUsage_MalformedWindowStartSkipped(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	now := time.Now().UTC()
	oneHourAgo := now.Add(-1 * time.Hour).Format(time.RFC3339)

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_malformed",
		Name:                "MalformedTest",
		TokenLimitPer5h:     100000,
		ExpiryDate:          "2099-01-01T00:00:00Z",
		CreatedAt:           "2026-01-01T00:00:00Z",
		LastUsed:            "2026-01-01T00:00:00Z",
		TotalLifetimeTokens: 200,
		UsageWindows: []UsageWindow{
			{WindowStart: "not-a-date", TokensUsed: 999},  // malformed, should be skipped
			{WindowStart: oneHourAgo, TokensUsed: 200},     // valid, should be counted
		},
	}}}
	b, _ := json.Marshal(data)
	os.WriteFile(f, b, 0644)

	ks, err := NewKeyStore(f)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()

	ks.UpdateUsage("pk_malformed", 50, 0)

	key, _ := ks.FindKey("pk_malformed")

	// Malformed window dropped, only valid window + new tokens (200 + 50 = 250)
	if len(key.UsageWindows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(key.UsageWindows))
	}
	if key.UsageWindows[0].TokensUsed != 250 {
		t.Fatalf("expected 250 tokens (valid window only), got %d", key.UsageWindows[0].TokensUsed)
	}
}

func TestKeyStore_UpdateUsage_DiskPersistenceRFC3339(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_persist",
		Name:                "PersistTest",
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

	ks.UpdateUsage("pk_persist", 777, 0)
	ks.Close() // force flush to disk

	// Read raw file and verify format
	raw, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}

	var diskData ApiKeysData
	if err := json.Unmarshal(raw, &diskData); err != nil {
		t.Fatal(err)
	}

	key := diskData.Keys[0]

	// Verify WindowStart on disk is RFC3339 parseable
	_, err = time.Parse(time.RFC3339, key.UsageWindows[0].WindowStart)
	if err != nil {
		t.Fatalf("disk WindowStart %q not RFC3339: %v", key.UsageWindows[0].WindowStart, err)
	}

	// Verify LastUsed on disk is RFC3339 parseable
	_, err = time.Parse(time.RFC3339, key.LastUsed)
	if err != nil {
		t.Fatalf("disk LastUsed %q not RFC3339: %v", key.LastUsed, err)
	}

	if key.TotalLifetimeTokens != 777 {
		t.Fatalf("expected 777 on disk, got %d", key.TotalLifetimeTokens)
	}
	if key.UsageWindows[0].TokensUsed != 777 {
		t.Fatalf("expected 777 in window on disk, got %d", key.UsageWindows[0].TokensUsed)
	}
}

func TestKeyStore_UpdateUsage_UnknownKeyIgnored(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "keys.json")

	data := ApiKeysData{Keys: []ApiKey{{
		Key:                 "pk_exists",
		Name:                "Exists",
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

	// Should not panic or cause issues
	ks.UpdateUsage("pk_nonexistent", 500, 0)

	key, _ := ks.FindKey("pk_exists")
	if key.TotalLifetimeTokens != 0 {
		t.Fatal("unrelated key should not be affected")
	}
}

func TestApiKey_IsExpired_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		date     string
		expired  bool
	}{
		{"past date", "2020-01-01T00:00:00Z", true},
		{"far future", "2099-01-01T00:00:00Z", false},
		{"empty string", "", true},
		{"invalid format", "not-a-date", true},
		{"date only without time", "2099-01-01", true}, // not RFC3339
		{"with timezone offset", "2099-01-01T00:00:00+07:00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &ApiKey{ExpiryDate: tt.date}
			if k.IsExpired() != tt.expired {
				t.Errorf("IsExpired(%q) = %v, want %v", tt.date, !tt.expired, tt.expired)
			}
		})
	}
}
