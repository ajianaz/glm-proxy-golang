package storage

import "time"

// UsageWindow tracks token usage within a time window.
type UsageWindow struct {
	WindowStart string `json:"window_start"`
	TokensUsed  int    `json:"tokens_used"`
}

// ApiKey represents a single API key with metadata.
type ApiKey struct {
	Key                 string        `json:"key"`
	Name                string        `json:"name"`
	Model               string        `json:"model,omitempty"`
	GlmKey              string        `json:"glmkey,omitempty"`
	TokenLimitPer5h     int           `json:"token_limit_per_5h"`
	ExpiryDate          string        `json:"expiry_date"`
	CreatedAt           string        `json:"created_at"`
	LastUsed            string        `json:"last_used"`
	TotalLifetimeTokens int           `json:"total_lifetime_tokens"`
	UsageWindows        []UsageWindow `json:"usage_windows"`
}

// UpstreamKey returns the per-key upstream key if set, otherwise the master key.
func (k *ApiKey) UpstreamKey(masterKey string) string {
	if k.GlmKey != "" {
		return k.GlmKey
	}
	return masterKey
}

// IsExpired returns true if the key's expiry date has passed.
func (k *ApiKey) IsExpired() bool {
	t, err := time.Parse(time.RFC3339, k.ExpiryDate)
	if err != nil {
		return true
	}
	return time.Now().UTC().After(t)
}

// ApiKeysData is the top-level JSON structure.
type ApiKeysData struct {
	Keys []ApiKey `json:"keys"`
}

// StatsResponse is returned by the /stats endpoint.
type StatsResponse struct {
	Key                 string       `json:"key"`
	Name                string       `json:"name"`
	Model               string       `json:"model"`
	TokenLimitPer5h     int          `json:"token_limit_per_5h"`
	ExpiryDate          string       `json:"expiry_date"`
	CreatedAt           string       `json:"created_at"`
	LastUsed            string       `json:"last_used"`
	IsExpired           bool         `json:"is_expired"`
	CurrentUsage        CurrentUsage `json:"current_usage"`
	TotalLifetimeTokens int          `json:"total_lifetime_tokens"`
}

// CurrentUsage shows usage within the current rolling window.
type CurrentUsage struct {
	TokensUsedInCurrentWindow int    `json:"tokens_used_in_current_window"`
	WindowStartedAt           string `json:"window_started_at"`
	WindowEndsAt              string `json:"window_ends_at"`
	RemainingTokens           int    `json:"remaining_tokens"`
}

// RateLimitInfo holds the result of a rate limit check.
type RateLimitInfo struct {
	Allowed     bool
	TokensUsed  int
	TokensLimit int
	WindowStart string
	WindowEnd   string
	RetryAfter  int // seconds
	Reason      string
}
