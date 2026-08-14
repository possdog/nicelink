package main

import (
	"embed"
	"errors"
	"regexp"

	bolt "go.etcd.io/bbolt"
)

// Static assets embedded into the binary at build time.
//
//go:embed index.html
var staticFiles embed.FS

// Database and key files.
const (
	dbName      = "link_shortener.bolt"
	bucketName  = "urls"
	metaBucket  = "meta"
	migratedKey = "migrated"   // presence in metaBucket means legacy→encrypted migration is done
	secretName  = "secret.key" // 32-byte server secret for per-alias key derivation
)

// Pre-compiled validation regexes — the same rules as the Rust original.
//
// urlRegex   matches http or https URLs containing only printable non-space
//
//	characters ([\x21-\x7e] is the ASCII printable range minus space).
//
// aliasRegex matches 5–20 characters from [a-zA-Z0-9_].
var (
	urlRegex   = regexp.MustCompile(`^https?://[\x21-\x7E]+$`)
	aliasRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{5,20}$`)
)

// Alphanumeric charset used for auto-generated short aliases.
const aliasCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// app bundles the shared bbolt database handle, the server secret used for
// per-alias key derivation, and implements http.Handler.
type app struct {
	db     *bolt.DB
	secret []byte // 32 bytes; never persisted alongside alias data
}

// --- request / response types (JSON) ---

type createResponse struct {
	ShortLink string `json:"short_link"`
}

type errorResponse struct {
	Detail string `json:"detail"`
}

// --- sentinel errors for control flow inside bbolt transactions ---

var (
	errAliasExists   = errors.New("alias exists")
	errAliasNotFound = errors.New("alias not found")
)
