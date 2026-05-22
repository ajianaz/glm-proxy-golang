package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"glm-proxy/internal/storage"
)

// AdminHandler handles /admin/* routes, gated by master API key.
type AdminHandler struct {
	db *sql.DB
}

// NewAdminHandler creates an admin handler. The db is the raw *sql.DB
// from the SQLite KeyStore — exposed via a new DB() method.
func NewAdminHandler(db *sql.DB) *AdminHandler {
	return &AdminHandler{db: db}
}

// --- Request/Response types ---

type createKeyRequest struct {
	Name            string  `json:"name"`
	Model           string  `json:"model,omitempty"`
	UpstreamKey     string  `json:"upstream_key,omitempty"`
	GlmKey          string  `json:"glmkey,omitempty"` // backward compat
	TokenLimitPer5h int     `json:"token_limit_per_5h"`
	ExpiryDate      string  `json:"expiry_date"`
}

// upstreamKeyValue returns the upstream key from the request, supporting both field names.
func (r *createKeyRequest) upstreamKeyValue() string {
	if r.UpstreamKey != "" {
		return r.UpstreamKey
	}
	return r.GlmKey
}

type updateKeyRequest struct {
	Name            *string `json:"name,omitempty"`
	Model           *string `json:"model,omitempty"`
	UpstreamKey     *string `json:"upstream_key,omitempty"`
	GlmKey          *string `json:"glmkey,omitempty"` // backward compat
	TokenLimitPer5h *int    `json:"token_limit_per_5h,omitempty"`
	ExpiryDate      *string `json:"expiry_date,omitempty"`
}

// upstreamKeyValue returns the upstream key from the request, supporting both field names.
func (r *updateKeyRequest) upstreamKeyValue() *string {
	if r.UpstreamKey != nil {
		return r.UpstreamKey
	}
	return r.GlmKey
}

type keyResponse struct {
	ID                  int64              `json:"id"`
	Key                 string             `json:"key"`
	Name                string             `json:"name"`
	Model               string             `json:"model"`
	UpstreamKey         string             `json:"upstream_key,omitempty"`
	TokenLimitPer5h     int                `json:"token_limit_per_5h"`
	ExpiryDate          string             `json:"expiry_date"`
	CreatedAt           string             `json:"created_at"`
	LastUsed            string             `json:"last_used"`
	TotalRequests       int                `json:"total_requests"`
	TotalLifetimeTokens int                `json:"total_lifetime_tokens"`
	TotalSpendUSD       float64            `json:"total_spend_usd"`
	UsageWindows        []storage.UsageWindow `json:"usage_windows,omitempty"`
}

type globalStatsResponse struct {
	TotalKeys           int     `json:"total_keys"`
	ActiveKeys          int     `json:"active_keys"`
	TotalRequests       int     `json:"total_requests"`
	TotalLifetimeTokens int     `json:"total_lifetime_tokens"`
	TotalSpendUSD       float64 `json:"total_spend_usd"`
}

// --- CRUD handlers ---

