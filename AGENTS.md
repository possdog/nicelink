# AGENTS.md — tllc

## Build & run

```bash
go build -o tllc ./cmd/tllc
./tllc   # listens :80, optionally :443 if TLS certs present
```

Requires **Go 1.22+** (uses enhanced mux patterns: `"GET /"`, `r.PathValue("alias")`).

## Architecture

Single binary. One package at `cmd/tllc/main.go` with an embedded `cmd/tllc/index.html`.

- **POST /** — create a short link (`?url=`, optional `?alias=`)
- **GET /** — serve the embedded index.html
- **GET /{alias}** — 308 redirect to the stored URL, or 404
- Storage: **bbolt** (`link_shortener.bolt`, bucket `"urls"`)
- TLS: auto-enabled when `domain.cert.pem` and `private.key.pem` exist in the working directory
- Graceful shutdown on SIGINT/SIGTERM (5s drain)

## Testing / linting

No tests, no linter, no CI. The `.gitignore` excludes `*.bolt`, `*.pem`, and the `/tllc` binary.
