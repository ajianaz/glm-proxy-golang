package litellm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Client wraps LiteLLM admin API calls.
type Client struct {
	BaseURL   string // e.g. http://litellm:4000
	MasterKey string // MASTER_KEY env var
	HTTP      *http.Client
}

// NewClient creates a LiteLLM admin client.
func NewClient(baseURL, masterKey string) *Client {
	return &Client{
		BaseURL:   baseURL,
		MasterKey: masterKey,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// TeamResponse is the response from POST /team/new
type TeamResponse struct {
	TeamID    string `json:"team_id"`
	TeamAlias string `json:"team_alias"`
}

// EnsureTeam creates a team if it doesn't exist (idempotent).
// Returns the team_id.
func (c *Client) EnsureTeam(alias string) (string, error) {
	body := map[string]string{"team_alias": alias}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", c.BaseURL+"/team/new", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("create team request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.MasterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("ensure team request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", fmt.Errorf("ensure team returned %d: %s", resp.StatusCode, string(respBody))
	}

	var teamResp TeamResponse
	if err := json.Unmarshal(respBody, &teamResp); err != nil {
		return "", fmt.Errorf("parse team response: %w (body: %s)", err, string(respBody))
	}

	log.Printf("[litellm] ensured team: %s (id: %s)", alias, teamResp.TeamID)
	return teamResp.TeamID, nil
}

// KeyResponse is the response from POST /key/generate
type KeyResponse struct {
	Key      string `json:"key"`
	KeyAlias string `json:"key_alias"`
	Expiry   string `json:"expiry"`
}

// GenerateKey creates a virtual key in LiteLLM assigned to the given team.
// Returns the generated key (sk-xxx format).
func (c *Client) GenerateKey(teamID, alias string) (string, error) {
	body := map[string]string{
		"team_id":   teamID,
		"key_alias": alias,
	}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", c.BaseURL+"/key/generate", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("create key request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.MasterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("generate key request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", fmt.Errorf("generate key returned %d: %s", resp.StatusCode, string(respBody))
	}

	var keyResp KeyResponse
	if err := json.Unmarshal(respBody, &keyResp); err != nil {
		return "", fmt.Errorf("parse key response: %w (body: %s)", err, string(respBody))
	}

	log.Printf("[litellm] generated key for team %s: sk-...%s", teamID, keyResp.Key[len(keyResp.Key)-8:])
	return keyResp.Key, nil
}
