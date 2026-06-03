package modelmap

import "testing"

func TestDefaultMapping(t *testing.T) {
	mm := New("") // empty env = use defaults

	if !mm.Enabled() {
		t.Fatal("expected mapping to be enabled by default")
	}

	tests := []struct {
		input    string
		expected string
	}{
		// Opus tier → glm-5.1
		{"claude-opus-4-20250514", "glm-5.1"},
		{"claude-3-opus-20240229", "glm-5.1"},
		{"claude-opus-4-0-1-20250514", "glm-5.1"}, // any opus variant

		// Sonnet tier → glm-5-turbo
		{"claude-sonnet-4-20250514", "glm-5-turbo"},
		{"claude-sonnet-4-5-20250514", "glm-5-turbo"},
		{"claude-3-5-sonnet-20241022", "glm-5-turbo"},
		{"claude-3-5-sonnet-latest", "glm-5-turbo"},
		{"claude-sonnet-4-0-20250514", "glm-5-turbo"},

		// Haiku tier → glm-4.5-air
		{"claude-3-5-haiku-20241022", "glm-4.5-air"},
		{"claude-3-haiku-20240307", "glm-4.5-air"},
		{"claude-3-5-haiku-latest", "glm-4.5-air"},

		// GLM models — pass through (no mapping needed)
		{"glm-5-turbo", "glm-5-turbo"},
		{"glm-5.1", "glm-5.1"},
		{"glm-4.7", "glm-4.7"},

		// Unknown models — pass through
		{"gpt-4o", "gpt-4o"},
		{"unknown-model", "unknown-model"},

		// Empty — pass through
		{"", ""},
	}

	for _, tt := range tests {
		got := mm.Resolve(tt.input)
		if got != tt.expected {
			t.Errorf("Resolve(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDisabled(t *testing.T) {
	mm := New("off")
	if mm.Enabled() {
		t.Fatal("expected mapping to be disabled with 'off'")
	}

	mm = New("none")
	if mm.Enabled() {
		t.Fatal("expected mapping to be disabled with 'none'")
	}

	// Should pass through even Claude models when disabled
	if got := mm.Resolve("claude-sonnet-4-20250514"); got != "claude-sonnet-4-20250514" {
		t.Errorf("Resolve(%q) = %q, want unchanged", "claude-sonnet-4-20250514", got)
	}
}

func TestEnvOverride(t *testing.T) {
	// Override sonnet tier to glm-4.7
	mm := New("sonnet:glm-4.7")
	if got := mm.Resolve("claude-sonnet-4-20250514"); got != "glm-4.7" {
		t.Errorf("tier override failed: got %q, want %q", got, "glm-4.7")
	}
	// Other tiers should keep defaults
	if got := mm.Resolve("claude-opus-4-20250514"); got != "glm-5.1" {
		t.Errorf("non-overridden tier failed: got %q, want %q", got, "glm-5.1")
	}

	// Specific model override
	mm = New("claude-sonnet-4-20250514:glm-4.7")
	if got := mm.Resolve("claude-sonnet-4-20250514"); got != "glm-4.7" {
		t.Errorf("specific override failed: got %q, want %q", got, "glm-4.7")
	}
	// Other sonnet variants should still match tier
	if got := mm.Resolve("claude-3-5-sonnet-20241022"); got != "glm-5-turbo" {
		t.Errorf("non-overridden specific should still match tier: got %q, want %q", got, "glm-5-turbo")
	}
}

func TestSpecificOverridesTier(t *testing.T) {
	// Specific mapping should take priority over tier
	mm := New("claude-opus-4-20250514:glm-4.6")
	if got := mm.Resolve("claude-opus-4-20250514"); got != "glm-4.6" {
		t.Errorf("specific should override tier: got %q, want %q", got, "glm-4.6")
	}
	// Other opus models still match tier
	if got := mm.Resolve("claude-3-opus-20240229"); got != "glm-5.1" {
		t.Errorf("other opus should match tier: got %q, want %q", got, "glm-5.1")
	}
}

func TestEntries(t *testing.T) {
	mm := New("")
	entries := mm.Entries()

	if len(entries) != 3 {
		t.Errorf("expected 3 default entries, got %d", len(entries))
	}
	if entries["*opus*"] != "glm-5.1" {
		t.Errorf("expected *opus* → glm-5.1, got %v", entries["*opus*"])
	}
	if entries["*sonnet*"] != "glm-5-turbo" {
		t.Errorf("expected *sonnet* → glm-5-turbo, got %v", entries["*sonnet*"])
	}
	if entries["*haiku*"] != "glm-4.5-air" {
		t.Errorf("expected *haiku* → glm-4.5-air, got %v", entries["*haiku*"])
	}
}

func TestCaseInsensitive(t *testing.T) {
	mm := New("")

	// Tier matching is case-insensitive
	tests := []struct {
		input    string
		expected string
	}{
		{"Claude-Opus-4-20250514", "glm-5.1"},
		{"CLAUDE-SONNET-4-20250514", "glm-5-turbo"},
		{"claude-3-5-HAIKU-20241022", "glm-4.5-air"},
	}

	for _, tt := range tests {
		got := mm.Resolve(tt.input)
		if got != tt.expected {
			t.Errorf("Resolve(%q) = %q, want %q (case insensitive)", tt.input, got, tt.expected)
		}
	}
}

func TestMalformedEnv(t *testing.T) {
	// Malformed entries should be silently skipped
	mm := New("no-colon,opus:glm-5.1,:empty-key,empty-value:, ,")
	if got := mm.Resolve("claude-opus-4-20250514"); got != "glm-5.1" {
		t.Errorf("malformed env should not break valid entries: got %q", got)
	}
	// Defaults should still be loaded
	if got := mm.Resolve("claude-sonnet-4-20250514"); got != "glm-5-turbo" {
		t.Errorf("defaults should be preserved: got %q", got)
	}
}
