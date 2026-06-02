# GLM Proxy (Go)

Go rewrite of the GLM Proxy API Gateway. Proxies requests to upstream LLM providers (Z.AI, OpenAI, Anthropic, etc.) via [LiteLLM](https://docs.litellm.ai/) with rate limiting, multi-user key management, **per-key upstream routing**, **cost tracking**, **Admin API**, and **true SSE streaming**.

## Storage: SQLite

Storage uses **SQLite** (via [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) — pure Go, zero CGO):

- **WAL mode** — concurrent read safety without blocking
- **Auto-migration** — existing `apikeys.json` auto-detected and imported on first boot
- **V2 schema** — `upstream_key`, `total_spend_usd`, `spend_usd` columns added via safe ALTER TABLE
- **Zero downtime** — no manual migration steps, JSON → SQLite happens transparently

| | TypeScript (Bun) | Go |
|---|---|---|
| Docker image | ~300MB | **~15MB** |
| Runtime RAM | ~200MB+ | **15-30MB** |
| SSE streaming | Broken (buffers full response) | **True chunked streaming** |
| Token tracking | Broken for streaming | **Works in streaming** |
| Storage | JSON file, read-every-request | **SQLite WAL, concurrent-safe** |
| Key management | Edit JSON manually | **Admin CRUD API** |
| Upstream | Hardcoded Z.AI | **Configurable (LiteLLM)** |
| Cost tracking | ❌ | **✅ Per-key window + lifetime** |

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) + Docker Compose
- LiteLLM proxy running (or any OpenAI/Anthropic-compatible endpoint)
- Upstream API key (LiteLLM virtual key, OpenAI, Z.AI, etc.)

### Step 1: Setup Environment

```bash
cp .env.example .env
```

Buka `.env` dan isi:

```env
# WAJIB: Master upstream API key (LiteLLM virtual key / provider key)
MASTER_KEY=sk-litellm-***

# WAJIB: Admin API key (generate: openssl rand -hex 32)
ADMIN_API_KEY=your-r...cret

# Upstream endpoints (default: LiteLLM proxy)
OPENAI_UPSTREAM=http://litellm:4000
ANTHROPIC_UPSTREAM=http://litellm:4000

# Opsional
DEFAULT_MODEL=glm-4.7
PORT=3000
```

> **Migration note:** `ZAI_API_KEY` still works (fallback). Rename to `MASTER_KEY` for clarity.

### Step 2: Jalankan

```bash
mkdir -p data
docker compose up -d

# Verify
curl http://localhost:3000/health
# {"status":"ok","timestamp":"..."}
```

### Step 3: Buat API Key via Admin API

```bash
# Buat key baru
curl -X POST http://localhost:3000/admin/keys \
  -H "Authorization: Bearer your-r...ret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "User 1",
    "token_limit_per_5h": 100000,
    "expiry_date": "2099-12-31T23:59:59Z"
  }'
# Response: {"id":1,"key":"sk_a1b2c3...","name":"User 1",...}
```

> **Simpan key-nya!** Full key hanya ditampilkan sekali saat creation.

### Per-Key Configuration

Setiap API key bisa punya **upstream key sendiri** dan **default model sendiri**:

```bash
curl -X POST http://localhost:3000/admin/keys \
  -H "Authorization: Bearer your-r...ret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Premium User",
    "model": "claude-3-5-sonnet-20241022",
    "upstream_key": "sk-user-litellm-key",
    "token_limit_per_5h": 500000,
    "expiry_date": "2099-12-31T23:59:59Z"
  }'
```

### Local Development (tanpa Docker)

```bash
# Build
CGO_ENABLED=0 go build -o bin/server ./cmd/server

# Jalankan
MASTER_KEY=sk-*** ADMIN_API_KEY=*** ./bin/server

# Run tests
CGO_ENABLED=0 go test ./... -v -race
```

## File Structure

```
glm-proxy-golang/
  .env.example                  # Konfigurasi env (copy → .env)
  docker-compose.yml            # Dev: build lokal + Traefik
  docker-compose.prod.yml       # Prod: pull image + Traefik
  docker-compose.local.yml      # Local: tanpa domain/Traefik
  data/                         # Volume mount ke /app/data
    apikeys.json                # Legacy (jika ada, auto-migrate → .migrated)
    apikeys.db                  # SQLite database (auto-created)
    apikeys.db-wal              # SQLite WAL file (auto-created)
    apikeys.db-shm              # SQLite shared memory (auto-created)
  Dockerfile                    # Multi-stage: golang:1.25-alpine → scratch
```

