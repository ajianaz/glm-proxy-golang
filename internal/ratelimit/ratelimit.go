package ratelimit

import (
	"time"

	"glm-proxy/internal/storage"
)

// CheckRateLimit implements a rolling 5-hour window rate limit check.
func CheckRateLimit(key *storage.ApiKey) *storage.RateLimitInfo {
	fiveHoursAgo := time.Now().Add(-5 * time.Hour).UTC()
	now := time.Now().UTC()

	var totalTokensUsed int
	var windowStart time.Time

	for _, w := range key.UsageWindows {
		ws, err := time.Parse(time.RFC3339, w.WindowStart)
		if err != nil {
			continue
		}
		if !ws.Before(fiveHoursAgo) {
			totalTokensUsed += w.TokensUsed
			if windowStart.IsZero() || ws.Before(windowStart) {
				windowStart = ws
			}
		}
	}

	if windowStart.IsZero() {
		windowStart = now
	}

	windowEnd := windowStart.Add(5 * time.Hour)

	info := &storage.RateLimitInfo{
		Allowed:     true,
		TokensUsed:  totalTokensUsed,
		TokensLimit: key.TokenLimitPer5h,
		WindowStart: windowStart.UTC().Format(time.RFC3339),
		WindowEnd:   windowEnd.UTC().Format(time.RFC3339),
	}

	// Uses > (strictly greater) to match the TS implementation.
	if totalTokensUsed > key.TokenLimitPer5h {
		info.Allowed = false
		retryAfter := int(time.Until(windowEnd).Seconds())
		if retryAfter < 0 {
			retryAfter = 0
		}
		info.RetryAfter = retryAfter
		info.Reason = "Token limit exceeded for current 5-hour window"
	}

	return info
}
