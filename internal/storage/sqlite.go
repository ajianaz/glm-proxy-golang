package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"glm-proxy/internal/litellm"

	_ "modernc.org/sqlite"
)

// KeyStore manages API key data backed by SQLite (WAL mode).
// It preserves the same public API as the JSON keystore.
type KeyStore struct {
	mu   sync.RWMutex
	db   *sql.DB
	path string // path to the SQLite file
}

// NewKeyStore opens or creates a SQLite database at dataFile.
// If dataFile is a JSON file that exists and the SQLite DB is empty, it migrates.
func NewKeyStore(dataFile string) (*KeyStore, error) {
	// Determine SQLite path: replace .json extension with .db, or append .db
	sqlitePath := dataFile
	if ext := filepath.Ext(dataFile); ext == ".json" {
		sqlitePath = dataFile[:len(dataFile)-len(ext)] + ".db"
	}

	ks := &KeyStore{
		path: sqlitePath,
	}

	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	ks.db = db

	// WAL mode for concurrent read safety
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	if err := ks.createTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	// Run migrations (non-destructive ALTER TABLE ADD COLUMN)
	if err := ks.migrate(); err != nil {
		log.Printf("[keystore-sqlite] migration warning: %v", err)
	}

	// Migrate from JSON if needed
	if err := ks.migrateFromJSON(dataFile); err != nil {
		log.Printf("[keystore-sqlite] JSON migration warning: %v", err)
	}

	return ks, nil
}

