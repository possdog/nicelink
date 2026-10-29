# AGENTS.md — nicelink

## Build & run

```bash
go build -o nicelink ./cmd/nicelink
./nicelink   # listens :80 by default, optionally :443 if TLS certs present
```

Requires **Go 1.22+** (uses enhanced mux patterns: `"GET /"`, `r.PathValue("alias")`).

Environment variables:

- `PORT` — when set, the default listen address becomes `:$PORT` (Fly.io injects `PORT=8080`; the `-port` flag still overrides it)
- `DATA_DIR` — directory holding `link_shortener.bolt` and `secret.key` (default: working directory; `/data` on Fly.io, where the persistent volume is mounted)
- `TRUST_PROXY` — when `true`, the rightmost `X-Forwarded-For` entry is trusted regardless of the peer address (set on Fly.io, where every inbound connection arrives via the platform's edge proxy; see Rate limiting)

## Architecture

Single binary, one package at `cmd/nicelink/`, split across focused source files:

```
cmd/nicelink/
├── main.go      — entry point: db init, secret loading, migration, HTTP server, graceful shutdown
├── app.go       — shared types/constants (app struct, validation regexes, JSON response types, sentinel errors, DATA_DIR paths)
├── crypto.go    — encryption helpers: secret management, alias hashing, key derivation, AES-256-GCM encrypt/decrypt
├── handlers.go  — HTTP handlers: serveIndex, handleCreate, handleRedirect, JSON helpers
├── ratelimit.go — per-IP rate limiting middleware (golang.org/x/time/rate)
├── migrate.go   — one-time migration of legacy plaintext entries to encrypted format
└── index.html   — embedded frontend (via go:embed)

Deployment files at the repo root:
├── Dockerfile      — multi-stage build (golang:1.27-alpine → alpine:3.22); ENV PORT=8080 DATA_DIR=/data
├── .dockerignore   — keeps *.bolt, secret.key, *.pem, .git out of the build context
└── fly.toml        — Fly.io app config: [mounts] volume nicelink_data → /data, [http_service] internal_port 8080
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

Both files live in `DATA_DIR` (working directory by default). On Fly.io, `DATA_DIR=/data` is a persistent volume, so the database and secret survive machine restarts and redeploys — but the volume is the single source of truth: never delete it, and keep scheduled snapshots enabled.

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

On **Fly.io** the peer is never loopback — every inbound connection arrives through the platform's edge proxy, which appends the real client IP as the rightmost `X-Forwarded-For` entry. Setting `TRUST_PROXY=true` (done in `fly.toml`) marks any peer as trusted, which is safe there because clients cannot reach the machine except through that proxy. Without it, all clients would share the proxy's single rate-limit bucket.

### Shutdown

Graceful shutdown on SIGINT/SIGTERM (5s drain).

## Deployment (Fly.io)

The app is deployed as the Fly app `possdog-nicelink` (region `iad`, hostname `possdog-nicelink.fly.dev`). The app name is namespaced to the `possdog` handle because Fly app names are global. TLS is terminated at the Fly edge (`force_https = true`), so the built-in `:443` listener stays off — the `domain.cert.pem`/`private.key.pem` files are for bare-metal use only.

**Persistence.** `fly.toml` mounts the volume `nicelink_data` at `/data` and sets `DATA_DIR=/data`, so `link_shortener.bolt` and `secret.key` live on the volume and survive restarts/redeploys. bbolt holds an exclusive file lock on the DB, so the app must run as **exactly one machine**: `min_machines_count = 1`, `max_machines_count = 1`, `auto_stop_machines = "off"`. Do not raise `max_machines_count` — each extra machine gets its own empty volume and cannot open the same DB. If horizontal scaling is ever needed, migrate storage to managed Postgres (`fly mpg`) first.

First-time setup (already done for `possdog-nicelink`, kept for reference):

```bash
fly apps create possdog-nicelink
fly volumes create nicelink_data --size 1 --region iad -a possdog-nicelink --yes
fly ips allocate-v4 -a possdog-nicelink --yes   # dedicated IPv4 ($2/mo); v6 is free
fly deploy --remote-only
```

Day-to-day:

```bash
./deploy.sh                       # vet+test, volume guards, fly deploy, post-deploy DB checks
fly status -a possdog-nicelink          # machine state + health checks
fly logs -a possdog-nicelink            # app logs (rate-limit 429s appear as "WARN: rate limit exceeded")
./clone_database.sh            # import an existing link_shortener.bolt + secret.key into /data (see below)
fly certs add t.l.lc            # custom domain, after DNS A/AAAA records point at Fly
```

**Migrating existing data** (e.g. from a previous VPS): run `./clone_database.sh` from the directory holding the old `link_shortener.bolt` and `secret.key`. The script resolves the running machine's ID dynamically (`fly status --json` + `jq`), then stages each file under a fresh `.incoming` name and swaps it in with `mv` over SSH, and restarts the machine last. This dance exists because `fly ssh sftp put` takes one file at a time (`put <local> <remote>`) and refuses to overwrite existing remote files, while `fly ssh console -C` does not run a shell (one command per call, no `&&`). The restart is required because bbolt keeps the old file open and locked until then; `mv` only renames the directory entry, which is safe.

Do the swap while no link-creation traffic is in flight (e.g. right after a deploy). The legacy plaintext migration runs automatically on next boot if the DB is in the old format. `secret.key` must travel with the DB or no link can be decrypted.

Volumes are pinned to one region and one physical host; scheduled snapshots are enabled by default (`fly volumes snapshots list`). Losing the volume or `secret.key` loses every link permanently.

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
