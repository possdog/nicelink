// Minimal link-shortener in Go: net/http + bbolt.
// Serves index.html (embedded), creates short links via POST /, and redirects
// via GET /{alias}.  Optionally serves HTTPS on :443 when domain.cert.pem and
// private.key.pem are present.
//
// Privacy model: neither aliases nor destination URLs are stored in plaintext.
// The bbolt key is SHA-256(alias) and the value is AES-256-GCM encrypted with
// a key derived from HMAC-SHA256(server-secret, alias).  Because the alias
// itself is never persisted, the only way to recover a destination is to know
// the alias and request GET /{alias}.  Losing secret.key makes every existing
// link permanently unrecoverable.

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bolt "go.etcd.io/bbolt"
)

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

	// Load (or generate) the server secret used for per-alias encryption.
	secret, err := loadOrCreateSecret()
	if err != nil {
		log.Fatalf("failed to load server secret: %v", err)
	}

	// One-time migration of any legacy plaintext entries to encrypted format.
	if err := migrateLegacyDB(db, secret); err != nil {
		log.Fatalf("failed to migrate legacy entries: %v", err)
	}

	srv := &app{db: db, secret: secret}

	// Build the mux using Go 1.22+ enhanced routing patterns.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", srv.serveIndex)
	mux.HandleFunc("POST /", srv.handleCreate)
	mux.HandleFunc("GET /{alias}", srv.handleRedirect)

	// Wrap the mux with per-IP rate limiting (golang.org/x/time/rate).
	rl := newRateLimiter()
	handler := rl.middleware(mux)

	// --- Signal-aware context for graceful shutdown ---
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	httpServer := &http.Server{
		Addr:    ":80",
		Handler: handler,
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
			Handler: handler,
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
