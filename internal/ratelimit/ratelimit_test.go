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
