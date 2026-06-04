# Changelog

## v0.1.1 — 2026-06-04

### Features
- SQLite storage + Admin API for key management
- Per-key configurable upstream + API key + cost tracking
- Dual-track CI (develop/main) with version injection via ldflags
- LiteLLM team management + auto virtual key generation
- ENV_MODE for LiteLLM team/key suffix isolation (dev vs prod)
- Server-side model mapping (Claude → GLM)
- Client-facing model aliases in `/v1/models` endpoint

### Fixes
- Admin API key masking — `RegenerateKey` returns full key response
- Upstream key must be `sk-*` format with fallback to `MASTER_KEY`
- Auto-generated key prefix changed from `pk-` to `sk-`
- Anthropic non-streaming endpoint — force stream + buffer + convert
- Deduplicate `message_start` + strip model prefix in Anthropic SSE streaming
- Inject estimated token counts in Anthropic SSE for Claude Code CLI compatibility
- Strip extended thinking params from request body for Claude Code CLI compat
- Extract system messages from `messages[]` for Claude Code v2.1.154+ compat
- Replace response model with client-requested model in SSE stream
- Apply model replacement and token estimation to non-streaming path
- Synthesize thinking blocks for Claude Code CLI compat (later disabled)
- Proper SSE thinking block injection with index management
- Set thinking to `disabled` instead of stripping from request
- Map sonnet tier to `glm-5.1` (same as opus) for Claude Code CLI compat
- Replace sonnet model name with opus in response for Claude Code CLI compat
- CI: remove paths filter from push (merge commits not triggering builds)

## v0.1.0 — 2025-06-01

Initial release.
