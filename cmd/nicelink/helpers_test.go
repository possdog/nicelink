package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// newTestApp creates an app backed by a fresh temp bbolt database with the
// "urls" and "meta" buckets pre-created.  The secret is deterministic (bytes
// 0–31) so tests are reproducible.  The DB is cleaned up automatically.
func newTestApp(t *testing.T) *app {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.bolt")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketName)); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists([]byte(metaBucket))
		return err
	})
	if err != nil {
		t.Fatalf("create buckets: %v", err)
	}

	// Deterministic 32-byte secret for reproducible crypto tests.
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}

	return &app{db: db, secret: secret}
}

// newRequest creates a POST / request with the given query string for
// handleCreate tests.
func newCreateRequest(t *testing.T, queryString string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/"+queryString, nil)
	return req
}

// doCreate invokes handleCreate and returns the recorder.  Convenience wrapper
// to reduce boilerplate in handler tests.
func doCreate(t *testing.T, a *app, queryString string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.handleCreate(w, newCreateRequest(t, queryString))
	return w
}

// doRedirect invokes handleRedirect with the given alias path value and
// returns the recorder.
func doRedirect(t *testing.T, a *app, alias string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+alias, nil)
	req.SetPathValue("alias", alias)
	w := httptest.NewRecorder()
	a.handleRedirect(w, req)
	return w
}

// createLink is a helper that creates a short link via handleCreate and
// fails the test if the response is not 201.
func createLink(t *testing.T, a *app, queryString string) createResponse {
	t.Helper()
	w := doCreate(t, a, queryString)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp createResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}