### Docker Volume

Volume `./data:/app/data:rw` sudah mencakup semua file yang dibutuhkan:

| File | Auto-created? | Description |
|------|---------------|-------------|
| `apikeys.db` | ✅ | SQLite database (main) |
| `apikeys.db-wal` | ✅ | WAL journal (concurrent read safety) |
| `apikeys.db-shm` | ✅ | Shared memory index |
| `apikeys.json.migrated` | ✅ | Backup JSON setelah migrasi |

## Endpoints

### Public

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/` | No | API info |
| GET | `/health` | No | Health check |

### Proxy (User-facing)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/stats` | Yes | Token usage + cost stats per key |
| POST | `/v1/messages` | Yes | Anthropic-compatible |
| ALL | `/v1/*` | Yes | OpenAI-compatible |

### Admin API (`/admin/*`)

All admin endpoints require `ADMIN_API_KEY` via `Authorization: Bearer` or `x-api-key` header.

| Method | Path | Description | Key in Response |
|--------|------|-------------|-----------------|
| GET | `/admin/stats` | Global stats (total keys, active, tokens, spend) | N/A |
| GET | `/admin/keys` | List all API keys | Masked (`sk-a1...c3d`) |
| POST | `/admin/keys` | Create new API key | **Full (unmasked)** |
| GET | `/admin/keys/{id}` | Get single key detail | Masked |
| PUT | `/admin/keys/{id}` | Update key fields | Masked |
| DELETE | `/admin/keys/{id}` | Delete key + cascade windows | N/A |
| POST | `/admin/keys/{id}/regenerate` | Regenerate key value | **Full (unmasked)** |

## Admin API Reference

### Authentication

```bash
# Option 1: Bearer token
Authorization: Bearer ***

# Option 2: x-api-key header
x-api-key: your-admin-secret
```

If `ADMIN_API_KEY` env is not set, all `/admin/*` endpoints return **403**.

### GET /admin/stats

Global dashboard stats.

```bash
curl http://localhost:3000/admin/stats \
  -H "Authorization: Bearer your-a...ret"
```

```json
{
  "total_keys": 5,
  "active_keys": 4,
  "total_requests": 1234,
  "total_lifetime_tokens": 567890,
  "total_spend_usd": 12.34
}
```

### POST /admin/keys

Create a new API key. Full key returned **only once**.

```bash
curl -X POST http://localhost:3000/admin/keys \
  -H "Authorization: Bearer your-a...ret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "New User",
    "model": "glm-4.7",
    "upstream_key": "sk-user-specific-key",
    "token_limit_per_5h": 500000,
    "expiry_date": "2099-12-31T23:59:59Z"
  }'
```

**Request fields:**

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `name` | ✅ | — | Display name |
| `model` | — | (from env) | Model override for this key |
| `upstream_key` | — | — | Per-key upstream key (bypasses `MASTER_KEY`) |
| `token_limit_per_5h` | — | `500000` | Token quota per rolling 5h window |
| `expiry_date` | ✅ | — | RFC3339 expiry date |

> **Backward compat:** JSON body juga menerima `glmkey` (legacy alias untuk `upstream_key`).

**Response (201):**

```json
{
  "id": 1,
  "key": "sk-a1b2c3d4e5f6full_key_here_51chars_total",
  "name": "New User",
  "model": "glm-4.7",
  "upstream_key": "sk-use...-key",
  "token_limit_per_5h": 500000,
  "expiry_date": "2099-12-31T23:59:59Z",
  "created_at": "2026-05-20T16:00:00Z",
  "last_used": null,
  "total_requests": 0,
  "total_lifetime_tokens": 0,
  "total_spend_usd": 0,
  "usage_windows": []
}
```

### GET /admin/keys

List all keys. Key values are masked (`sk-a1...c3d`).

```bash
curl http://localhost:3000/admin/keys \
  -H "Authorization: Bearer your-a...ret"
```

### GET /admin/keys/{id}

