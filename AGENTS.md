# AGENTS.md — nicelink

## Build & run

```bash
go build -o nicelink ./cmd/nicelink
./nicelink   # listens :80, optionally :443 if TLS certs present
```

Requires **Go 1.22+** (uses enhanced mux patterns: `"GET /"`, `r.PathValue("alias")`).

## Architecture

Single binary, one package at `cmd/nicelink/`, split across focused source files:

```
cmd/nicelink/
├── main.go      — entry point: db init, secret loading, migration, HTTP server, graceful shutdown
├── app.go       — shared types/constants (app struct, validation regexes, JSON response types, sentinel errors)
├── crypto.go    — encryption helpers: secret management, alias hashing, key derivation, AES-256-GCM encrypt/decrypt
├── handlers.go  — HTTP handlers: serveIndex, handleCreate, handleRedirect, JSON helpers
├── ratelimit.go — per-IP rate limiting middleware (golang.org/x/time/rate)
├── migrate.go   — one-time migration of legacy plaintext entries to encrypted format
└── index.html   — embedded frontend (via go:embed)
```

### HTTP endpoints

- **POST /** — create a short link (`?url=`, optional `?alias=`); returns JSON `{ "short_link": "/<alias>" }`
- **GET /** — serve the embedded index.html
- **GET /{alias}** — 308 redirect to the stored URL, or 404

### Storage & encryption

Storage backend is **bbolt** (`link_shortener.bolt`). Neither aliases nor destination URLs are stored in plaintext:

- **Key:** `SHA-256(alias)` — the alias itself is never persisted
- **Value:** `AES-256-GCM(url)`, encrypted with a per-alias key derived via `HMAC-SHA256(secret, alias)`
- **Secret:** a 32-byte server secret stored in `secret.key` (0600), auto-generated on first run
- The only way to recover a destination URL is to know the alias and request `GET /{alias}`

Because keys are hashed and values are encrypted, a sysadmin with direct access to the `.bolt` file cannot trivially read aliases or destinations. **Losing `secret.key` makes every existing link permanently unrecoverable.**

Two bbolt buckets are used:
- `"urls"` — the encrypted link mappings
- `"meta"` — operational metadata (currently just a `"migrated"` flag)

### Legacy migration

On startup, `migrate.go` runs a one-time migration of any legacy plaintext entries (where `key=alias, value=url`) to the encrypted format. Legacy entries are detected by key length: old aliases are 5–20 bytes, SHA-256 hashes are always 32. Completion is tracked via the `"migrated"` key in the `"meta"` bucket.

### TLS

Auto-enabled when `domain.cert.pem` and `private.key.pem` exist in the working directory.

### Rate limiting

All HTTP endpoints are wrapped in per-client-IP rate limiting (`ratelimit.go`) using `golang.org/x/time/rate`. Each IP gets a token-bucket limiter (5 req/s sustained, burst of 10). Requests beyond the allowance receive `429 Too Many Requests`.

Client IP extraction (`clientIP`) depends on the peer:

- **Direct exposure** (no reverse proxy): `r.RemoteAddr` is the kernel-supplied TCP source IP and is used directly; `X-Forwarded-For` and `X-Real-IP` are ignored — honoring them would allow any client to bypass rate limiting by sending a different header value on each request.
- **Behind a trusted proxy** (peer is `127.0.0.1` or `::1`, e.g. Caddy on localhost): the **rightmost** `X-Forwarded-For` entry is used. Proxies append the address of their immediate peer to the chain, so with a single trusted hop the rightmost entry is the one the trusted proxy appended; everything to its left may be client-supplied and spoofable. Taking the leftmost entry would let a client rotate fake IPs and get a fresh rate-limit bucket per request. If the rightmost entry is not a valid IP, the proxy did not append one and the whole chain is untrustworthy — the limiter falls back to the proxy's own IP (one shared bucket). `X-Real-IP` is never trusted, even behind a trusted proxy, because proxies do not strip a client-supplied `X-Real-IP`.

Note for future deployments: the rightmost-entry rule is correct for exactly one trusted proxy hop. If a second proxy layer is ever added in front of the app, revisit `clientIP` (the rightmost entry would then be the middle proxy, not the client).

### Shutdown

Graceful shutdown on SIGINT/SIGTERM (5s drain).

## Testing / linting

```bash
go test -v -count=1 ./cmd/nicelink/
go vet ./cmd/nicelink/
go fmt ./cmd/nicelink/
```

Tests live alongside the source in `cmd/nicelink/*_test.go`:

- `helpers_test.go` — shared test infrastructure (temp bbolt DB, request helpers)
- `crypto_test.go` — hash/deriveKey/encrypt/decrypt, secret management, randomAlias
- `handlers_test.go` — serveIndex, handleCreate, handleRedirect (URL/alias validation, edge cases, malicious inputs)
- `ratelimit_test.go` — clientIP extraction, rate limiting, middleware
- `migrate_test.go` — legacy migration, idempotency, mixed entries, plaintext removal

No linter or CI. The `.gitignore` excludes `*.bolt`, `*.pem`, `secret.key`, and the `/nicelink` binary.
