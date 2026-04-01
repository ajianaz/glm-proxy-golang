package ratelimit

import (
	"testing"
	"time"

	"glm-proxy/internal/storage"
)

func makeKey(tokens int, limit int) *storage.ApiKey {
	return &storage.ApiKey{
		Key:             "pk_test",
		TokenLimitPer5h: limit,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		UsageWindows: []storage.UsageWindow{
			{WindowStart: time.Now().UTC().Format(time.RFC3339), TokensUsed: tokens},
		},
	}
}

func TestCheckRateLimit_Allowed(t *testing.T) {
	info := CheckRateLimit(makeKey(500, 1000))
	if !info.Allowed {
		t.Fatal("should be allowed")
	}
	if info.TokensUsed != 500 {
		t.Fatalf("expected 500, got %d", info.TokensUsed)
	}
}

func TestCheckRateLimit_ExactlyAtLimit(t *testing.T) {
	info := CheckRateLimit(makeKey(1000, 1000))
	if !info.Allowed {
		t.Fatal("should be allowed when exactly at limit (uses > not >=)")
	}
}

func TestCheckRateLimit_Exceeded(t *testing.T) {
	info := CheckRateLimit(makeKey(1001, 1000))
	if info.Allowed {
		t.Fatal("should be blocked when over limit")
	}
	if info.RetryAfter < 0 {
		t.Fatal("retryAfter should be non-negative")
	}
}

func TestApiKey_IsExpired(t *testing.T) {
	expired := &storage.ApiKey{ExpiryDate: "2020-01-01T00:00:00Z"}
	if !expired.IsExpired() {
		t.Fatal("should be expired")
	}

	valid := &storage.ApiKey{ExpiryDate: "2099-01-01T00:00:00Z"}
	if valid.IsExpired() {
		t.Fatal("should not be expired")
	}
}

func TestCheckRateLimit_SumsMultipleActiveWindows(t *testing.T) {
	now := time.Now().UTC()
	key := &storage.ApiKey{
		Key:             "pk_multi",
		TokenLimitPer5h: 1000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		UsageWindows: []storage.UsageWindow{
			{WindowStart: now.Add(-1 * time.Hour).Format(time.RFC3339), TokensUsed: 200},
			{WindowStart: now.Add(-2 * time.Hour).Format(time.RFC3339), TokensUsed: 300},
		},
	}

	info := CheckRateLimit(key)
	if info.TokensUsed != 500 {
		t.Fatalf("expected 500 tokens (sum of all active windows), got %d", info.TokensUsed)
	}
	if !info.Allowed {
		t.Fatal("should be allowed (500 < 1000)")
	}
}

func TestCheckRateLimit_IgnoresExpiredWindows(t *testing.T) {
	now := time.Now().UTC()
	key := &storage.ApiKey{
		Key:             "pk_old",
		TokenLimitPer5h: 1000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		UsageWindows: []storage.UsageWindow{
			{WindowStart: now.Add(-6 * time.Hour).Format(time.RFC3339), TokensUsed: 900}, // expired
			{WindowStart: now.Add(-1 * time.Hour).Format(time.RFC3339), TokensUsed: 100},  // active
		},
	}

	info := CheckRateLimit(key)
	if info.TokensUsed != 100 {
		t.Fatalf("expected 100 tokens (only active window), got %d", info.TokensUsed)
	}
}

func TestCheckRateLimit_SkipsMalformedWindowStart(t *testing.T) {
	now := time.Now().UTC()
	key := &storage.ApiKey{
		Key:             "pk_bad",
		TokenLimitPer5h: 1000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		UsageWindows: []storage.UsageWindow{
			{WindowStart: "not-a-date", TokensUsed: 999},
			{WindowStart: now.Format(time.RFC3339), TokensUsed: 100},
		},
	}

	info := CheckRateLimit(key)
	if info.TokensUsed != 100 {
		t.Fatalf("expected 100 (malformed skipped), got %d", info.TokensUsed)
	}
}

func TestCheckRateLimit_NoWindows(t *testing.T) {
	key := &storage.ApiKey{
		Key:             "pk_empty",
		TokenLimitPer5h: 1000,
		ExpiryDate:      "2099-01-01T00:00:00Z",
		UsageWindows:    []storage.UsageWindow{},
	}

	info := CheckRateLimit(key)
	if info.TokensUsed != 0 {
		t.Fatalf("expected 0 tokens, got %d", info.TokensUsed)
	}
	if !info.Allowed {
		t.Fatal("should be allowed with no usage")
	}
}