Get single key detail with usage windows and spend data.

```bash
curl http://localhost:3000/admin/keys/1 \
  -H "Authorization: Bearer your-a...ret"
```

```json
{
  "id": 1,
  "key": "sk-a1...c3d",
  "name": "New User",
  "model": "glm-4.7",
  "upstream_key": "sk-use...-key",
  "token_limit_per_5h": 500000,
  "expiry_date": "2099-12-31T23:59:59Z",
  "created_at": "2026-05-20T16:00:00Z",
  "last_used": "2026-05-20T14:00:00Z",
  "total_requests": 1234,
  "total_lifetime_tokens": 567890,
  "total_spend_usd": 12.34,
  "usage_windows": [
    {
      "window_start": "2026-05-20T14:00:00Z",
      "tokens_used": 12345,
      "requests": 100,
      "cached_tokens": 5000,
      "spend_usd": 1.23
    }
  ]
}
```

### PUT /admin/keys/{id}

Update specific fields (partial update). Only provided fields are changed.

```bash
curl -X PUT http://localhost:3000/admin/keys/1 \
  -H "Authorization: Bearer your-a...ret" \
  -H "Content-Type: application/json" \
  -d '{"name": "Updated Name", "token_limit_per_5h": 200000}'
```

### DELETE /admin/keys/{id}

Delete key and all associated usage windows (cascade).

```bash
curl -X DELETE http://localhost:3000/admin/keys/1 \
  -H "Authorization: Bearer your-a...ret"
# 204 No Content
```

### POST /admin/keys/{id}/regenerate

Generate new key value for existing key. Old key immediately invalidated.

```bash
curl -X POST http://localhost:3000/admin/keys/1/regenerate \
  -H "Authorization: Bearer your-a...ret"
```

```json
{
  "id": 1,
  "key": "sk_new_random_hex_48_chars_here_full_key",
  "name": "New User",
  "model": "glm-4.7",
  "upstream_key": "sk-use...-key",
  "token_limit_per_5h": 500000,
  "expiry_date": "2099-12-31T23:59:59Z",
  "created_at": "2026-05-20T16:00:00Z",
  "last_used": "2026-05-20T14:00:00Z",
  "total_requests": 1234,
  "total_lifetime_tokens": 567890,
  "total_spend_usd": 12.34,
  "usage_windows": [...]
}
```

## Authentication

Two methods for proxy endpoints:

```bash
# Option 1: Bearer token
Authorization: Bearer ***

# Option 2: x-api-key header
x-api-key: sk_your_key
```

## Usage Examples

### Health Check

```bash
curl http://localhost:3000/health
# {"status":"ok","timestamp":"2026-05-20T16:00:00Z"}
```

### Check Quota & Spend

```bash
curl -H "Authorization: Bearer ***" http://localhost:3000/stats
```

Response includes key info, token usage, remaining quota, and cost:

```json
{
  "key": "sk-a1...c3d",
  "name": "New User",
  "model": "glm-4.7",
  "is_expired": false,
  "total_lifetime_tokens": 567890,
  "total_requests": 1234,
  "total_spend_usd": 12.34,
  "current_usage": {
    "window_start": "2026-05-20T14:00:00Z",
    "tokens_used": 12345,
    "requests": 100,
    "cached_tokens": 5000,
    "spend_usd": 1.23,
    "remaining_tokens": 487655,
    "window_ends_at": "2026-05-20T19:00:00Z"
  }
}
```

### OpenAI-Compatible (Non-Streaming)

```bash
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer ***" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "glm-4.7",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'
```

### OpenAI-Compatible (Streaming)

```bash
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer ***" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "glm-4.7",
    "messages": [{"role": "user", "content": "Tell me a joke"}],
    "stream": true
  }'
```

### Anthropic-Compatible (Streaming)

```bash
curl -X POST http://localhost:3000/v1/messages \
  -H "Authorization: Bearer ***" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "glm-4.7",
    "max_tokens": 1024,
    "stream": true,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### Using with Anthropic SDK (Python)

```python
import anthropic

client = anthropic.Anthropic(
    api_key='sk_your_key',
    base_url='http://localhost:3000',
)