func (ks *KeyStore) createTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS api_keys (
		id                    INTEGER PRIMARY KEY AUTOINCREMENT,
		key                   TEXT    UNIQUE NOT NULL,
		name                  TEXT    NOT NULL DEFAULT '',
		model                 TEXT    NOT NULL DEFAULT '',
		glm_key               TEXT    NOT NULL DEFAULT '',
		upstream_key          TEXT    NOT NULL DEFAULT '',
		token_limit_per_5h    INTEGER NOT NULL DEFAULT 0,
		expiry_date           TEXT    NOT NULL DEFAULT '',
		created_at            TEXT    NOT NULL DEFAULT '',
		last_used             TEXT    NOT NULL DEFAULT '',
		total_requests        INTEGER NOT NULL DEFAULT 0,
		total_lifetime_tokens INTEGER NOT NULL DEFAULT 0,
		total_spend_usd       REAL    NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS usage_windows (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		api_key_id   INTEGER NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
		window_start TEXT    NOT NULL DEFAULT '',
		tokens_used  INTEGER NOT NULL DEFAULT 0,
		requests     INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		spend_usd    REAL    NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_usage_windows_key_id ON usage_windows(api_key_id);
	`
	_, err := ks.db.Exec(schema)
	return err
}

// migrate runs non-destructive schema migrations.
// Each migration adds new columns with defaults — safe for existing data.
func (ks *KeyStore) migrate() error {
	migrations := []struct {
		table string
		col   string
		def   string
	}{
		{"api_keys", "upstream_key", "TEXT NOT NULL DEFAULT ''"},
		{"api_keys", "total_spend_usd", "REAL NOT NULL DEFAULT 0"},
		{"usage_windows", "spend_usd", "REAL NOT NULL DEFAULT 0"},
	}

	for _, m := range migrations {
		// Check if column already exists (SQLite doesn't have IF NOT EXISTS for ALTER TABLE)
		var count int
		err := ks.db.QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
			m.table, m.col,
		).Scan(&count)
		if err != nil {
			log.Printf("[keystore-sqlite] migration check %s.%s: %v", m.table, m.col, err)
			continue
		}
		if count > 0 {
			continue // column already exists
		}

		sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", m.table, m.col, m.def)
		if _, err := ks.db.Exec(sql); err != nil {
			log.Printf("[keystore-sqlite] migration add %s.%s: %v", m.table, m.col, err)
			continue
		}
		log.Printf("[keystore-sqlite] migration: added %s.%s", m.table, m.col)
	}

	// Copy glm_key → upstream_key ONLY if it's a valid LiteLLM key (sk-*)
	// Old Z.AI direct keys (non-sk) should fall back to MASTER_KEY
	ks.db.Exec("UPDATE api_keys SET upstream_key = glm_key WHERE upstream_key = '' AND glm_key != '' AND glm_key LIKE 'sk-%'")

	// Clean existing rows: clear upstream_key that are not valid LiteLLM keys (sk-*)
	// This fixes data from previous migration that blindly copied all glm_keys
	ks.db.Exec("UPDATE api_keys SET upstream_key = '' WHERE upstream_key NOT LIKE 'sk-%' AND upstream_key != ''")

	return nil
}

// migrateFromJSON imports keys from a JSON file if SQLite is empty.
func (ks *KeyStore) migrateFromJSON(jsonFile string) error {
	// Only migrate if JSON file exists
	if _, err := os.Stat(jsonFile); os.IsNotExist(err) {
		return nil
	}

	// Check if SQLite already has data
	var count int
	if err := ks.db.QueryRow("SELECT COUNT(*) FROM api_keys").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil // already migrated or has data
	}

	data, err := os.ReadFile(jsonFile)
	if err != nil {
		return fmt.Errorf("read json: %w", err)
	}

	var apiData ApiKeysData
	if err := json.Unmarshal(data, &apiData); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}

	if len(apiData.Keys) == 0 {
		return nil
	}

	tx, err := ks.db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback()

	insertKey, err := tx.Prepare(`
		INSERT INTO api_keys (key, name, model, glm_key, upstream_key, token_limit_per_5h, expiry_date, created_at, last_used, total_requests, total_lifetime_tokens, total_spend_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert key: %w", err)
	}
	defer insertKey.Close()

	insertWindow, err := tx.Prepare(`
		INSERT INTO usage_windows (api_key_id, window_start, tokens_used, requests, cached_tokens, spend_usd)
		VALUES (?, ?, ?, ?, ?, 0)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert window: %w", err)
	}
	defer insertWindow.Close()

	for _, k := range apiData.Keys {
		// Only use UpstreamKey if it's a valid LiteLLM key (sk-*)
		// Old glmkey (Z.AI direct keys) should NOT be copied to upstream_key
		// They will fall back to MASTER_KEY via GetUpstreamKey()
		validUpstreamKey := ""
		if strings.HasPrefix(k.UpstreamKey, "sk-") {
			validUpstreamKey = k.UpstreamKey
		}

		res, err := insertKey.Exec(k.Key, k.Name, k.Model, k.UpstreamKey, validUpstreamKey, k.TokenLimitPer5h,
			k.ExpiryDate, k.CreatedAt, k.LastUsed, k.TotalRequests, k.TotalLifetimeTokens)
		if err != nil {
			return fmt.Errorf("insert key %s: %w", MaskKey(k.Key), err)
		}
		keyID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("get key id: %w", err)
		}
		for _, w := range k.UsageWindows {
			if _, err := insertWindow.Exec(keyID, w.WindowStart, w.TokensUsed, w.Requests, w.CachedTokens); err != nil {
				return fmt.Errorf("insert window for key %s: %w", MaskKey(k.Key), err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	// Rename JSON file to .migrated
	migratedPath := jsonFile + ".migrated"
	if err := os.Rename(jsonFile, migratedPath); err != nil {
		log.Printf("[keystore-sqlite] warning: could not rename %s to %s: %v", jsonFile, migratedPath, err)
	} else {
		log.Printf("[keystore-sqlite] migrated %d keys from %s → %s", len(apiData.Keys), jsonFile, migratedPath)
	}

	return nil
}

// FindKey looks up an API key by its value, including its usage windows.
func (ks *KeyStore) FindKey(key string) (*ApiKey, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	row := ks.db.QueryRow(`
		SELECT id, key, name, model, upstream_key, token_limit_per_5h, expiry_date, created_at, last_used, total_requests, total_lifetime_tokens, total_spend_usd
		FROM api_keys WHERE key = ?`, key)

	var ak ApiKey
	var id int64
	err := row.Scan(&id, &ak.Key, &ak.Name, &ak.Model, &ak.UpstreamKey, &ak.TokenLimitPer5h,
		&ak.ExpiryDate, &ak.CreatedAt, &ak.LastUsed, &ak.TotalRequests, &ak.TotalLifetimeTokens, &ak.TotalSpendUSD)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		log.Printf("[keystore-sqlite] FindKey scan error: %v", err)
		return nil, false
	}

	// Load usage windows
	ak.UsageWindows, err = ks.loadWindows(id)
	if err != nil {
		log.Printf("[keystore-sqlite] FindKey load windows error: %v", err)
	}

	return &ak, true
}

// loadWindows reads all usage windows for a key ID. Caller must hold at least RLock.
func (ks *KeyStore) loadWindows(keyID int64) ([]UsageWindow, error) {
	rows, err := ks.db.Query(`
		SELECT window_start, tokens_used, requests, cached_tokens, spend_usd
		FROM usage_windows WHERE api_key_id = ? ORDER BY window_start`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []UsageWindow
	for rows.Next() {
		var w UsageWindow
		if err := rows.Scan(&w.WindowStart, &w.TokensUsed, &w.Requests, &w.CachedTokens, &w.SpendUSD); err != nil {
			return nil, err
		}
		windows = append(windows, w)
	}
	return windows, rows.Err()
}

// AllKeys returns a copy of all keys with their usage windows.
func (ks *KeyStore) AllKeys() []ApiKey {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	rows, err := ks.db.Query(`
		SELECT id, key, name, model, upstream_key, token_limit_per_5h, expiry_date, created_at, last_used, total_requests, total_lifetime_tokens, total_spend_usd
		FROM api_keys`)
	if err != nil {
		log.Printf("[keystore-sqlite] AllKeys query error: %v", err)
		return nil
	}
	defer rows.Close()

	var keys []ApiKey
	for rows.Next() {
		var ak ApiKey
		var id int64
		if err := rows.Scan(&id, &ak.Key, &ak.Name, &ak.Model, &ak.UpstreamKey, &ak.TokenLimitPer5h,
			&ak.ExpiryDate, &ak.CreatedAt, &ak.LastUsed, &ak.TotalRequests, &ak.TotalLifetimeTokens, &ak.TotalSpendUSD); err != nil {
			log.Printf("[keystore-sqlite] AllKeys scan error: %v", err)
			continue
		}
		ak.UsageWindows, _ = ks.loadWindows(id)
		keys = append(keys, ak)
	}
	return keys
}

// UpdateUsage atomically updates token usage and cost for a key, consolidating active windows.
func (ks *KeyStore) UpdateUsage(keyValue string, tokensUsed int, cachedTokens int, cost float64) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	now := time.Now().UTC()
	fiveHoursAgo := now.Add(-5 * time.Hour)

	tx, err := ks.db.Begin()
	if err != nil {
		log.Printf("[keystore-sqlite] UpdateUsage begin error: %v", err)
		return
	}
	defer tx.Rollback()

	var keyID int64
	var totalRequests, totalLifetimeTokens int
	var totalSpend float64
	err = tx.QueryRow(`
		SELECT id, total_requests, total_lifetime_tokens, total_spend_usd
		FROM api_keys WHERE key = ?`, keyValue).Scan(&keyID, &totalRequests, &totalLifetimeTokens, &totalSpend)
	if err != nil {
		if err == sql.ErrNoRows {
			return // key not found, silently ignore (matches original behavior)
		}
		log.Printf("[keystore-sqlite] UpdateUsage key lookup error: %v", err)
		return
	}

	// Load all usage windows for this key
	rows, err := tx.Query(`
		SELECT id, window_start, tokens_used, requests, cached_tokens, spend_usd
		FROM usage_windows WHERE api_key_id = ?`, keyID)
	if err != nil {
		log.Printf("[keystore-sqlite] UpdateUsage load windows error: %v", err)
		return
	}
	defer rows.Close()

	var activeTokens, activeRequests, activeCached int
	var activeSpend float64
	var earliestStart time.Time
	var windowIDs []int64 // IDs of windows to delete (all of them)

	for rows.Next() {
		var wID int64
		var w UsageWindow
		if err := rows.Scan(&wID, &w.WindowStart, &w.TokensUsed, &w.Requests, &w.CachedTokens, &w.SpendUSD); err != nil {
			log.Printf("[keystore-sqlite] UpdateUsage window scan error: %v", err)
			return
		}
		ws, err := time.Parse(time.RFC3339, w.WindowStart)
		if err != nil {
			// Malformed window — still delete it, just don't count its tokens
			windowIDs = append(windowIDs, wID)
			continue
		}
		if !ws.Before(fiveHoursAgo) {
			activeTokens += w.TokensUsed
			activeRequests += w.Requests
			activeCached += w.CachedTokens
			activeSpend += w.SpendUSD
			if earliestStart.IsZero() || ws.Before(earliestStart) {
				earliestStart = ws
			}
		}
		windowIDs = append(windowIDs, wID)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[keystore-sqlite] UpdateUsage rows error: %v", err)
		return
	}

	// Consolidate: add new usage to active totals
	activeTokens += tokensUsed
	activeRequests++
	activeCached += cachedTokens
	activeSpend += cost
	if earliestStart.IsZero() {
		earliestStart = now
	}

	// Delete all old windows
	if len(windowIDs) > 0 {
		placeholders := make([]string, len(windowIDs))
		args := make([]interface{}, len(windowIDs))
		for i, id := range windowIDs {
			placeholders[i] = "?"
			args[i] = id
		}
		delSQL := "DELETE FROM usage_windows WHERE id IN (" + joinPlaceholders(placeholders) + ")"
		if _, err := tx.Exec(delSQL, args...); err != nil {
			log.Printf("[keystore-sqlite] UpdateUsage delete windows error: %v", err)
			return
		}
	}

	// Insert the single consolidated window
	_, err = tx.Exec(`
		INSERT INTO usage_windows (api_key_id, window_start, tokens_used, requests, cached_tokens, spend_usd)
		VALUES (?, ?, ?, ?, ?, ?)`,
		keyID, earliestStart.Format(time.RFC3339), activeTokens, activeRequests, activeCached, activeSpend)
	if err != nil {
		log.Printf("[keystore-sqlite] UpdateUsage insert window error: %v", err)
		return
	}

	// Update key stats (tokens + cost)
	_, err = tx.Exec(`
		UPDATE api_keys SET last_used = ?, total_requests = ?, total_lifetime_tokens = ?, total_spend_usd = ? WHERE id = ?`,
		now.Format(time.RFC3339), totalRequests+1, totalLifetimeTokens+tokensUsed, totalSpend+cost, keyID)
	if err != nil {
		log.Printf("[keystore-sqlite] UpdateUsage update key error: %v", err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[keystore-sqlite] UpdateUsage commit error: %v", err)
		return
	}

	log.Printf("[keystore-sqlite] UpdateUsage: key=%s tokens=%d cached=%d cost=%.4f requests=%d lifetime=%d active=%d",
		MaskKey(keyValue), tokensUsed, cachedTokens, cost, totalRequests+1, totalLifetimeTokens+tokensUsed, activeTokens)
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
			WindowSpendUSD:            info.WindowSpendUSD,
		},
		TotalRequests:       key.TotalRequests,
		TotalLifetimeTokens: key.TotalLifetimeTokens,
		TotalSpendUSD:       key.TotalSpendUSD,
	}
}

// Close closes the SQLite database. Safe to call multiple times.
func (ks *KeyStore) Close() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.db.Close()
}

// IsDirty always returns false for the SQLite backend (no dirty mechanism needed).
func (ks *KeyStore) IsDirty() bool {
	return false
}

// DB returns the underlying *sql.DB for admin operations.
func (ks *KeyStore) DB() *sql.DB {
	return ks.db
}

// MigrateLiteLLM ensures all API keys have valid LiteLLM virtual keys.
// It creates the "glm-proxy" team if needed and generates virtual keys
// for any existing keys that don't have a valid upstream_key.
// Returns nil if all migrations succeeded, error if LiteLLM is unreachable.
func (ks *KeyStore) MigrateLiteLLM(baseURL, masterKey string) error {
	if masterKey == "" {
		log.Printf("[litellm-migration] skipped: MASTER_KEY not set")
		return nil
	}

	client := litellm.NewClient(baseURL, masterKey)

	// Step 1: Ensure team exists
	teamID, err := client.EnsureTeam("glm-proxy")
	if err != nil {
		return fmt.Errorf("ensure team: %w", err)
	}

	// Step 2: Find keys without valid upstream_key
	rows, err := ks.db.Query(
		"SELECT id, name FROM api_keys WHERE upstream_key = '' OR upstream_key NOT LIKE 'sk-%' ORDER BY id",
	)
	if err != nil {
		return fmt.Errorf("query keys without upstream: %w", err)
	}
	defer rows.Close()

	type keyInfo struct {
		ID   int64
		Name string
	}

	var keys []keyInfo
	for rows.Next() {
		var k keyInfo
		if err := rows.Scan(&k.ID, &k.Name); err != nil {
			return fmt.Errorf("scan key: %w", err)
		}
		keys = append(keys, k)
	}

	if len(keys) == 0 {
		log.Printf("[litellm-migration] all keys already have valid upstream_key")
		return nil
	}

	log.Printf("[litellm-migration] found %d keys without valid upstream_key, generating...", len(keys))

	// Step 3: Generate virtual key for each
	for _, k := range keys {
		alias := k.Name
		if alias == "" {
			alias = fmt.Sprintf("key-%d", k.ID)
		}

		virtualKey, err := client.GenerateKey(teamID, alias)
		if err != nil {
			log.Printf("[litellm-migration] failed to generate key for #%d (%s): %v", k.ID, k.Name, err)
			continue // skip this key, don't block others
		}

		_, err = ks.db.Exec("UPDATE api_keys SET upstream_key = ? WHERE id = ?", virtualKey, k.ID)
		if err != nil {
			log.Printf("[litellm-migration] failed to update key #%d: %v", k.ID, err)
			continue
		}

		log.Printf("[litellm-migration] migrated key #%d (%s) → sk-...%s", k.ID, k.Name, virtualKey[len(virtualKey)-8:])
	}

	return nil
}

// MaskKey returns a masked version of the API key for logging and responses.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-3:]
}

// joinPlaceholders joins SQL placeholder strings.
func joinPlaceholders(phs []string) string {
	result := ""
	for i, p := range phs {
		if i > 0 {
			result += ","
		}
		result += p
	}
	return result
}
