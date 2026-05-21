package storage

import (
	"encoding/json"
	"time"
)

// UsageWindow tracks token usage within a time window.
type UsageWindow struct {
	WindowStart string  `json:"window_start"`
	TokensUsed  int     `json:"tokens_used"`
	Requests    int     `json:"requests"`
	CachedTokens int    `json:"cached_tokens"`
	SpendUSD    float64 `json:"spend_usd"`
}

// ApiKey represents a single API key with metadata.
type ApiKey struct {
	Key                 string        `json:"key"`
	Name                string        `json:"name"`
	Model               string        `json:"model,omitempty"`
	UpstreamKey         string        `json:"upstream_key,omitempty"`
	TokenLimitPer5h     int           `json:"token_limit_per_5h"`
	ExpiryDate          string        `json:"expiry_date"`
	CreatedAt           string        `json:"created_at"`
	LastUsed            string        `json:"last_used"`
	TotalRequests       int           `json:"total_requests"`
	TotalLifetimeTokens int           `json:"total_lifetime_tokens"`
	TotalSpendUSD       float64       `json:"total_spend_usd"`
	UsageWindows        []UsageWindow `json:"usage_windows"`
}

// UnmarshalJSON implements backward compat: accept both "upstream_key" (new) and "glmkey" (legacy).
func (k *ApiKey) UnmarshalJSON(data []byte) error {
	type Alias ApiKey
	aux := &struct {
		GlmKey    string `json:"glmkey"`
		UpstreamKey string `json:"upstream_key"`
		*Alias
	}{
		Alias: (*Alias)(k),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	// Prefer new field, fall back to legacy
	if aux.UpstreamKey != "" {
		k.UpstreamKey = aux.UpstreamKey
	} else {
		k.UpstreamKey = aux.GlmKey
	}
	return nil
}

// UpstreamKeyField returns the per-key upstream key if set, otherwise the master key.
// Renamed from UpstreamKey() to avoid conflict with the field name.
func (k *ApiKey) GetUpstreamKey(masterKey string) string {
	if k.UpstreamKey != "" {
		return k.UpstreamKey
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
	TotalRequests       int          `json:"total_requests"`
	TotalLifetimeTokens int          `json:"total_lifetime_tokens"`
	TotalSpendUSD       float64      `json:"total_spend_usd"`
}

// CurrentUsage shows usage within the current rolling window.
type CurrentUsage struct {
	TokensUsedInCurrentWindow int    `json:"tokens_used_in_current_window"`
	WindowStartedAt           string `json:"window_started_at"`
	WindowEndsAt              string `json:"window_ends_at"`
	RemainingTokens           int    `json:"remaining_tokens"`
	WindowSpendUSD            float64 `json:"window_spend_usd"`
}

// RateLimitInfo holds the result of a rate limit check.
type RateLimitInfo struct {
	Allowed        bool
	TokensUsed     int
	TokensLimit    int
	WindowStart    string
	WindowEnd      string
	RetryAfter     int // seconds
	Reason         string
	WindowSpendUSD float64
}
