# tllc

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
go build -o tllc ./cmd/tllc

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o tllc ./cmd/tllc
```
