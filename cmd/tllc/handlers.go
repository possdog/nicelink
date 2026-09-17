package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// --- HTTP handlers ---

// serveIndex replies with the embedded index.html on GET /.
func (a *app) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := staticFiles.ReadFile("index.html")
	if err != nil {
		// Should never happen — the file is embedded at build time.
		log.Printf("ERROR: embedded index.html not readable: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// handleCreate handles POST / with query parameters "url" (required) and
// "alias" (optional).  It validates inputs, stores the mapping in bbolt,
// and returns the generated short-link path as JSON.
//
// Status codes:
//
//	201 — short link created
//	400 — invalid or missing parameters
//	409 — alias already exists
//	500 — internal / database error
func (a *app) handleCreate(w http.ResponseWriter, r *http.Request) {
	// --- 1. Parse & validate URL ---
	url := strings.TrimSpace(r.URL.Query().Get("url"))
	if url == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Detail: "Missing 'url' parameter"})
		return
	}
	if !urlRegex.MatchString(url) {
		writeJSON(w, http.StatusBadRequest,
			errorResponse{Detail: "Invalid URL format. Must be a valid http or https URL"})
		return
	}

	// --- 2. Parse & validate alias (or generate one) ---
	// Trim before the emptiness check so that whitespace-only aliases are
	// treated as "no alias provided" and auto-generated, consistent with an
	// absent or empty parameter.
	var alias string
	if rawAlias := strings.TrimSpace(r.URL.Query().Get("alias")); rawAlias != "" {
		alias = rawAlias
		if !aliasRegex.MatchString(alias) {
			writeJSON(w, http.StatusBadRequest,
				errorResponse{Detail: "Invalid alias format. Must be 5-20 alphanumeric characters or underscores"})
			return
		}
	} else {
		alias = randomAlias()
	}

	// --- 3. Encrypt destination & atomically check existence & insert ---
	keyHash := hashAlias(alias)
	encrypted, err := encryptURL(a.secret, alias, url)
	if err != nil {
		// Log the hash, not the alias — the alias is the secret that must
		// never be persisted, and a log file is persistence.
		log.Printf("ERROR: encrypt failed for key %x: %v", keyHash, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Detail: "Internal Server Error"})
		return
	}

	err = a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucketName)
		}
		// Bail if the alias is already taken (checked via hash).
		if existing := b.Get(keyHash); existing != nil {
			return errAliasExists
		}
		return b.Put(keyHash, encrypted)
	})

	if errors.Is(err, errAliasExists) {
		writeJSON(w, http.StatusConflict, errorResponse{Detail: "Alias already exists"})
		return
	}
	if err != nil {
		log.Printf("ERROR: db insert failed for key %x: %v", keyHash, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Detail: "Internal Server Error"})
		return
	}

	writeJSON(w, http.StatusCreated, createResponse{ShortLink: "/" + alias})
}

// handleRedirect looks up alias in bbolt and issues a permanent redirect to
// the stored URL.  Returns 404 when the alias is unknown.
//
// Status codes:
//
//	308 — permanent redirect
//	404 — alias not found
//	500 — internal / database error
func (a *app) handleRedirect(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	keyHash := hashAlias(alias)

	var encrypted []byte
	err := a.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucketName)
		}
		val := b.Get(keyHash)
		if val == nil {
			return errAliasNotFound
		}
		// Copy because bbolt reuses the buffer after the transaction closes.
		encrypted = append([]byte(nil), val...)
		return nil
	})

	if errors.Is(err, errAliasNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Detail: "Not Found"})
		return
	}
	if err != nil {
		log.Printf("ERROR: db lookup failed for key %x: %v", keyHash, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Detail: "Internal Server Error"})
		return
	}

	url, err := decryptURL(a.secret, alias, encrypted)
	if err != nil {
		log.Printf("ERROR: decrypt failed for key %x: %v", keyHash, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Detail: "Internal Server Error"})
		return
	}

	http.Redirect(w, r, url, http.StatusPermanentRedirect)
}

// --- helpers ---

// writeJSON serialises v as JSON and writes it to w with the given status
// code.  Logs (but swallows) encoding errors to avoid leaking raw data.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("ERROR: JSON encode failed: %v", err)
	}
}
