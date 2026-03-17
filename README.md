# GLM Proxy (Go)

Go rewrite of the GLM Proxy API Gateway. Proxies requests to Z.AI (glm-4.7) with rate limiting, multi-user token management, and **true SSE streaming**.

| | TypeScript (Bun) | Go |
|---|---|---|
| Docker image | ~300MB | **~7MB** |
| Runtime RAM | ~200MB+ | **15-30MB** |
| SSE streaming | Broken (buffers full response) | **True chunked streaming** |
| Token tracking | Broken for streaming | **Works in streaming** |
| Storage | Reads file every request | **In-memory cache + periodic flush** |

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) + Docker Compose
- Z.AI API key (dari [Z.AI dashboard](https://open.bigmodel.cn/))

### Step 1: Setup Environment

```bash
# Copy env template
cp .env.example .env
```

Buka `.env` dan isi:

```env
# WAJIB: Master API key dari Z.AI (untuk semua user yang TANPA glmkey)
ZAI_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxx

# Opsional: Default model jika per-key model kosong
DEFAULT_MODEL=glm-4.7

# Opsional: Port server
PORT=3000

# Opsional: Path ke file API keys (di dalam container)
DATA_FILE=/app/data/apikeys.json
```

### Step 2: Buat API Keys

```bash
mkdir -p data
```

Buat `data/apikeys.json`:

```json
{
  "keys": [
    {
      "key": "pk_ajianaz",
      "name": "Ajianaz",
      "model": "glm-4.7",
      "token_limit_per_5h": 100000,
      "expiry_date": "2027-12-31T23:59:59Z",
      "created_at": "2026-03-17T00:00:00Z",
      "last_used": "2026-03-17T00:00:00Z",
      "total_lifetime_tokens": 0,
      "usage_windows": []
    },
    {
      "key": "pk_premium_user",
      "name": "Premium User",
      "model": "glm-4.7",
      "glmkey": "sk-premium-user-own-zai-key",
      "token_limit_per_5h": 500000,
      "expiry_date": "2027-06-30T23:59:59Z",
      "created_at": "2026-03-17T00:00:00Z",
      "last_used": "2026-03-17T00:00:00Z",
      "total_lifetime_tokens": 0,
      "usage_windows": []
    }
  ]
}
```

> **Catatan**: `glmkey` opsional. Jika diisi, user itu pakai key sendiri ke Z.AI (bukan master key dari `.env`).

### Step 3: Jalankan

```bash
docker compose up -d

# Verify
curl http://localhost:3000/health
# {"status":"ok","timestamp":"..."}
```

### Step 4: Test

```bash
# Cek quota
curl -H "Authorization: Bearer pk_ajianaz" http://localhost:3000/stats

# Chat completion
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer pk_ajianaz" \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Halo!"}]}'
```

### Struktur File yang Dibutuhkan

```
glm-proxy-golang/
  .env                    # ZAI_API_KEY, DEFAULT_MODEL, PORT
  data/
    apikeys.json          # Daftar API key user (volume mount ke /app/data)
  docker-compose.yml      # Konfigurasi container (sudah ada)
  Dockerfile              # Build image (sudah ada)
```

### Local Development (tanpa Docker)

```bash
# Build
go build -o bin/server ./cmd/server

# Jalankan (butuh data/apikeys.json)
ZAI_API_KEY=sk-xxxxxx ./bin/server

# Run tests
go test ./... -v -race
```

## Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/` | No | API info |
| GET | `/health` | No | Health check |
| GET | `/stats` | Yes | Token usage stats |
| POST | `/v1/messages` | Yes | Anthropic-compatible (goes to BigModel) |
| ALL | `/v1/*` | Yes | OpenAI-compatible (goes to Z.AI, strips `/v1`) |

## Authentication

Two methods:

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
# {"status":"ok","timestamp":"2026-03-17T00:00:00Z"}
```

### Check Quota

```bash
curl -H "Authorization: Bearer pk_test_user" http://localhost:3000/stats
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

## API Key Management

Keys stored in `data/apikeys.json`. Edit the file and the proxy picks up changes automatically.

### Key Structure

```json
{
  "keys": [{
    "key": "pk_user_12345",
    "name": "User Full Name",
    "model": "glm-4.7",
    "glmkey": "user_specific_zai_key",
    "token_limit_per_5h": 100000,
    "expiry_date": "2027-12-31T23:59:59Z",
    "created_at": "2026-01-01T00:00:00Z",
    "last_used": "2026-01-01T00:00:00Z",
    "total_lifetime_tokens": 0,
    "usage_windows": []
  }]
}
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `key` | Yes | Unique key (format: `pk_*`) |
| `name` | Yes | Display name |
| `model` | No | Model override for this key (falls back to `DEFAULT_MODEL`, then `glm-4.7`) |
| `glmkey` | No | **New**: Per-user upstream Z.AI key. Bypasses the master `ZAI_API_KEY` |
| `token_limit_per_5h` | Yes | Token quota per rolling 5-hour window |
| `expiry_date` | Yes | ISO 8601 expiry date |
| `created_at` | Yes | ISO 8601 creation time |
| `last_used` | Auto | Auto-updated on each request |
| `total_lifetime_tokens` | Auto | Cumulative token count |
| `usage_windows` | Auto | Internal tracking (auto-managed, auto-cleaned) |

### `glmkey` Feature

Each user can have their own upstream Z.AI API key:

```json
{
  "key": "pk_premium_user",
  "name": "Premium User",
  "glmkey": "user_premium_zai_key",
  "token_limit_per_5h": 500000,
  "expiry_date": "2027-12-31T23:59:59Z",
  "created_at": "2026-01-01T00:00:00Z",
  "last_used": "2026-01-01T00:00:00Z",
  "total_lifetime_tokens": 0,
  "usage_windows": []
}
```

Priority: `user's glmkey` > master `ZAI_API_KEY` env var.

## Environment Variables

Semua variabel di-set via file `.env` (Docker) atau export di shell (local).

| Variable | Default | Wajib? | Description |
|----------|---------|--------|-------------|
| `ZAI_API_KEY` | (none) | **Ya** | Master API key dari Z.AI. Digunakan untuk upstream auth semua user yang **tidak punya** `glmkey` |
| `DEFAULT_MODEL` | `glm-4.7` | Tidak | Model default jika user tidak punya `model` di apikeys.json |
| `PORT` | `3000` | Tidak | Port server |
| `DATA_FILE` | `data/apikeys.json` | Tidak | Path ke file API keys. Di Docker gunakan `/app/data/apikeys.json` |

### Dari Mana Dapat ZAI_API_KEY?

1. Buka [Z.AI Open Platform](https://open.bigmodel.cn/)
2. Login / register
3. Ke halaman **API Keys** / **Console**
4. Create new key
5. Copy key-nya (format: `sk-xxxx...`)

### Hubungan `ZAI_API_KEY` vs `glmkey`

```
User request masuk
    |
    v
User punya glmkey di apikeys.json?
    |
    +-- Ya --> pakai glmkey user untuk upstream auth
    |
    +-- Tidak --> pakai ZAI_API_KEY dari .env
```

## Rate Limiting

- **Type**: Rolling 5-hour window
- **Metric**: Total tokens across all active windows
- **Threshold**: `>` token_limit_per_5h (exactly at limit is still allowed)
- **Response**: HTTP 429 with `Retry-After` header (seconds until window expires)

When rate limited:
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
    config/config.go              # Env parsing
    storage/
      types.go                    # ApiKey, RateLimitInfo, StatsResponse
      keystore.go                 # In-memory cache + sync.RWMutex + periodic flush
    ratelimit/ratelimit.go        # Rolling 5h window
    proxy/
      types.go                    # Model resolution, header forwarding
      openai.go                   # Proxy to api.z.ai (strip /v1, Bearer auth)
      anthropic.go                # Proxy to open.bigmodel.cn (path as-is, x-api-key)
      sse.go                      # True chunked SSE streaming + inline token parse
      relay.go                    # Non-streaming response relay
    middleware/
      context.go                  # Context key helpers
      auth.go                     # Auth (Bearer/x-api-key)
      ratelimit.go                # 429 middleware
    handler/
      router.go                   # Chi router + CORS
      health.go                   # GET /health, GET /
      stats.go                    # GET /stats
      openai.go                   # /v1/* handler
      anthropic.go                # /v1/messages handler
  Dockerfile                      # Multi-stage: golang:1.25-alpine -> scratch
  docker-compose.yml              # Traefik labels, 128M limit, security hardening
```

## Docker

```bash
# Build
docker build -t glm-proxy-go .

# Run
docker compose up -d

# View logs
docker compose logs -f

# Stop
docker compose down
```

### Security Hardening (docker-compose.yml)

- `read_only: true` - Read-only filesystem
- `cap_drop: ALL` - Drop all Linux capabilities
- `no-new-privileges` - Prevent privilege escalation
- `memory: 128M` limit / `32M` reservation
- `scratch` base image - No shell, no package manager, minimal attack surface

### Traefik Integration

Uncomment the labels section in `docker-compose.yml`:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.glm-proxy.rule=Host(`glm.example.com`)"
  - "traefik.http.routers.glm-proxy.tls=true"
  - "traefik.http.routers.glm-proxy.tls.certresolver=letsencrypt"
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
| 403 | API key expired |
| 429 | Token quota exceeded |
| 502 | Upstream (Z.AI) error |

## Troubleshooting

```bash
# Container won't start
docker compose logs -f

# Rebuild
docker compose up --build -d

# Port conflict - change PORT in .env
PORT=3001 docker compose up -d

# Test upstream Z.AI key directly
curl -H "Authorization: Bearer YOUR_ZAI_KEY" https://api.z.ai/api/coding/paas/v4/models
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
