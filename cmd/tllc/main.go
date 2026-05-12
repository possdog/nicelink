// Minimal link-shortener in Go: net/http + bbolt.
// Serves index.html (embedded), creates short links via POST /, and redirects
// via GET /{alias}.  Optionally serves HTTPS on :443 when domain.cert.pem and
// private.key.pem are present.

package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Static assets embedded into the binary at build time.
//
//go:embed index.html
var staticFiles embed.FS

// Database constants.
const (
	dbName     = "link_shortener.bolt"
	bucketName = "urls"
)

// Pre-compiled validation regexes — the same rules as the Rust original.
//
// urlRegex   matches http or https URLs containing only printable non-space
//            characters ([\x21-\x7e] is the ASCII printable range minus space).
// aliasRegex matches 5–20 characters from [a-zA-Z0-9_].
var (
	urlRegex   = regexp.MustCompile(`^https?://[\x21-\x7E]+$`)
	aliasRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{5,20}$`)
)

// Alphanumeric charset used for auto-generated short aliases.
const aliasCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// app bundles the shared bbolt database handle and implements http.Handler.
type app struct {
	db *bolt.DB
}

// --- request / response types (JSON) ---

type createResponse struct {
	ShortLink string `json:"short_link"`
}

type errorResponse struct {
	Detail string `json:"detail"`
}

// randomAlias generates a cryptographically random 5-character alphanumeric
// string.  Falls back to a timestamp-based suffix if the system RNG fails,
// which is vanishingly unlikely but prevents a silent broken state.
func randomAlias() string {
	const length = 5
	b := make([]byte, length)
	charsetLen := big.NewInt(int64(len(aliasCharset)))
	for i := range b {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			// crypto/rand failures are exceptionally rare; log and escape
			// gracefully so the caller still gets *some* alias.
			log.Printf("WARNING: crypto/rand read failed: %v", err)
			// Use a per-rune fallback that is deterministic but
			// unlikely to collide in practice.
			b[i] = aliasCharset[i%len(aliasCharset)]
			continue
		}
		b[i] = aliasCharset[n.Int64()]
	}
	return string(b)
}

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
	var alias string
	if rawAlias := r.URL.Query().Get("alias"); rawAlias != "" {
		alias = strings.TrimSpace(rawAlias)
		if alias == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Detail: "Alias cannot be empty"})
			return
		}
		if !aliasRegex.MatchString(alias) {
			writeJSON(w, http.StatusBadRequest,
				errorResponse{Detail: "Invalid alias format. Must be 5-20 alphanumeric characters or underscores"})
			return
		}
	} else {
		alias = randomAlias()
	}

	// --- 3. Atomically check existence & insert ---
	err := a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucketName)
		}
		// Bail if the alias is already taken.
		if existing := b.Get([]byte(alias)); existing != nil {
			return errAliasExists
		}
		return b.Put([]byte(alias), []byte(url))
	})

	if errors.Is(err, errAliasExists) {
		writeJSON(w, http.StatusConflict, errorResponse{Detail: "Alias already exists"})
		return
	}
	if err != nil {
		log.Printf("ERROR: db insert failed for alias %q: %v", alias, err)
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

	var url string
	err := a.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucketName)
		}
		val := b.Get([]byte(alias))
		if val == nil {
			return errAliasNotFound
		}
		url = string(val)
		return nil
	})

	if errors.Is(err, errAliasNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Detail: "Not Found"})
		return
	}
	if err != nil {
		log.Printf("ERROR: db lookup failed for alias %q: %v", alias, err)
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

// --- sentinel errors for control flow inside bbolt transactions ---

var (
	errAliasExists   = errors.New("alias exists")
	errAliasNotFound = errors.New("alias not found")
)

// --- main ---

func main() {
	// Ensure the bbolt bucket exists before we start serving.
	db, err := bolt.Open(dbName, 0600, nil)
	if err != nil {
		log.Fatalf("failed to open database %q: %v", dbName, err)
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})
	if err != nil {
		log.Fatalf("failed to initialise bucket %q: %v", bucketName, err)
	}

	srv := &app{db: db}

	// Build the mux using Go 1.22+ enhanced routing patterns.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", srv.serveIndex)
	mux.HandleFunc("POST /", srv.handleCreate)
	mux.HandleFunc("GET /{alias}", srv.handleRedirect)

	// --- Signal-aware context for graceful shutdown ---
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	httpServer := &http.Server{
		Addr:    ":80",
		Handler: mux,
	}

	// Start HTTP in a goroutine; a fatal listen failure terminates the process.
	go func() {
		log.Printf("HTTP listening on %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Conditionally start HTTPS if both certificate files exist.
	var httpsServer *http.Server
	if fileExists("domain.cert.pem") && fileExists("private.key.pem") {
		httpsServer = &http.Server{
			Addr:    ":443",
			Handler: mux,
		}
		go func() {
			log.Printf("HTTPS listening on %s", httpsServer.Addr)
			err := httpsServer.ListenAndServeTLS("domain.cert.pem", "private.key.pem")
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("HTTPS server error: %v", err)
			}
		}()
	} else {
		log.Println("HTTPS skipped — TLS certificate files not found")
	}

	// Block until the OS sends a termination signal.
	<-ctx.Done()
	log.Println("shutting down gracefully …")

	// Give outstanding requests a few seconds to drain.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	if httpsServer != nil {
		if err := httpsServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTPS shutdown error: %v", err)
		}
	}
	log.Println("servers stopped")
}

// fileExists reports whether the named file exists and is accessible.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
