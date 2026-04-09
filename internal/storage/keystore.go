package storage

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// KeyStore manages in-memory API key data with periodic disk flushing.
type KeyStore struct {
	mu        sync.RWMutex
	data      ApiKeysData
	dataFile  string
	dirty     bool
	stopFlush chan struct{}
	closed    bool
	closeOnce sync.Once
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
				if err := ks.save(); err != nil {
					log.Printf("[keystore] flush error: %v", err)
					// Keep dirty=true so next tick retries
				} else {
					log.Printf("[keystore] flushed to %s", ks.dataFile)
					ks.dirty = false
				}
			}
			ks.mu.Unlock()
		case <-ks.stopFlush:
			return
		}
	}
}

// Close stops the flush goroutine and does a final save if dirty.
// Safe to call multiple times.
func (ks *KeyStore) Close() {
	ks.closeOnce.Do(func() {
		close(ks.stopFlush)
		ks.mu.Lock()
		if ks.dirty {
			_ = ks.save()
		}
		ks.closed = true
		ks.mu.Unlock()
	})
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
		Key:               MaskKey(key.Key),
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
		TotalRequests:       key.TotalRequests,
		TotalLifetimeTokens: key.TotalLifetimeTokens,
	}
}

// UpdateUsage atomically updates token usage for a key.
func (ks *KeyStore) UpdateUsage(keyValue string, tokensUsed int) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	now := time.Now().UTC()
	fiveHoursAgo := now.Add(-5 * time.Hour)

	for i := range ks.data.Keys {
		if ks.data.Keys[i].Key == keyValue {
			k := &ks.data.Keys[i]
			k.LastUsed = now.Format(time.RFC3339)
			k.TotalRequests++
			k.TotalLifetimeTokens += tokensUsed

			// Sum all active window tokens and find earliest window start
			var activeTokens int
			var earliestStart time.Time
			for _, w := range k.UsageWindows {
				ws, err := time.Parse(time.RFC3339, w.WindowStart)
				if err != nil {
					continue
				}
				if !ws.Before(fiveHoursAgo) {
					activeTokens += w.TokensUsed
					if earliestStart.IsZero() || ws.Before(earliestStart) {
						earliestStart = ws
					}
				}
			}

			// Consolidate all active windows + new tokens into a single window
			activeTokens += tokensUsed
			if earliestStart.IsZero() {
				earliestStart = now
			}
			k.UsageWindows = []UsageWindow{
				{
					WindowStart: earliestStart.Format(time.RFC3339),
					TokensUsed:  activeTokens,
				},
			}

			// Save immediately to ensure data persists even if container restarts
			if err := ks.save(); err != nil {
				log.Printf("[keystore] UpdateUsage save error: %v", err)
				ks.dirty = true // keep dirty so flush loop retries
			}
			log.Printf("[keystore] UpdateUsage: key=%s tokens=%d requests=%d lifetime=%d active=%d",
				MaskKey(k.Key), tokensUsed, k.TotalRequests, k.TotalLifetimeTokens, activeTokens)
			return
		}
	}
}

// MaskKey returns a masked version of the API key for logging and responses.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-3:]
}

// IsDirty returns whether there are unsaved changes (for testing).
func (ks *KeyStore) IsDirty() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.dirty
}

// AllKeys returns a copy of all keys (for admin endpoints if needed).
func (ks *KeyStore) AllKeys() []ApiKey {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	out := make([]ApiKey, len(ks.data.Keys))
	copy(out, ks.data.Keys)
	return out
}
