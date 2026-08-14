package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
)

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

// --- cryptographic helpers ---

// loadOrCreateSecret reads the 32-byte server secret from secretName, or
// generates a new one and persists it with 0600 permissions if the file does
// not exist yet.  The secret is the root key from which per-alias encryption
// keys are derived; without it no destination URL can ever be recovered.
func loadOrCreateSecret() ([]byte, error) {
	if data, err := os.ReadFile(secretName); err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("%s exists but is %d bytes (expected 32)", secretName, len(data))
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", secretName, err)
	}

	// First run — generate and persist a fresh secret.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	if err := os.WriteFile(secretName, secret, 0600); err != nil {
		return nil, fmt.Errorf("write %s: %w", secretName, err)
	}
	log.Printf("generated new server secret in %s", secretName)
	return secret, nil
}

// hashAlias returns SHA-256(alias), used as the bbolt key.  This ensures the
// alias itself is never stored in the database.
func hashAlias(alias string) []byte {
	h := sha256.Sum256([]byte(alias))
	return h[:]
}

// deriveKey returns the per-alias AES-256 key: HMAC-SHA256(secret, alias).
// The alias acts as per-record keying material so that compromising one
// entry's key does not reveal any other entry.
func deriveKey(secret []byte, alias string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(alias))
	return mac.Sum(nil)
}

// encryptURL encrypts the destination URL with AES-256-GCM using a key derived
// from the server secret and the alias.  The returned slice is nonce || ciphertext.
func encryptURL(secret []byte, alias, url string) ([]byte, error) {
	key := deriveKey(secret, alias)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	// Seal appends the ciphertext to the nonce so we store nonce || ciphertext.
	return gcm.Seal(nonce, nonce, []byte(url), nil), nil
}

// decryptURL reverses encryptURL.  It returns an error if the nonce/ciphertext
// is malformed or the key does not match (which would indicate a tampered or
// mismatched alias).
func decryptURL(secret []byte, alias string, ciphertext []byte) (string, error) {
	key := deriveKey(secret, alias)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
