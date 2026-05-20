# GLM Proxy (Go)

Go rewrite of the GLM Proxy API Gateway. Proxies requests to Z.AI (GLM models) with rate limiting, multi-user key management, **Admin API**, and **true SSE streaming**.

## Storage: SQLite

Since v0.2.0, storage uses **SQLite** (via [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) — pure Go, zero CGO):

- **WAL mode** — concurrent read safety without blocking
- **Auto-migration** — existing `apikeys.json` auto-detected and imported on first boot
- **Zero downtime** — no manual migration steps, JSON → SQLite happens transparently

| | TypeScript (Bun) | Go |
|---|---|---|
| Docker image | ~300MB | **~15MB** |
| Runtime RAM | ~200MB+ | **15-30MB** |
| SSE streaming | Broken (buffers full response) | **True chunked streaming** |
| Token tracking | Broken for streaming | **Works in streaming** |
| Storage | JSON file, read-every-request | **SQLite WAL, concurrent-safe** |
| Key management | Edit JSON manually | **Admin CRUD API** |

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) + Docker Compose
- Z.AI API key (dari [Z.AI dashboard](https://open.bigmodel.cn/))

### Step 1: Setup Environment

```bash
cp .env.example .env
```

Buka `.env` dan isi:

```env
# WAJIB: Master API key dari Z.AI
ZAI_API_KEY=sk-your-zai-key

# WAJIB: Admin API key (generate: openssl rand -hex 32)
ADMIN_API_KEY=your-random-secret

# Opsional
DEFAULT_MODEL=glm-4.7
PORT=3000
```

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
  -H "Authorization: Bearer your-random-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "User 1",
    "token_limit_per_5h": 100000,
    "expiry_date": "2099-12-31T23:59:59Z"
  }'
# Response: {"id":1,"key":"pk_a1b2c3...","name":"User 1",...}
```

> **Simpan key-nya!** Full key hanya ditampilkan sekali saat creation.

### Local Development (tanpa Docker)

```bash
# Build
CGO_ENABLED=0 go build -o bin/server ./cmd/server

# Jalankan
ZAI_API_KEY=sk-xxx ADMIN_API_KEY=secret ./bin/server

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
| GET | `/stats` | Yes | Token usage stats per key |
| POST | `/v1/messages` | Yes | Anthropic-compatible |
| ALL | `/v1/*` | Yes | OpenAI-compatible |

### Admin API (`/admin/*`)

All admin endpoints require `ADMIN_API_KEY` via `Authorization: Bearer` or `x-api-key` header.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/stats` | Global stats (total keys, active, tokens) |
| GET | `/admin/keys` | List all API keys (keys masked) |
| POST | `/admin/keys` | Create new API key |
| GET | `/admin/keys/{id}` | Get single key detail |
| PUT | `/admin/keys/{id}` | Update key fields |
| DELETE | `/admin/keys/{id}` | Delete key + cascade windows |
| POST | `/admin/keys/{id}/regenerate` | Regenerate key value |

## Admin API Reference

### Authentication

```bash
# Option 1: Bearer token
Authorization: Bearer your-admin-secret

# Option 2: x-api-key header
x-api-key: your-admin-secret
```

If `ADMIN_API_KEY` env is not set, all `/admin/*` endpoints return **403**.

### GET /admin/stats

Global dashboard stats.

```bash
curl http://localhost:3000/admin/stats \
  -H "Authorization: Bearer your-admin-secret"
```

```json
{
  "total_keys": 5,
  "active_keys": 4,
  "total_requests": 1234,
  "total_lifetime_tokens": 567890
}
```

### POST /admin/keys

Create a new API key. Full key returned **only once**.

```bash
curl -X POST http://localhost:3000/admin/keys \
  -H "Authorization: Bearer your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "New User",
    "model": "glm-4.7",
    "glm_key": "sk-user-own-key",
    "token_limit_per_5h": 500000,
    "expiry_date": "2099-12-31T23:59:59Z"
  }'
```

**Request fields:**

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `name` | ✅ | — | Display name |
| `model` | — | (from env) | Model override for this key |
| `glm_key` | — | — | Per-key upstream Z.AI key (bypasses master key) |
| `token_limit_per_5h` | — | `500000` | Token quota per rolling 5h window |
| `expiry_date` | ✅ | — | RFC3339 expiry date |

**Response (201):**

```json
{
  "id": 1,
  "key": "pk_a1b2c3d4e5f6...",
  "name": "New User",
  "model": "glm-4.7",
  "token_limit_per_5h": 500000,
  "expiry_date": "2099-12-31T23:59:59Z",
  "created_at": "2026-05-20T16:00:00Z"
}
```

### GET /admin/keys

List all keys. Key values are masked (`pk_a1...c3d`).

```bash
curl http://localhost:3000/admin/keys \
  -H "Authorization: Bearer your-admin-secret"
```

### GET /admin/keys/{id}

Get single key detail with usage windows.

```bash
curl http://localhost:3000/admin/keys/1 \
  -H "Authorization: Bearer your-admin-secret"
```

### PUT /admin/keys/{id}

Update specific fields (partial update). Only provided fields are changed.

```bash
curl -X PUT http://localhost:3000/admin/keys/1 \
  -H "Authorization: Bearer your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"name": "Updated Name", "token_limit_per_5h": 200000}'
```

### DELETE /admin/keys/{id}

Delete key and all associated usage windows (cascade).

```bash
curl -X DELETE http://localhost:3000/admin/keys/1 \
  -H "Authorization: Bearer your-admin-secret"
# 204 No Content
```

### POST /admin/keys/{id}/regenerate

Generate new key value for existing key. Old key immediately invalidated.

```bash
curl -X POST http://localhost:3000/admin/keys/1/regenerate \
  -H "Authorization: Bearer your-admin-secret"
```

```json
{"key": "pk_new_random_hex_48_chars"}
```

## Authentication

Two methods for proxy endpoints:

```bash
# Option 1: Bearer token
Authorization: Bearer pk_your_key

# Option 2: x-api-key header
x-api-key: pk_your_key
```

## Usage Examples

### Health Check

```bash
curl http://localhost:3000/health
# {"status":"ok","timestamp":"2026-05-20T16:00:00Z"}
```

### Check Quota

```bash
curl -H "Authorization: Bearer pk_your_key" http://localhost:3000/stats
```

### OpenAI-Compatible (Non-Streaming)

```bash
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer pk_your_key" \
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
  -H "Authorization: Bearer pk_your_key" \
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
  -H "Authorization: Bearer pk_your_key" \
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
    api_key='pk_your_key',
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
  apiKey: 'pk_your_key',
  baseURL: 'http://localhost:3000',
});

const msg = await anthropic.messages.create({
  model: 'glm-4.7',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'Hello, GLM Proxy!' }],
});
console.log(msg.content);
```

## Key Management

### Via Admin API (Recommended)

All CRUD operations through `/admin/keys` endpoints. No manual file editing needed.

### Via JSON (Legacy / Migration)

Existing `apikeys.json` auto-migrates to SQLite on first boot:

1. Server starts, detects `apikeys.json` exists
2. Imports all keys + usage windows into SQLite
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

### Key Fields

| Field | Description |
|-------|-------------|
| `key` | Unique API key (`pk_` + 48 hex chars, auto-generated) |
| `name` | Display name |
| `model` | Model override (falls back to `DEFAULT_MODEL` → `glm-4.7`) |
| `glm_key` | Per-key upstream Z.AI key (bypasses master `ZAI_API_KEY`) |
| `token_limit_per_5h` | Token quota per rolling 5-hour window |
| `expiry_date` | RFC3339 expiry date |
| `created_at` | Auto-set on creation (RFC3339) |
| `last_used` | Auto-updated on each request |
| `total_requests` | Cumulative request count |
| `total_lifetime_tokens` | Cumulative token count |

### Upstream Key Priority

```
User request
    │
    ├─ Key has glm_key? ──Yes──→ use key's glm_key
    │
    └─ No glm_key ────────────→ use ZAI_API_KEY from env
```

## Environment Variables

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `ZAI_API_KEY` | — | **Yes** | Master upstream API key from Z.AI |
| `ADMIN_API_KEY` | — | **Yes** (for Admin API) | Secret key for `/admin/*` endpoints |
| `DEFAULT_MODEL` | `glm-4.7` | No | Fallback model if per-key model is empty |
| `ALLOWED_MODELS` | (all) | No | Comma-separated model allowlist |
| `PORT` | `3000` | No | Server port |
| `DATA_FILE` | `data/apikeys.json` | No | Data file path (SQLite DB auto-created alongside) |

### Where to get ZAI_API_KEY

1. Open [Z.AI Open Platform](https://open.bigmodel.cn/)
2. Login / register
3. Go to **API Keys** / **Console**
4. Create new key
5. Copy key (format: `sk-xxxx...`)

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
    config/config.go              # Env parsing (ZAI_API_KEY, ADMIN_API_KEY, etc.)
    storage/
      types.go                    # ApiKey, UsageWindow, RateLimitInfo, StatsResponse
      sqlite.go                   # SQLite KeyStore (WAL, JSON migration, CRUD)
    ratelimit/ratelimit.go        # Rolling 5h window rate limiter
    proxy/
      types.go                    # Model resolution, header forwarding
      openai.go                   # Proxy to api.z.ai (OpenAI-compatible)
      anthropic.go                # Proxy to open.bigmodel.cn (Anthropic-compatible)
      sse.go                      # True chunked SSE streaming + token extraction
      relay.go                    # Non-streaming response relay
    middleware/
      context.go                  # Context key helpers
      auth.go                     # Auth (Bearer/x-api-key)
      ratelimit.go                # 429 middleware
    handler/
      router.go                   # Chi router + CORS + admin auth middleware
      health.go                   # GET /health, GET /
      stats.go                    # GET /stats (per-key)
      admin.go                    # /admin/* CRUD (keys, stats, regenerate)
      openai.go                   # /v1/* handler
      anthropic.go                # /v1/messages handler
  Dockerfile                      # Multi-stage: golang:1.25-alpine → scratch
  docker-compose.yml              # Dev: build lokal + Traefik labels
  docker-compose.prod.yml         # Prod: pull from GHCR + Traefik labels
  docker-compose.local.yml        # Local: tanpa domain/Traefik
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

Push ke `main` otomatis build dan push Docker image ke GitHub Container Registry.

### Setup Secrets

Di repo GitHub: **Settings > Secrets and variables > Actions > New repository secret**

| Secret | Value |
|--------|-------|
| `DOCKER_BASEURL` | `ghcr.io` |
| `DOCKER_USERNAME` | GitHub username |
| `DOCKER_PASSWORD` | GitHub PAT (classic, scope: `write:packages`) |

### Tagging

| Trigger | Image Tag |
|---------|-----------|
| Push ke `main` | `main`, `sha-abc1234` |
| Tag `v1.0.0` | `1.0.0`, `1.0`, `latest` |
| Pull request | Build only, no push |

## Deploy ke Server (Minimal Files)

Server hanya butuh **3 file**, tidak butuh source code:

```
~/serviceku/glm-proxy-golang/
  .env                    # ZAI_API_KEY, ADMIN_API_KEY, DOMAIN
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
ZAI_API_KEY=sk-xxx
ADMIN_API_KEY=$(openssl rand -hex 32)
DEFAULT_MODEL=glm-4.7
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

| Model | Description | Context | Max Output |
|-------|-------------|---------|------------|
| glm-4.7 | High-intelligence flagship | 200K | 96K |
| glm-4.5-air | High cost-performance | 128K | 96K |
| glm-4.5-flash | Free model | 128K | 96K |

## Error Codes

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Invalid request body |
| 401 | Missing/invalid API key |
| 403 | Key expired / Admin API not configured |
| 404 | Key/resource not found |
| 429 | Token quota exceeded |
| 502 | Upstream (Z.AI) error |

## Troubleshooting

```bash
# Container won't start
docker compose logs -f

# Rebuild
docker compose up --build -d

# Port conflict
PORT=3001 docker compose up -d

# Test upstream Z.AI key
curl -H "Authorization: Bearer sk-xxx" https://api.z.ai/api/coding/paas/v4/models

# Admin API returns 403
# → ADMIN_API_KEY env not set. Check .env file.

# Database corrupted
# → Delete data/apikeys.db* and restart (data loss if no backup).
# → Or restore from data/apikeys.json.migrated backup.
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
