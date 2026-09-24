package main

import (
	"crypto/sha256"
	"fmt"
	"log"

	bolt "go.etcd.io/bbolt"
)

// migrateLegacyDB converts old plaintext entries (key=alias, value=url) to the
// encrypted format (key=SHA-256(alias), value=AES-256-GCM(url)).  It runs once
// on startup, gated by a flag in a separate metadata bucket.
//
// Legacy entries are detected by key length: old aliases are 5–20 bytes, while
// SHA-256 hashes are always 32 bytes.  This distinction is reliable because the
// alias regex ([a-zA-Z0-9_]{5,20}) never produces a 32-byte key.
func migrateLegacyDB(db *bolt.DB, secret []byte) error {
	// --- 1. Check if migration already ran ---
	alreadyMigrated := false
	err := db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(metaBucket))
		if meta == nil {
			return nil // meta bucket doesn't exist yet — definitely not migrated
		}
		if v := meta.Get([]byte(migratedKey)); v != nil {
			alreadyMigrated = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("check migration status: %w", err)
	}
	if alreadyMigrated {
		return nil
	}

	// --- 2. Scan for legacy entries (keys not equal to 32 bytes) ---
	type entry struct{ alias, url string }
	var legacy []entry
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if len(k) != sha256.Size {
				legacy = append(legacy, entry{alias: string(k), url: string(v)})
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan legacy entries: %w", err)
	}

	if len(legacy) == 0 {
		// No legacy entries — just set the flag so we never scan again.
		return db.Update(func(tx *bolt.Tx) error {
			meta, err := tx.CreateBucketIfNotExists([]byte(metaBucket))
			if err != nil {
				return err
			}
			return meta.Put([]byte(migratedKey), []byte("1"))
		})
	}

	log.Printf("migrating %d legacy link(s) to encrypted format …", len(legacy))

	// --- 3. Rewrite each entry: delete old plaintext key, insert hashed+encrypted ---
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucketName)
		}
		for _, e := range legacy {
			if err := b.Delete([]byte(e.alias)); err != nil {
				return fmt.Errorf("delete legacy key %q: %w", e.alias, err)
			}
			encrypted, err := encryptURL(secret, e.alias, e.url)
			if err != nil {
				return fmt.Errorf("encrypt legacy alias %q: %w", e.alias, err)
			}
			if err := b.Put(hashAlias(e.alias), encrypted); err != nil {
				return fmt.Errorf("store migrated alias %q: %w", e.alias, err)
			}
		}
		// Mark migration complete in a separate metadata bucket.
		meta, err := tx.CreateBucketIfNotExists([]byte(metaBucket))
		if err != nil {
			return err
		}
		return meta.Put([]byte(migratedKey), []byte("1"))
	})
	if err != nil {
		return err
	}
	log.Printf("migration complete: %d link(s) converted", len(legacy))
	return nil
}