message = client.messages.create(
    model='glm-4.7',
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello, GLM Proxy!"}],
)
print(message.content)
```

### Using with Anthropic SDK (TypeScript)

```typescript
import Anthropic from '@anthropic-ai/sdk';

const anthropic = new Anthropic({
  apiKey: 'sk_your_key',
  baseURL: 'http://localhost:3000',
});

const msg = await anthropic.messages.create({
  model: 'glm-4.7',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'Hello, GLM Proxy!' }],
});
console.log(msg.content);
```

## Cost Tracking

GLM Proxy tracks **cost per API key** by parsing the `x-litellm-response-cost` header from upstream responses (LiteLLM proxy). No extra API calls needed.

### What's Tracked

| Metric | Scope | Description |
|--------|-------|-------------|
| `total_spend_usd` | Lifetime | Cumulative USD spend across all requests |
| `window_spend_usd` | Rolling 5h | USD spend within current rate limit window |

### How It Works

```
Client request → GLM Proxy → Upstream (LiteLLM)
                ← Response with x-litellm-response-cost header
                → Parse header → Store cost in SQLite
```

- Cost parsed from response header only — **zero extra latency**
- If header is missing (non-LiteLLM upstream), cost defaults to `0`
- Visible in `/stats`, `/admin/stats`, and `/admin/keys/{id}`

### Upstream Key Priority

```
User request
    │
    ├─ Key has upstream_key? ──Yes──→ use key's upstream_key
    │
    └─ No upstream_key ─────────────→ use MASTER_KEY from env
