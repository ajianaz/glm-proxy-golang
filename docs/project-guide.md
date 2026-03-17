# Panduan Project GLM Proxy (Go)

Dokumen ini menjelaskan struktur project, alur kode, dan konsep Go yang dipakai.
Ditulis untuk yang baru belajar Go tapi sudah paham dasar programming.

---

## Daftar Isi

1. [Struktur Project](#1-struktur-project)
2. [Konsep Go yang Dipakai](#2-konsep-go-yang-dipakai)
3. [Alur Request dari Awal Sampai Akhir](#3-alur-request-dari-awal-sampai-akhir)
4. [Penjelasan Tiap Package](#4-penjelasan-tiap-package)
5. [Cara Membaca Kode Ini](#5-cara-membaca-kode-ini)
6. [Perbandingan dengan TypeScript](#6-perbandingan-dengan-typescript)

---

## 1. Struktur Project

```
glm-proxy-golang/
├── cmd/
│   └── server/
│       └── main.go              <- Titik masuk program (seperti index.ts)
│
├── internal/                     <- Semua kode business logic ada di sini
│   ├── config/
│   │   └── config.go             <- Baca env variable (PORT, ZAI_API_KEY, dll)
│   │
│   ├── storage/
│   │   ├── types.go              <- Struct/data model (ApiKey, StatsResponse, dll)
│   │   └── keystore.go           <- Simpan & baca API keys dari file JSON
│   │
│   ├── ratelimit/
│   │   └── ratelimit.go          <- Hitung token usage, cek limit 5 jam
│   │
│   ├── middleware/
│   │   ├── context.go            <- Simpan data ke request context
│   │   ├── auth.go               <- Cek API key (Authorization / x-api-key)
│   │   ├── ratelimit.go          <- Blokir request jika limit exceeded
│   │   └── json.go               <- Helper tulis JSON response
│   │
│   ├── proxy/
│   │   ├── types.go              <- Helper functions (model injection, header forwarding)
│   │   ├── openai.go             <- Forward request ke Z.AI (OpenAI-compatible)
│   │   ├── anthropic.go          <- Forward request ke BigModel (Anthropic-compatible)
│   │   ├── sse.go                <- Streaming SSE (data dikirim chunk per chunk)
│   │   └── relay.go              <- Forward response non-streaming
│   │
│   └── handler/
│       ├── router.go             <- Definisikan semua route (GET /, GET /health, dll)
│       ├── health.go             <- Handler GET / dan GET /health
│       ├── stats.go              <- Handler GET /stats
│       ├── openai.go             <- Handler /v1/* (OpenAI)
│       └── anthropic.go          <- Handler /v1/messages (Anthropic)
│
├── test/
│   └── integration/
│       ├── integration_test.go   <- Test end-to-end HTTP
│       └── streaming_test.go     <- Test proxy, model injection, glmkey
│
├── go.mod                        <- Package manager (seperti package.json)
├── go.sum                        <- Lock file (seperti bun.lockb)
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── .env.example
```

### Kenapa Struktur Seperti Ini?

Go punya konvensi baku untuk struktur project:

| Konvensi | Artinya |
|----------|---------|
| `cmd/server/` | Entry point program. Bisa punya banyak cmd (cmd/cli, cmd/migrate, dll) |
| `internal/` | Kode yang **tidak boleh** di-import oleh project lain. Private |
| `package foo` | Setiap folder = 1 package. Semua file dalam folder share scope |
| `*_test.go` | File test. Go otomatis tau ini file test dari nama |

> **Analogi TypeScript**: Kalau di TS kamu punya `src/index.ts`, `src/handlers/`, `src/utils/`, di Go itu jadi `cmd/server/main.go`, `internal/handler/`, `internal/middleware/`.

---

## 2. Konsep Go yang Dipakai

### 2.1 Package

Di Go, semua file dalam satu folder punya **package yang sama**:

```go
package storage  // semua file di internal/storage/ punya package "storage"

func FindKey() {} // bisa dipanggil langsung oleh file lain di folder yang sama
```

Untuk import dari package lain:

```go
import "glm-proxy/internal/storage"  // import package "storage"

key, ok := store.FindKey("pk_test")  // panggil fungsi dari package storage
```

> **Perbedaan dengan TS**: Di TS kamu pakai `import { FindKey } from './storage'`. Di Go, kamu import **package** (folder), bukan file individual.

### 2.2 Struct

Struct = object/class tanpa method (atau dengan method via receiver):

```go
// Definisi struct (seperti interface/type di TS)
type ApiKey struct {
    Key   string `json:"key"`    // tag `json:"key"` untuk serialisasi
    Name  string `json:"name"`
    Limit int    `json:"token_limit_per_5h"`
}

// Method dengan receiver (seperti class method di TS)
func (k *ApiKey) IsExpired() bool {
    return time.Now().After(k.ExpiryDate)
}
```

Tag `` `json:"key"` `` = mapping antara struct field dan JSON key. Mirip `@JsonProperty("key")` di Java atau `@JsonAlias` di TS.

### 2.3 Interface (Implisit)

Go punya interface tapi **implisit** - tidak perlu `implements`:

```go
// Di TypeScript:
// class KeyStore implements IKeyStore { ... }

// Di Go: cukup implement semua method-nya, otomatis satisfy interface
type Storage interface {
    FindKey(key string) (*ApiKey, bool)
    UpdateUsage(key string, tokens int)
}
```

Project ini **tidak banyak pakai interface** karena langsung pakai concrete type (`*KeyStore`). Ini sengaja untuk kesederhanaan.

### 2.4 Goroutine (`go func()`)

Goroutine = lightweight thread. Dipakai di beberapa tempat:

```go
// Fire-and-forget: update usage di background
go func() {
    store.UpdateUsage(keyValue, totalTokens)
}()

// Parallel: server jalan di background
go func() {
    srv.ListenAndServe()
}()
```

> **Perbedaan dengan TS**: Di TS kamu pakai `Promise` atau `async`. Di Go, cukup tambah `go` di depan function call, dan dia jalan paralel.

### 2.5 Channel (`chan`)

Channel = cara goroutine saling kirim data (seperti `EventEmitter` di TS):

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM)
<-quit  // blocking: tunggu sampai ada sinyal
```

### 2.6 Context (`context.Context`)

Context = membawa data sepanjang lifecycle request (seperti `req.app` di Express/Next.js):

```go
// Set data ke context
r = r.WithContext(context.WithValue(r.Context(), apiKeyKey, key))

// Ambil data dari context (di handler lain)
key := r.Context().Value(apiKeyKey).(*storage.ApiKey)
```

### 2.7 Defer

`defer` = jalankan code sebelum function selesai (seperti `try/finally`):

```go
func NewKeyStore() (*KeyStore, error) {
    // ...
    defer store.Close()  // otomatis dipanggil saat function return
}
```

### 2.8 Mutex (`sync.RWMutex`)

Mutex = kunci supaya tidak ada race condition (seperti `Mutex` di Rust atau lock di database):

```go
type KeyStore struct {
    mu   sync.RWMutex
    data ApiKeysData
}

func (ks *KeyStore) FindKey(key string) (*ApiKey, bool) {
    ks.mu.RLock()         // Read lock (banyak goroutine bisa baca bersamaan)
    defer ks.mu.RUnlock()
    // ... baca data
}

func (ks *KeyStore) UpdateUsage(key string, tokens int) {
    ks.mu.Lock()          // Write lock (hanya 1 goroutine yang bisa tulis)
    defer ks.mu.Unlock()
    // ... tulis data
}
```

### 2.9 Pointer (`*` vs value)

```go
func FindKey(key string) (*ApiKey, bool)
//                     ^
//                     pointer ke ApiKey (bisa nil, bisa di-modify)

func FindKey(key string) (ApiKey, bool)
//                     ^
//                     value copy (tidak bisa nil, tidak bisa di-modify original)
```

Project ini pakai pointer (`*ApiKey`) karena:
- Bisa `nil` (untuk cek "not found")
- Hemat memory (tidak copy seluruh struct)
- Bisa modify original data

---

## 3. Alur Request dari Awal Sampai Akhir

### Alur: `GET /health` (paling sederhana)

```
Browser                Server (Go)
  |                       |
  |-- GET /health ------->|
  |                       |-- router.go:  r.Get("/health", Health)
  |                       |-- health.go:  tulis JSON {"status":"ok"}
  |<-- 200 JSON ----------|
```

### Alur: `POST /v1/chat/completions` (proxy lengkap)

```
Browser                    Server (Go)                         Upstream (Z.AI)
  |                           |                                      |
  |-- POST /v1/chat/comp ---->|                                      |
  |   Authorization: Bearer   |                                      |
  |   pk_test_user            |                                      |
  |                           |                                      |
  |                     [middleware/auth.go]                         |
  |                           |-- extractApiKey: ambil dari header   |
  |                           |-- store.FindKey: cari di memory      |
  |                           |-- IsExpired: cek tanggal             |
  |                           |-- simpan ke context                  |
  |                           |                                      |
  |                     [middleware/ratelimit.go]                    |
  |                           |-- CheckRateLimit: hitung token 5h    |
  |                           |-- kalau over limit -> 429 return     |
  |                           |                                      |
  |                     [handler/openai.go]                          |
  |                           |-- ambil ApiKey dari context          |
  |                           |-- delegate ke proxy.OpenAIProxy      |
  |                           |                                      |
  |                     [proxy/openai.go]                            |
  |                           |-- GetModelForKey: resolve model       |
  |                           |-- UpstreamKey: ambil glmkey or master |
  |                           |-- readAndInjectModel: inject model    |
  |                           |-- strip /v1 dari path                |
  |                           |-- buat HTTP request ke upstream       |
  |                           |                                      |
  |                           |-- POST /api/coding/paas/v4/        |
  |                           |   chat/completions ---------------->|
  |                           |   Authorization: Bearer master_key |
  |                           |                                      |
  |                           |<-- 200 JSON response ---------------|
  |                           |                                      |
  |                     [proxy/relay.go]                             |
  |                           |-- copy header response               |
  |                           |-- kirim body ke client               |
  |                           |-- (background) parse usage tokens    |
  |                           |-- (background) UpdateUsage ke store   |
  |                           |                                      |
  |<-- 200 JSON response ------|                                      |
  |                           |                                      |
```

### Alur: SSE Streaming

```
Browser                    Server (Go)                         Upstream
  |                           |                                      |
  |-- POST /v1/chat/comp ---->|                                      |
  |   "stream": true          |                                      |
  |                           |                                      |
  |                     [auth + ratelimit ...]                       |
  |                           |                                      |
  |                     [proxy/openai.go]                            |
  |                           |-- detect Content-Type: text/event-stream
  |                           |-- delegate ke streamSSE()             |
  |                           |                                      |
  |                     [proxy/sse.go]                               |
  |                           |-- kirim header SSE ke client         |
  |                           |-- loop: baca baris per baris         |
  |                           |   |                                  |
  |                           |   |-- baris "data: {chunk1}" ------->|
  |                           |<-- flush -----------------------------|
  |<-- data: {chunk1} --------|                                      |
  |                           |   |                                  |
  |                           |   |-- baris "data: {chunk2}" ------->|
  |                           |<-- flush -----------------------------|
  |<-- data: {chunk2} --------|                                      |
  |                           |   |                                  |
  |                           |   |-- baris "data: [DONE]" --------->|
  |<-- data: [DONE] ----------|                                      |
  |                           |                                      |
  |                           |-- totalTokens terhitung             |
  |                           |-- UpdateUsage ke store               |
  |                           |                                      |
```

**Perbedaan penting dengan TS version**: Di TS, `await response.text()` menunggu SEMUA data selesai baru dikirim ke client. Di Go, data dikirim **baris per baris** pakai `bufio.Scanner` + `Flusher`.

---

## 4. Penjelasan Tiap Package

### `cmd/server/main.go` - Entry Point

File ini yang jalan pertama kali. Fungsinya:
- Parse konfigurasi
- Load API keys dari file
- Setup router
- Start HTTP server
- Tunggu sinyal shutdown (Ctrl+C / SIGTERM)
- Graceful shutdown (selesaikan request yang masih proses)

```go
func main() {
    cfg := config.Load()           // baca env variable
    store := storage.NewKeyStore()  // load apikeys.json ke memory
    router := handler.NewRouter()   // setup semua route + middleware

    srv := &http.Server{Addr: ":3000", Handler: router}
    go srv.ListenAndServe()         // jalan di background

    <-quit                          // tunggu SIGTERM
    srv.Shutdown(ctx)               // graceful shutdown
}
```

### `internal/config/` - Konfigurasi

Membaca environment variable. Sangat sederhana, hanya pembungkus `os.Getenv`.

### `internal/storage/` - Data Layer

**types.go** = semua struct/data model:
- `ApiKey` - data 1 API key (termasuk field `glmkey`)
- `ApiKeysData` - wrapper array of keys (format file JSON)
- `StatsResponse` - response /stats endpoint
- `RateLimitInfo` - hasil cek rate limit

**keystore.go** = logic simpan/baca:
- Load file JSON ke memory saat startup
- `FindKey()` - cari key (read lock, concurrency-safe)
- `UpdateUsage()` - update token (write lock)
- `flushLoop()` - goroutine yang tiap 30 detik tulis ke disk jika ada perubahan
- `Close()` - stop flush goroutine + final save

> **Kenapa in-memory?** TS version baca file JSON setiap request. Go version load sekali ke memory, baca dari memory (cepat), tulis ke disk secara periodic (aman).

### `internal/ratelimit/` - Rate Limiting

Satu file saja, satu fungsi utama: `CheckRateLimit()`.

Algoritma:
1. Ambil semua `usage_windows` yang masih aktif (5 jam terakhir)
2. Jumlahkan total token
3. Bandingkan dengan `token_limit_per_5h`
4. Kalau `totalTokens > limit` -> return `allowed: false` + `retryAfter`

### `internal/middleware/` - HTTP Middleware

Middleware di Go = fungsi yang membungkus handler, mirip Express.js middleware.

```go
// Pattern middleware di Go:
func MyMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // ... lakukan sesuatu sebelum handler
        next.ServeHTTP(w, r)  // panggil handler berikutnya
        // ... lakukan sesuatu setelah handler
    })
}
```

**auth.go** - Cek API key:
1. Ambil dari header `Authorization: Bearer xxx` atau `x-api-key`
2. Cari di KeyStore
3. Cek apakah expired
4. Kalau valid, simpan ke request context

**ratelimit.go** - Cek quota:
1. Ambil ApiKey dari context
2. Panggil `ratelimit.CheckRateLimit()`
3. Kalau exceeded, return 429 + header `Retry-After`

**context.go** - Helper set/get data di context:
- `SetApiKey(r, key)` - simpan ApiKey ke context
- `GetApiKey(r)` - ambil ApiKey dari context

### `internal/proxy/` - Upstream Proxy

Ini bagian terpenting - yang melakukan forwarding ke Z.AI.

**types.go** - Helper functions:
- `GetModelForKey()` - resolve model: per-key > env default > "glm-4.7"
- `readAndInjectModel()` - baca body, inject/overwrite field "model"
- `forwardHeaders()` - copy header content-type, accept, user-agent
- `WriteError()` - tulis JSON error response

**openai.go** - Proxy ke Z.AI:
- Upstream: `https://api.z.ai/api/coding/paas/v4`
- Path: `/v1/chat/completions` -> `/chat/completions` (strip `/v1`)
- Auth: `Authorization: Bearer <upstream_key>`
- Deteksi streaming vs non-streaming dari Content-Type

**anthropic.go** - Proxy ke BigModel:
- Upstream: `https://open.bigmodel.cn/api/anthropic`
- Path: `/v1/messages` -> `/v1/messages` (pakai apa adanya)
- Auth: `x-api-key: <upstream_key>`
- Forward header `anthropic-version`

**sse.go** - True SSE Streaming:
- Baca dari upstream baris per baris (`bufio.Scanner`)
- Tulis ke client baris per baris + flush
- Parse token dari chunk data yang mengandung `usage`
- Return total tokens untuk di-track

**relay.go** - Non-streaming response:
- Baca semua body dari upstream
- Copy header + body ke client
- Parse JSON untuk extract token usage
- Update usage secara async (fire-and-forget)

### `internal/handler/` - HTTP Handlers

**router.go** - Setup chi router:
```
GET /                  -> Index (public)
GET /health            -> Health (public)
GET /stats             -> Stats (auth + ratelimit)
POST /v1/messages      -> Anthropic (auth + ratelimit)
ALL  /v1/*             -> OpenAI (auth + ratelimit)
```

> Urutan penting: `/v1/messages` harus didefinisikan **sebelum** `/v1/*` catch-all.

Handler lain (`health.go`, `stats.go`, `openai.go`, `anthropic.go`) sangat tipis - ambil ApiKey dari context lalu delegate ke proxy.

---

## 5. Cara Membaca Kode Ini

Kalau kamu mau paham project ini secara utuh, baca urutannya:

1. **`storage/types.go`** - Pahami dulu data modelnya (ApiKey, dll)
2. **`storage/keystore.go`** - Bagaimana data disimpan di memory
3. **`ratelimit/ratelimit.go`** - Logika rate limit
4. **`middleware/auth.go`** + **`middleware/context.go`** - Cara auth bekerja
5. **`proxy/types.go`** - Helper functions
6. **`proxy/openai.go`** - Inti proxy logic (paling penting)
7. **`proxy/sse.go`** - Streaming logic
8. **`handler/router.go`** - Semua route digabungkan
9. **`cmd/server/main.go`** - Glue everything together

Untuk test, baca:
1. **`storage/keystore_test.go`** - Test data layer
2. **`middleware/auth_test.go`** - Test auth
3. **`test/integration/integration_test.go`** - Test full HTTP stack

### Tips Membaca Go Code

```go
// := adalah short variable declaration (infer type dari value)
name := "hello"    // sama dengan: var name string = "hello"

// Multiple return value
key, ok := store.FindKey("pk_test")  // ok = boolean found/not found

// Error handling pattern (bukan try/catch)
result, err := doSomething()
if err != nil {
    return err  // return error
}
// use result...

// Interface satisfaction (implisit)
// Kalau sebuah type punya method FindKey() dan UpdateUsage(),
// otomatis satisfy interface yang butuh method tersebut
```

---

## 6. Perbandingan dengan TypeScript

| Konsep | TypeScript | Go |
|--------|-----------|-----|
| Entry point | `index.ts` / `bun run index.ts` | `cmd/server/main.go` / `go run ./cmd/server` |
| Package manager | `bun install` / `package.json` | `go mod tidy` / `go.mod` |
| Import | `import { X } from './file'` | `import "glm-proxy/internal/file"` |
| Types | `interface X { ... }` | `type X struct { ... }` |
| Export | `export function foo()` | Nama kapital = otomatis export (`Foo`, bukan `foo`) |
| Private | `function foo()` atau `#foo` | Nama huruf kecil = private (`foo`, bukan `Foo`) |
| Async | `async/await` | `goroutine` + `channel` |
| Middleware | `app.use((req, res, next) => ...)` | `func(next http.Handler) http.Handler` |
| Request obj | `req.body`, `req.headers` | `r.Body`, `r.Header` |
| Response | `res.json(data)` | `json.NewEncoder(w).Encode(data)` |
| Error handling | `try/catch` | `if err != nil { return err }` |
| Null check | `value ?? default`, `value?.prop` | `if value == nil { ... }`, `value.Property` (panic if nil!) |
| HTTP framework | Hono / Express | chi (wrapper stdlib `net/http`) |
| Test | `describe/it/expect` (bun:test) | `func TestXxx(t *testing.T)` + `if ... { t.Fatal() }` |
| Build | `bun build` | `go build` |
| Binary | ~300MB (node+bun+deps) | ~6MB (static binary) |

### Naming Convention (PENTING)

Di Go, **huruf kapital pertama = public/exported**, huruf kecil = private:

```go
// PUBLIC (bisa diakses dari package lain)
type ApiKey struct { ... }     // exported type
func FindKey() { ... }         // exported function
func (k *ApiKey) UpstreamKey()  // exported method

// PRIVATE (hanya bisa diakses dalam package yang sama)
type apiKeysData struct { ... } // unexported type
func readAndInjectModel() { ... } // unexported function
func (ks *KeyStore) save() { ... } // unexported method
```

Ini mengapa di project ini ada dua versi function:
- `WriteError()` (kapital) = dipanggil dari package lain
- `writeError()` / `readAndInjectModel()` (kecil) = hanya dipakai internal proxy package

---

## Dependensi Project

Project ini **minimal dependensi** - hanya 2 library eksternal:

| Library | Versi | Fungsi |
|---------|-------|--------|
| `github.com/go-chi/chi/v5` | v5.2.5 | HTTP router (lightweight wrapper stdlib) |
| `github.com/go-chi/cors` | v1.2.2 | CORS middleware |

Selebihnya menggunakan Go standard library:
- `net/http` - HTTP server & client
- `encoding/json` - JSON parse & serialize
- `sync` - Mutex, goroutine sync
- `bufio` - Buffered I/O (untuk SSE scanner)
- `os` - File system, env variable
- `os/signal` - Signal handling (SIGTERM, SIGINT)
- `context` - Request context
- `time` - Time parsing & formatting
- `io` - I/O interfaces (ReadCloser, Flusher)
