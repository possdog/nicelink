package main

import (
	"bytes"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// insertLegacyEntry writes a plaintext entry (key=alias, value=url) directly
// into the "urls" bucket, simulating a pre-encryption database.
func insertLegacyEntry(t *testing.T, db *bolt.DB, alias, url string) {
	t.Helper()
	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			t.Fatal("urls bucket not found")
		}
		return b.Put([]byte(alias), []byte(url))
	})
	if err != nil {
		t.Fatalf("insert legacy entry: %v", err)
	}
}

// assertBucketEntry retrieves the raw key/value pair for inspection.
func getRawEntry(t *testing.T, db *bolt.DB, key []byte) []byte {
	t.Helper()
	var val []byte
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			t.Fatal("urls bucket not found")
		}
		v := b.Get(key)
		if v != nil {
			val = append([]byte(nil), v...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("get raw entry: %v", err)
	}
	return val
}

func TestMigrateLegacyDB_NoLegacyEntries(t *testing.T) {
	a := newTestApp(t)
	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The migration flag should be set.
	if !isMigrated(t, a.db) {
		t.Fatal("migration flag not set after running on empty DB")
	}
}

func TestMigrateLegacyDB_WithLegacyEntries(t *testing.T) {
	a := newTestApp(t)

	// Insert legacy plaintext entries.
	insertLegacyEntry(t, a.db, "oldlink1", "http://example.com/1")
	insertLegacyEntry(t, a.db, "oldlink2", "http://example.com/2")

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Legacy plaintext keys should be gone.
	if val := getRawEntry(t, a.db, []byte("oldlink1")); val != nil {
		t.Fatal("legacy plaintext key still exists after migration")
	}
	if val := getRawEntry(t, a.db, []byte("oldlink2")); val != nil {
		t.Fatal("legacy plaintext key still exists after migration")
	}

	// Hashed keys should contain encrypted data (nonce || ciphertext).
	hashed1 := hashAlias("oldlink1")
	val := getRawEntry(t, a.db, hashed1)
	if val == nil {
		t.Fatal("hashed key not found after migration")
	}
	// Should be at least nonce (12) + tag (16) + some ciphertext.
	if len(val) < 28 {
		t.Fatalf("encrypted value too short: %d bytes", len(val))
	}

	// The migration flag should be set.
	if !isMigrated(t, a.db) {
		t.Fatal("migration flag not set after migration")
	}
}