```

## Key Management

### Via Admin API (Recommended)

All CRUD operations through `/admin/keys` endpoints. No manual file editing needed.

### Via JSON (Legacy / Migration)

Existing `apikeys.json` auto-migrates to SQLite on first boot:

1. Server starts, detects `apikeys.json` exists
2. Imports all keys + usage windows into SQLite (including `upstream_key`, spend data)
3. Renames `apikeys.json` → `apikeys.json.migrated` (backup preserved)
4. Subsequent boots use SQLite only

```json
{
  "keys": [
    {
      "key": "pk_legacy_user",
      "name": "Legacy User",
      "model": "glm-4.7",
      "glmkey": "",
      "token_limit_per_5h": 100000,
      "expiry_date": "2099-12-31T23:59:59Z",
      "created_at": "2026-03-17T00:00:00Z",
      "last_used": "",
      "total_lifetime_tokens": 0,
      "usage_windows": []
    }
  ]
}
```

> **Note:** `glmkey` dalam JSON otomatis di-migrate ke `upstream_key` di SQLite.

### Key Fields

| Field | Description |
|-------|-------------|
| `key` | Unique API key (`sk-` + 48 hex chars = 51 chars, auto-generated) |
| `name` | Display name |
| `model` | Model override (falls back to `DEFAULT_MODEL` → `glm-4.7`) |
| `upstream_key` | Per-key upstream key (bypasses `MASTER_KEY`) |
| `token_limit_per_5h` | Token quota per rolling 5-hour window |
| `expiry_date` | RFC3339 expiry date |
| `created_at` | Auto-set on creation (RFC3339) |
| `last_used` | Auto-updated on each request |
| `total_requests` | Cumulative request count |
| `total_lifetime_tokens` | Cumulative token count |
| `total_spend_usd` | Cumulative USD spend (from LiteLLM cost header) |

## Environment Variables

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `MASTER_KEY` | — | **Yes** | Master upstream API key (LiteLLM virtual key / provider key) |
| `ADMIN_API_KEY` | — | **Yes** (for Admin API) | Secret key for `/admin/*` endpoints |
| `OPENAI_UPSTREAM` | `http://litellm:4000` | No | Upstream endpoint for OpenAI-compatible requests |
| `ANTHROPIC_UPSTREAM` | `http://litellm:4000` | No | Upstream endpoint for Anthropic-compatible requests |
| `DEFAULT_MODEL` | `glm-4.7` | No | Fallback model if per-key model is empty |
| `ALLOWED_MODELS` | (all) | No | Comma-separated model allowlist |
| `PORT` | `3000` | No | Server port |
| `DB_PATH` | `data/proxy.db` | No | SQLite database path |
| `DATA_FILE` | `data/apikeys.json` | No | Legacy JSON path (for migration) |
| `ENV_MODE` | `prod` | No | Environment mode for LiteLLM team isolation (`prod`/`dev`/`staging`) |

### Legacy Env Vars

| Old | New | Notes |
|-----|-----|-------|
| `ZAI_API_KEY` | `MASTER_KEY` | Still works as fallback |

## Rate Limiting

- **Type**: Rolling 5-hour window
- **Metric**: Total tokens across all active windows
- **Threshold**: `>` token_limit_per_5h (exactly at limit = still allowed)
- **Response**: HTTP 429 with `Retry-After` header

```json
{
  "error": {
    "message": "Token limit exceeded for current 5-hour window",
    "type": "rate_limit_exceeded",
    "tokens_used": 100500,
    "tokens_limit": 100000,
    "window_ends_at": "2026-01-01T05:00:00Z"
  }
}
```

## Architecture

```
glm-proxy-golang/
  cmd/server/main.go              # Entry point, graceful shutdown
  internal/
    config/config.go              # Env parsing (MASTER_KEY, UPSTREAM, etc.)
    storage/
      types.go                    # ApiKey, UsageWindow, RateLimitInfo, StatsResponse
      sqlite.go                   # SQLite KeyStore (WAL, JSON migration, CRUD, V2 schema)
    ratelimit/ratelimit.go        # Rolling 5h window rate limiter
    litellm/
      client.go                   # LiteLLM admin API client (team, key generation)
    proxy/
      types.go                    # Model resolution, header forwarding, cost parsing
      openai.go                   # Proxy → OPENAI_UPSTREAM (OpenAI-compatible)
      anthropic.go                # Proxy → ANTHROPIC_UPSTREAM (Anthropic-compatible)
      sse.go                      # True chunked SSE streaming + token extraction
      relay.go                    # Non-streaming response relay + cost tracking
      converter.go                # OpenAI↔Anthropic JSON converter
    middleware/
      context.go                  # Context key helpers
      auth.go                     # Auth (Bearer/x-api-key)
      ratelimit.go                # 429 middleware
    handler/
      router.go                   # Chi router + CORS + admin auth middleware
      health.go                   # GET /health, GET /
      stats.go                    # GET /stats (per-key, with spend)
      admin.go                    # /admin/* CRUD (keys, stats, regenerate, spend)
      openai.go                   # /v1/* handler
      anthropic.go                # /v1/messages handler
  test/
    integration/
      integration_test.go          # Public endpoint + CORS tests
      streaming_test.go           # SSE streaming tests
      admin_test.go               # Admin CRUD + masking tests
  Dockerfile                      # Multi-stage: golang:1.25-alpine → scratch
  docker-compose.yml              # Dev: build lokal + Traefik labels
  docker-compose.prod.yml         # Prod: pull from GHCR + Traefik labels
  docker-compose.local.yml        # Local: tanpa domain/Traefik
```

### Request Flow

```
Client
  │
  ├─ POST /v1/chat/completions (OpenAI)
  │   POST /v1/messages (Anthropic)
  │
  ▼
Auth Middleware → Rate Limit → Proxy Handler
                                  │
                                  ├─ Resolve model (per-key > env DEFAULT_MODEL)
                                  ├─ Resolve upstream key (per-key upstream_key > MASTER_KEY)
                                  │
                                  ▼
                            Upstream (LiteLLM / Provider)
                                  │
                                  ▼
                            Response (with x-litellm-response-cost header)
                                  │
                                  ├─ Extract tokens → UpdateUsage (SQLite)
                                  ├─ Parse cost → Track spend (window + lifetime)
                                  │
                                  ▼
                            Client Response
```

## Docker

```bash
# Build (dev)
docker compose up --build -d

# Pull (prod)
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d

# View logs
docker compose logs -f

# Stop
docker compose down
```

### Security Hardening

- `read_only: true` — Read-only filesystem
- `cap_drop: ALL` — Drop all Linux capabilities
- `no-new-privileges` — Prevent privilege escalation
- `memory: 128M` limit / `32M` reservation
- `scratch` base image — No shell, no package manager, minimal attack surface

### Traefik Integration

Domain dan port dikonfigurasi via `.env`:

```env
DOMAIN=glm.ajianaz.dev
PORT=3000
```

Traefik labels di `docker-compose.yml` otomatis membaca `DOMAIN` dan `PORT`.

### Volume Mount

```yaml
volumes:
  - ./data:/app/data:rw
```

Folder `./data` di-host di-mount ke `/app/data` di container. Semua file database (`.db`, `.db-wal`, `.db-shm`) dan legacy JSON (`.migrated`) ada di sini.

## CI/CD (GitHub Actions)

Dual-track CI/CD: `develop` and `main` branches + semver tags. Builds are pushed to GitHub Container Registry (GHCR) automatically.

### Setup

No custom secrets needed for pushing to GHCR. GitHub's built-in `GITHUB_TOKEN` is used automatically with `permissions: packages: write`.

Deploy to the server is handled by a separate `deploy.yml` workflow triggered via `workflow_run`, using Infisical OIDC + SSH.

### Tagging

| Trigger | Image Tags |
|---------|-----------|
| Push to `develop` | `develop`, `sha-abc1234` |
| Push to `main` | `latest`, `main`, `sha-abc1234` |
| Tag `v0.x.x` | `v0.x.x`, `0.x`, `latest` |
| Pull request | Build only, no push |

## Deploy ke Server (Minimal Files)

> **Auto-deploy:** Pushes to `main` and semver tags automatically trigger `deploy.yml` (via `workflow_run`) which deploys to the server using Infisical OIDC + SSH. The manual steps below are still available if needed.

Server hanya butuh **3 file**, tidak butuh source code:

```
~/serviceku/glm-proxy-golang/
  .env                    # MASTER_KEY, ADMIN_API_KEY, DOMAIN
  docker-compose.prod.yml # pull image dari GHCR
  data/                   # auto-created on first run
```

### First Deploy

```bash
ssh user@your-server
mkdir -p ~/serviceku/glm-proxy-golang/data

# Login GHCR (once)
echo "$GITHUB_PAT" | docker login ghcr.io -u ajianaz --password-stdin

# .env
cat > ~/serviceku/glm-proxy-golang/.env << 'EOF'
MASTER_KEY=sk-***
ADMIN_API_KEY=***$(openssl rand -hex 32)
DEFAULT_MODEL=glm-4.7
OPENAI_UPSTREAM=http://litellm:4000
ANTHROPIC_UPSTREAM=http://litellm:4000
PORT=3000
DOMAIN=glm.ajianaz.dev
DOCKER_IMAGE=ghcr.io/ajianaz/glm-proxy-go:main
EOF

# Start
cd ~/serviceku/glm-proxy-golang
docker compose -f docker-compose.prod.yml up -d
```

### Update

```bash
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

## Available Models

Depends on upstream configuration. When using LiteLLM, all models configured in LiteLLM's `config.yaml` are available. Example:

| Model | Provider | Description |
|-------|----------|-------------|
| glm-4.6v | Z.AI | Vision-capable (via LiteLLM) |
| glm-4.7 | Z.AI | Default model |
| glm-5 | Z.AI | Latest generation |
| glm-5-turbo | Z.AI | Fast variant |
| glm-5.1 | Z.AI | Latest stable |

## Error Codes

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Invalid request body |
| 401 | Missing/invalid API key |
| 403 | Key expired / Admin API not configured |
| 404 | Key/resource not found |
| 429 | Token quota exceeded |
| 502 | Upstream error |

## Troubleshooting

```bash
# Container won't start
docker compose logs -f

# Rebuild
docker compose up --build -d

# Port conflict
PORT=3001 docker compose up -d

# Test upstream connectivity
curl http://litellm:4000/health

# Admin API returns 403
# → ADMIN_API_KEY env not set. Check .env file.

# Database corrupted
# → Delete data/proxy.db* and restart (data loss if no backup).
# → Or restore from data/apikeys.json.migrated backup.

# Cost tracking shows 0
# → Upstream must return x-litellm-response-cost header.
# → LiteLLM proxy sends this automatically. Other upstreams may not.
```

## Makefile Commands

```bash
make build          # Build binary to bin/server
make test           # Run all tests with race detection
make test-coverage  # Run tests and show coverage
make docker-build   # Build Docker image
make docker-up      # docker compose up -d --build
make docker-down    # docker compose down
make check          # build + test
make clean          # Remove bin/ and coverage files
```

## License

MIT
# ci trigger
