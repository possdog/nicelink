# nicelink

> 🤖 This code was written by a human-led AI agent.

A little baby link shortener. Zero trackers, server-side URL encryption.

## Usage
Use the web frontend at [t.l.lc](https://t.l.lc/), or make a POST request:
```bash
# Get a random alias
curl -X POST 'https://t.l.lc/?url=https://example.com/'
# → {"short_link":"/qlbUh"}

# Optionally specify a custom alias
curl -X POST 'https://t.l.lc/?url=https://example.com/&alias=example'
# → {"short_link":"/example"}
```

## Building
```bash
go build -o nicelink ./cmd/nicelink

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o nicelink ./cmd/nicelink
```

## Deploying (Fly.io)

The app runs on Fly.io with its state on a persistent volume, so links survive restarts and redeploys:

- `Dockerfile` builds a minimal image (`PORT=8080`, `DATA_DIR=/data`)
- `fly.toml` mounts the volume `nicelink_data` at `/data` and runs exactly one machine (bbolt holds an exclusive lock on the DB file, so it cannot scale horizontally)

```bash
fly volumes create nicelink_data --size 1 --region iad -a possdog-nicelink --yes
fly ips allocate-v4 -a possdog-nicelink --yes
fly deploy --remote-only     # then verify: https://possdog-nicelink.fly.dev/
```

Both `link_shortener.bolt` and `secret.key` live in `/data`; losing either the volume or the secret loses every link. To import an existing database, upload both files to `/data` (e.g. `fly ssh sftp put link_shortener.bolt secret.key /data/`) before starting the machine.