func TestMigrateLegacyDB_PreservesData(t *testing.T) {
	// After migration, GET /{alias} should still redirect to the original URL.
	a := newTestApp(t)
	insertLegacyEntry(t, a.db, "oldlink", "http://preserved.example.com")

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	w := doRedirect(t, a, "oldlink")
	if w.Code != 308 {
		t.Fatalf("expected 308 after migration, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "http://preserved.example.com" {
		t.Fatalf("expected redirect to http://preserved.example.com, got %q", loc)
	}
}

func TestMigrateLegacyDB_AlreadyMigrated(t *testing.T) {
	a := newTestApp(t)

	// Run migration once (on empty DB — sets the flag).
	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	// Insert a legacy-style entry *after* the flag is set.  The second
	// migration should be a no-op and leave it untouched.
	insertLegacyEntry(t, a.db, "postmig", "http://example.com/post")

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// The legacy entry should still be there (migration was skipped).
	val := getRawEntry(t, a.db, []byte("postmig"))
	if val == nil {
		t.Fatal("legacy entry was unexpectedly migrated on second run")
	}
	if string(val) != "http://example.com/post" {
		t.Fatalf("legacy entry value changed: got %q", string(val))
	}
}

func TestMigrateLegacyDB_MixedEntries(t *testing.T) {
	// A DB with both legacy (plaintext) and already-encrypted (32-byte key)
	// entries: only the legacy ones should be migrated.
	a := newTestApp(t)

	// Legacy entry (plaintext key).
	insertLegacyEntry(t, a.db, "legacy1", "http://example.com/legacy")

	// Already-encrypted entry (32-byte hashed key).
	encrypted, err := encryptURL(a.secret, "enc1", "http://example.com/enc")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	hashedKey := hashAlias("enc1")
	if err := a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		return b.Put(hashedKey, encrypted)
	}); err != nil {
		t.Fatalf("insert encrypted entry: %v", err)
	}

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The legacy entry should now be encrypted under its hash.
	w := doRedirect(t, a, "legacy1")
	if w.Code != 308 {
		t.Fatalf("expected 308 for migrated legacy alias, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "http://example.com/legacy" {
		t.Fatalf("expected redirect to http://example.com/legacy, got %q", loc)
	}

	// The already-encrypted entry should still work.
	w = doRedirect(t, a, "enc1")
	if w.Code != 308 {
		t.Fatalf("expected 308 for already-encrypted alias, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "http://example.com/enc" {
		t.Fatalf("expected redirect to http://example.com/enc, got %q", loc)
	}

	if !isMigrated(t, a.db) {
		t.Fatal("migration flag not set")
	}
}

func TestMigrateLegacyDB_MultipleEntries(t *testing.T) {
	a := newTestApp(t)

	// Insert several legacy entries.
	urls := map[string]string{
		"link_a": "http://a.example.com",
		"link_b": "http://b.example.com",
		"link_c": "http://c.example.com",
		"link_d": "http://d.example.com",
		"link_e": "http://e.example.com",
	}
	for alias, url := range urls {
		insertLegacyEntry(t, a.db, alias, url)
	}

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// All entries should be redirectable.
	for alias, url := range urls {
		w := doRedirect(t, a, alias)
		if w.Code != 308 {
			t.Fatalf("expected 308 for %q, got %d", alias, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != url {
			t.Fatalf("expected redirect to %q, got %q", url, loc)
		}
	}

	if !isMigrated(t, a.db) {
		t.Fatal("migration flag not set")
	}
}

func TestMigrateLegacyDB_NoBucketsYet(t *testing.T) {
	// A fresh DB with no buckets at all — migration should handle this
	// gracefully (just set the flag).
	a := newTestApp(t)

	// Delete the buckets to simulate a truly fresh DB.
	_ = a.db.Update(func(tx *bolt.Tx) error {
		_ = tx.DeleteBucket([]byte(bucketName))
		_ = tx.DeleteBucket([]byte(metaBucket))
		return nil
	})

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate on empty DB: %v", err)
	}

	if !isMigrated(t, a.db) {
		t.Fatal("migration flag not set on empty DB")
	}
}

func TestMigrateLegacyDB_LegacyEntryAtBoundaryLengths(t *testing.T) {
	// Legacy aliases are 5–20 bytes. Test the boundary cases to verify the
	// key-length heuristic (len != 32 means legacy).
	a := newTestApp(t)

	// 5-byte alias (minimum valid alias length).
	insertLegacyEntry(t, a.db, "abcde", "http://five.example.com")
	// 20-byte alias (maximum valid alias length).
	insertLegacyEntry(t, a.db, "01234567890123456789", "http://twenty.example.com")

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Both should be redirectable.
	w := doRedirect(t, a, "abcde")
	if w.Code != 308 || w.Header().Get("Location") != "http://five.example.com" {
		t.Fatalf("5-byte alias: expected 308 → http://five.example.com, got %d → %q",
			w.Code, w.Header().Get("Location"))
	}

	w = doRedirect(t, a, "01234567890123456789")
	if w.Code != 308 || w.Header().Get("Location") != "http://twenty.example.com" {
		t.Fatalf("20-byte alias: expected 308 → http://twenty.example.com, got %d → %q",
			w.Code, w.Header().Get("Location"))
	}
}

// --- helpers ---

func isMigrated(t *testing.T, db *bolt.DB) bool {
	t.Helper()
	var migrated bool
	err := db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(metaBucket))
		if meta == nil {
			return nil
		}
		if v := meta.Get([]byte(migratedKey)); v != nil {
			migrated = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("check migration status: %v", err)
	}
	return migrated
}

// Ensure the migration doesn't leave plaintext data behind: after migration,
// the value stored under the hash key should NOT equal the plaintext URL.
func TestMigrateLegacyDB_NoPlaintextRemains(t *testing.T) {
	a := newTestApp(t)
	insertLegacyEntry(t, a.db, "secret_link", "http://secret.example.com/sensitive")

	if err := migrateLegacyDB(a.db, a.secret); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	val := getRawEntry(t, a.db, hashAlias("secret_link"))
	if val == nil {
		t.Fatal("no value found under hashed key")
	}
	if bytes.Equal(val, []byte("http://secret.example.com/sensitive")) {
		t.Fatal("plaintext URL is stored unencrypted after migration")
	}
}