// ListKeys handles GET /admin/keys
func (h *AdminHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, key, name, model, upstream_key, token_limit_per_5h, expiry_date, created_at, last_used, total_requests, total_lifetime_tokens, total_spend_usd
		FROM api_keys ORDER BY id`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query error: "+err.Error())
		return
	}
	defer rows.Close()

	var keys []keyResponse
	for rows.Next() {
		var k keyResponse
		if err := rows.Scan(&k.ID, &k.Key, &k.Name, &k.Model, &k.UpstreamKey, &k.TokenLimitPer5h,
			&k.ExpiryDate, &k.CreatedAt, &k.LastUsed, &k.TotalRequests, &k.TotalLifetimeTokens, &k.TotalSpendUSD); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		// Mask key in list response
		k.Key = storage.MaskKey(k.Key)
		// Load usage windows
		k.UsageWindows, _ = h.loadWindows(k.ID)
		keys = append(keys, k)
	}
	if keys == nil {
		keys = []keyResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keys)
}

// CreateKey handles POST /admin/keys
func (h *AdminHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name == "" {
		writeAdminError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.ExpiryDate == "" {
		writeAdminError(w, http.StatusBadRequest, "expiry_date is required")
		return
	}
	if req.TokenLimitPer5h <= 0 {
		req.TokenLimitPer5h = 500000 // sensible default
	}

	// Validate expiry date format
	if _, err := time.Parse(time.RFC3339, req.ExpiryDate); err != nil {
		writeAdminError(w, http.StatusBadRequest, "expiry_date must be RFC3339 format (e.g. 2099-12-31T23:59:59Z)")
		return
	}

	// Generate API key
	apiKey, err := generateAPIKey()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "key generation failed")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := h.db.Exec(`
		INSERT INTO api_keys (key, name, model, upstream_key, token_limit_per_5h, expiry_date, created_at, last_used)
		VALUES (?, ?, ?, ?, ?, ?, ?, '')`,
		apiKey, req.Name, req.Model, req.upstreamKeyValue(), req.TokenLimitPer5h, req.ExpiryDate, now)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "insert failed: "+err.Error())
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "get insert ID failed: "+err.Error())
		return
	}

	resp := keyResponse{
		ID:              id,
		Key:             apiKey, // Return full key ONLY on creation
		Name:            req.Name,
		Model:           req.Model,
		UpstreamKey:     req.upstreamKeyValue(),
		TokenLimitPer5h: req.TokenLimitPer5h,
		ExpiryDate:      req.ExpiryDate,
		CreatedAt:       now,
		LastUsed:        "",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// GetKey handles GET /admin/keys/{id}
func (h *AdminHandler) GetKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid key ID")
		return
	}

	k, ok := h.fetchKey(id)
	if !ok {
		writeAdminError(w, http.StatusNotFound, "key not found")
		return
	}
	k.Key = storage.MaskKey(k.Key)
	k.UsageWindows, _ = h.loadWindows(id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(k)
}

// UpdateKey handles PUT /admin/keys/{id}
func (h *AdminHandler) UpdateKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid key ID")
		return
	}

	// Verify key exists
	if _, ok := h.fetchKey(id); !ok {
		writeAdminError(w, http.StatusNotFound, "key not found")
		return
	}

	var req updateKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Build dynamic UPDATE
	if req.ExpiryDate != nil {
		if _, err := time.Parse(time.RFC3339, *req.ExpiryDate); err != nil {
			writeAdminError(w, http.StatusBadRequest, "expiry_date must be RFC3339 format")
			return
		}
	}

	updates := []string{}
	args := []interface{}{}
	if req.Name != nil {
		updates = append(updates, "name = ?")
		args = append(args, *req.Name)
	}
	if req.Model != nil {
		updates = append(updates, "model = ?")
		args = append(args, *req.Model)
	}
	if req.upstreamKeyValue() != nil {
		updates = append(updates, "upstream_key = ?")
		args = append(args, *req.upstreamKeyValue())
	}
	if req.TokenLimitPer5h != nil {
		updates = append(updates, "token_limit_per_5h = ?")
		args = append(args, *req.TokenLimitPer5h)
	}
	if req.ExpiryDate != nil {
		updates = append(updates, "expiry_date = ?")
		args = append(args, *req.ExpiryDate)
	}

	if len(updates) == 0 {
		writeAdminError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	args = append(args, id)
	query := "UPDATE api_keys SET " + joinStr(updates, ", ") + " WHERE id = ?"
	if _, err := h.db.Exec(query, args...); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "update failed: "+err.Error())
		return
	}

	k, ok := h.fetchKey(id)
	if !ok {
		writeAdminError(w, http.StatusInternalServerError, "key disappeared after update")
		return
	}
	k.Key = storage.MaskKey(k.Key)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(k)
}

// DeleteKey handles DELETE /admin/keys/{id}
func (h *AdminHandler) DeleteKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid key ID")
		return
	}

	result, err := h.db.Exec("DELETE FROM api_keys WHERE id = ?", id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "delete failed: "+err.Error())
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeAdminError(w, http.StatusNotFound, "key not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// GlobalStats handles GET /admin/stats
func (h *AdminHandler) GlobalStats(w http.ResponseWriter, r *http.Request) {
	var totalKeys, activeKeys, totalReqs, totalTokens int
	var totalSpend float64

	now := time.Now().UTC().Format(time.RFC3339)

	h.db.QueryRow("SELECT COUNT(*) FROM api_keys").Scan(&totalKeys)
	h.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE expiry_date > ?", now).Scan(&activeKeys)
	h.db.QueryRow("SELECT COALESCE(SUM(total_requests), 0) FROM api_keys").Scan(&totalReqs)
	h.db.QueryRow("SELECT COALESCE(SUM(total_lifetime_tokens), 0) FROM api_keys").Scan(&totalTokens)
	h.db.QueryRow("SELECT COALESCE(SUM(total_spend_usd), 0) FROM api_keys").Scan(&totalSpend)

	resp := globalStatsResponse{
		TotalKeys:           totalKeys,
		ActiveKeys:          activeKeys,
		TotalRequests:       totalReqs,
		TotalLifetimeTokens: totalTokens,
		TotalSpendUSD:       totalSpend,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Regenerate key ---

// RegenerateKey handles POST /admin/keys/{id}/regenerate
func (h *AdminHandler) RegenerateKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid key ID")
		return
	}

	newKey, err := generateAPIKey()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "key generation failed")
		return
	}

	result, err := h.db.Exec("UPDATE api_keys SET key = ? WHERE id = ?", newKey, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "update failed: "+err.Error())
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeAdminError(w, http.StatusNotFound, "key not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": newKey})
}

// --- Helpers ---

func (h *AdminHandler) fetchKey(id int64) (keyResponse, bool) {
	var k keyResponse
	err := h.db.QueryRow(`
		SELECT id, key, name, model, upstream_key, token_limit_per_5h, expiry_date, created_at, last_used, total_requests, total_lifetime_tokens, total_spend_usd
		FROM api_keys WHERE id = ?`, id).Scan(
		&k.ID, &k.Key, &k.Name, &k.Model, &k.UpstreamKey, &k.TokenLimitPer5h,
		&k.ExpiryDate, &k.CreatedAt, &k.LastUsed, &k.TotalRequests, &k.TotalLifetimeTokens, &k.TotalSpendUSD)
	if err != nil {
		return k, false
	}
	return k, true
}

func (h *AdminHandler) loadWindows(keyID int64) ([]storage.UsageWindow, error) {
	rows, err := h.db.Query(`
		SELECT window_start, tokens_used, requests, cached_tokens, spend_usd
		FROM usage_windows WHERE api_key_id = ? ORDER BY window_start`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []storage.UsageWindow
	for rows.Next() {
		var w storage.UsageWindow
		if err := rows.Scan(&w.WindowStart, &w.TokensUsed, &w.Requests, &w.CachedTokens, &w.SpendUSD); err != nil {
			return nil, err
		}
		windows = append(windows, w)
	}
	return windows, rows.Err()
}

func writeAdminError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func generateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pk_" + hex.EncodeToString(b), nil
}

func joinStr(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
