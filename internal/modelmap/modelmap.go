// Package modelmap provides server-side model name mapping.
// It allows clients to send Claude model names (e.g. claude-sonnet-4-20250514)
// and transparently maps them to GLM models (e.g. glm-5-turbo).
//
// Mapping resolution order:
//  1. Specific model mapping (exact match)
//  2. Tier-based mapping (pattern match: opus, sonnet, haiku)
//  3. No mapping (pass through original model name)
package modelmap

import (
	"strings"
)

// Tier constants for default mapping (matching Z.AI Claude Code defaults).
const (
	TierOpus   = "opus"
	TierSonnet = "sonnet"
	TierHaiku  = "haiku"
)

// DefaultMapping is the built-in mapping table matching Z.AI's recommended settings:
//   - opus   → glm-5.1
//   - sonnet → glm-5.1 (Claude Code CLI rejects sonnet responses from third-party providers;
//              mapping to glm-5.1 (same as opus) works around this limitation)
//   - haiku  → glm-4.5-air
var DefaultMapping = map[string]string{
	TierOpus:   "glm-5.1",
	TierSonnet: "glm-5.1",
	TierHaiku:  "glm-4.5-air",
}

// ModelMap holds the resolved model mapping configuration.
// It supports two levels of mapping:
//   - specific: exact model name → target (e.g. "claude-opus-4-20250514" → "glm-5.1")
//   - tier:     pattern-based → target (e.g. any "*opus*" → "glm-5.1")
type ModelMap struct {
	specific map[string]string // exact model name → target model
	tier     map[string]string // tier name → target model (opus, sonnet, haiku)
	enabled  bool
}

// New creates a ModelMap from an optional env var override.
// envFormat: "claude-opus-4-20250514:glm-5.1,sonnet:glm-5-turbo"
//
// Special values:
//   - "" (empty) → use built-in default mapping (enabled)
//   - "off" or "none" → disable all mapping
//   - comma-separated "key:value" pairs → override defaults
//
// Tier keys (opus, sonnet, haiku) override the corresponding tier's default target.
// Non-tier keys are treated as specific model name mappings.
func New(envValue string) *ModelMap {
	mm := &ModelMap{
		specific: make(map[string]string),
		tier:     make(map[string]string),
		enabled:  true,
	}

	if strings.EqualFold(envValue, "off") || strings.EqualFold(envValue, "none") {
		mm.enabled = false
		return mm
	}

	// Start with built-in defaults for tiers
	for k, v := range DefaultMapping {
		mm.tier[k] = v
	}

	if envValue == "" {
		return mm // use defaults only
	}

	// Parse env overrides
	for _, pair := range strings.Split(envValue, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}

		// Check if key is a tier name
		switch strings.ToLower(key) {
		case TierOpus, TierSonnet, TierHaiku:
			mm.tier[strings.ToLower(key)] = value
		default:
			// Specific model name mapping
			mm.specific[key] = value
		}
	}

	return mm
}

// Resolve maps a client-provided model name to the target model.
// Returns the original model if no mapping matches.
func (mm *ModelMap) Resolve(model string) string {
	if !mm.enabled || model == "" {
		return model
	}

	// 1. Specific mapping (exact match)
	if target, ok := mm.specific[model]; ok {
		return target
	}

	// 2. Tier-based mapping (case-insensitive pattern match)
	lower := strings.ToLower(model)
	for tier, target := range mm.tier {
		if strings.Contains(lower, tier) {
			return target
		}
	}

	// 3. No mapping — pass through
	return model
}

// Enabled returns whether model mapping is active.
func (mm *ModelMap) Enabled() bool {
	return mm.enabled
}

// Entries returns the full mapping table for display/debugging.
// Returns a map of source → target for all specific + tier entries.
func (mm *ModelMap) Entries() map[string]string {
	result := make(map[string]string)
	for k, v := range mm.specific {
		result[k] = v
	}
	// Expand tiers into readable entries
	for tier, target := range mm.tier {
		result["*"+tier+"*"] = target
	}
	return result
}

// ClientModels returns all client-facing model names that the proxy accepts.
// These are the aliases that will be mapped to upstream models.
// Claude Code CLI calls GET /v1/models to validate model names before making requests.
func (mm *ModelMap) ClientModels() []string {
	if !mm.enabled {
		return nil
	}
	models := make([]string, 0, len(mm.specific)+len(mm.tier))
	for k := range mm.specific {
		models = append(models, k)
	}
	// Add well-known Claude model names for each tier
	for tier := range mm.tier {
		switch tier {
		case TierOpus:
			models = append(models, "claude-opus-4-20250514", "claude-opus-4-0")
		case TierSonnet:
			models = append(models, "claude-sonnet-4-20250514", "claude-sonnet-4-0", "claude-sonnet-4-6")
		case TierHaiku:
			models = append(models, "claude-haiku-3-5-20241022", "claude-haiku-3-5", "claude-3-5-haiku-20241022")
		}
	}
	return models
}
