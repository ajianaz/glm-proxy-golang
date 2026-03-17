package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// KeyStore manages in-memory API key data with periodic disk flushing.
type KeyStore struct {
	mu          sync.RWMutex
	data        ApiKeysData
	dataFile    string
	dirty       bool
	stopFlush   chan struct{}
}

// NewKeyStore loads API keys from the given JSON file into memory.
func NewKeyStore(dataFile string) (*KeyStore, error) {
	ks := &KeyStore{
		dataFile:  dataFile,
		stopFlush: make(chan struct{}),
	}

	if err := ks.load(); err != nil {
		// If file doesn't exist, start with empty data
		if !os.IsNotExist(err) {
			return nil, err
		}
		ks.data = ApiKeysData{Keys: []ApiKey{}}
	}

	go ks.flushLoop()
	return ks, nil
}

// load reads the JSON file from disk.
func (ks *KeyStore) load() error {
	data, err := os.ReadFile(ks.dataFile)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &ks.data)
}

// save atomically writes data to disk.
func (ks *KeyStore) save() error {
	if err := os.MkdirAll(filepath.Dir(ks.dataFile), 0755); err != nil {
		return err
	}

	tmp := ks.dataFile + ".tmp"
	b, err := json.MarshalIndent(ks.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, ks.dataFile)
}

// flushLoop periodically saves dirty data to disk.
func (ks *KeyStore) flushLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ks.mu.Lock()
			if ks.dirty {
				_ = ks.save()
				ks.dirty = false
			}
			ks.mu.Unlock()
		case <-ks.stopFlush:
			return
		}
	}
}

// Close stops the flush goroutine and does a final save if dirty.
func (ks *KeyStore) Close() {
	close(ks.stopFlush)
	ks.mu.Lock()
	if ks.dirty {
		_ = ks.save()
	}
	ks.mu.Unlock()
}

// FindKey looks up an API key by its value.
func (ks *KeyStore) FindKey(key string) (*ApiKey, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	for i := range ks.data.Keys {
		if ks.data.Keys[i].Key == key {
			return &ks.data.Keys[i], true
		}
	}
	return nil, false
}

// GetStats returns a StatsResponse for a given key.
func (ks *KeyStore) GetStats(key *ApiKey, info *RateLimitInfo, model string) StatsResponse {
	return StatsResponse{
		Key:               key.Key,
		Name:              key.Name,
		Model:             model,
		TokenLimitPer5h:   key.TokenLimitPer5h,
		ExpiryDate:        key.ExpiryDate,
		CreatedAt:         key.CreatedAt,
		LastUsed:          key.LastUsed,
		IsExpired:         key.IsExpired(),
		CurrentUsage: CurrentUsage{
			TokensUsedInCurrentWindow: info.TokensUsed,
			WindowStartedAt:           info.WindowStart,
			WindowEndsAt:              info.WindowEnd,
			RemainingTokens:           max(0, info.TokensLimit-info.TokensUsed),
		},
		TotalLifetimeTokens: key.TotalLifetimeTokens,
	}
}

// UpdateUsage atomically updates token usage for a key.
func (ks *KeyStore) UpdateUsage(keyValue string, tokensUsed int) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	fiveHoursAgo := time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339Nano)

	for i := range ks.data.Keys {
		if ks.data.Keys[i].Key == keyValue {
			k := &ks.data.Keys[i]
			k.LastUsed = now
			k.TotalLifetimeTokens += tokensUsed

			// Find or create current window
			found := false
			for j := range k.UsageWindows {
				if k.UsageWindows[j].WindowStart >= fiveHoursAgo {
					k.UsageWindows[j].TokensUsed += tokensUsed
					found = true
					break
				}
			}
			if !found {
				k.UsageWindows = append(k.UsageWindows, UsageWindow{
					WindowStart: now,
					TokensUsed:  tokensUsed,
				})
			}

			// Clean up old windows
			cleaned := k.UsageWindows[:0]
			for _, w := range k.UsageWindows {
				if w.WindowStart >= fiveHoursAgo {
					cleaned = append(cleaned, w)
				}
			}
			k.UsageWindows = cleaned
			ks.dirty = true
			return
		}
	}
}

// AllKeys returns a copy of all keys (for admin endpoints if needed).
func (ks *KeyStore) AllKeys() []ApiKey {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	out := make([]ApiKey, len(ks.data.Keys))
	copy(out, ks.data.Keys)
	return out
}
