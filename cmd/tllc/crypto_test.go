package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- hashAlias ---

func TestHashAlias_Deterministic(t *testing.T) {
	h1 := hashAlias("testalias")
	h2 := hashAlias("testalias")
	if !bytes.Equal(h1, h2) {
		t.Fatal("hashAlias is not deterministic for the same input")
	}
}

func TestHashAlias_ReturnsSHA256Size(t *testing.T) {
	h := hashAlias("anything")
	if len(h) != sha256.Size {
		t.Fatalf("expected %d bytes, got %d", sha256.Size, len(h))
	}
}

func TestHashAlias_DifferentInputsProduceDifferentHashes(t *testing.T) {
	h1 := hashAlias("alias1")
	h2 := hashAlias("alias2")
	if bytes.Equal(h1, h2) {
		t.Fatal("different aliases produced the same hash")
	}
}

func TestHashAlias_EmptyString(t *testing.T) {
	// Should not panic; produces a valid SHA-256 of empty string.
	h := hashAlias("")
	if len(h) != sha256.Size {
		t.Fatalf("expected %d bytes for empty input, got %d", sha256.Size, len(h))
	}
	// Verify against the known SHA-256 of "".
	expected := sha256.Sum256([]byte(""))
	if !bytes.Equal(h, expected[:]) {
		t.Fatal("hashAlias(\"\") does not match sha256(\"\")")
	}
}

// --- deriveKey ---

func TestDeriveKey_Deterministic(t *testing.T) {
	secret := make([]byte, 32)
	k1 := deriveKey(secret, "alias")
	k2 := deriveKey(secret, "alias")
	if !bytes.Equal(k1, k2) {
		t.Fatal("deriveKey is not deterministic")
	}
}

func TestDeriveKey_DifferentAliasesProduceDifferentKeys(t *testing.T) {
	secret := make([]byte, 32)
	k1 := deriveKey(secret, "alias1")
	k2 := deriveKey(secret, "alias2")
	if bytes.Equal(k1, k2) {
		t.Fatal("different aliases produced the same derived key")
	}
}

func TestDeriveKey_DifferentSecretsProduceDifferentKeys(t *testing.T) {
	s1 := make([]byte, 32)
	s2 := make([]byte, 32)
	s2[0] = 1
	k1 := deriveKey(s1, "alias")
	k2 := deriveKey(s2, "alias")
	if bytes.Equal(k1, k2) {
		t.Fatal("different secrets produced the same derived key")
	}
}

func TestDeriveKey_Returns32Bytes(t *testing.T) {
	secret := make([]byte, 32)
	k := deriveKey(secret, "test")
	if len(k) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(k))
	}
}

// --- encryptURL / decryptURL round-trip ---

func TestEncryptDecryptURL_RoundTrip(t *testing.T) {
	secret := make([]byte, 32)
	cases := []struct {
		alias string
		url   string
	}{
		{"alias1", "http://example.com"},
		{"alias2", "https://example.com/path?query=value#fragment"},
		{"my_alias", "http://example.com:8080/path"},
		{"ABCDE", "https://a.b.c.d.e"},
		{"test123", "http://x"},
	}

	for _, tc := range cases {
		encrypted, err := encryptURL(secret, tc.alias, tc.url)
		if err != nil {
			t.Fatalf("encryptURL(%q, %q): %v", tc.alias, tc.url, err)
		}
		decrypted, err := decryptURL(secret, tc.alias, encrypted)
		if err != nil {
			t.Fatalf("decryptURL(%q, %q): %v", tc.alias, tc.url, err)
		}
		if decrypted != tc.url {
			t.Fatalf("round-trip mismatch: got %q, want %q", decrypted, tc.url)
		}
	}
}

func TestEncryptDecryptURL_EmptyURL(t *testing.T) {
	secret := make([]byte, 32)
	encrypted, err := encryptURL(secret, "alias", "")
	if err != nil {
		t.Fatalf("encryptURL: %v", err)
	}
	decrypted, err := decryptURL(secret, "alias", encrypted)
	if err != nil {
		t.Fatalf("decryptURL: %v", err)
	}
	if decrypted != "" {
		t.Fatalf("expected empty URL, got %q", decrypted)
	}
}

func TestEncryptURL_NonceUniqueness(t *testing.T) {
	// Two encryptions of the same (alias, url) should produce different
	// ciphertexts because the nonce is random.
	secret := make([]byte, 32)
	ct1, _ := encryptURL(secret, "alias", "http://example.com")
	ct2, _ := encryptURL(secret, "alias", "http://example.com")
	if bytes.Equal(ct1, ct2) {
		t.Fatal("two encryptions of the same input produced identical ciphertext (nonce not random)")
	}
}

func TestEncryptURL_CiphertextStartsWithNonce(t *testing.T) {
	// The format is nonce || ciphertext, so the first 12 bytes (GCM nonce
	// size) should differ between two encryptions of the same input.
	secret := make([]byte, 32)
	ct1, _ := encryptURL(secret, "alias", "http://example.com")
	ct2, _ := encryptURL(secret, "alias", "http://example.com")
	if len(ct1) < 12 || len(ct2) < 12 {
		t.Fatalf("ciphertext too short: %d / %d", len(ct1), len(ct2))
	}
	if bytes.Equal(ct1[:12], ct2[:12]) {
		t.Fatal("nonce (first 12 bytes) is the same across encryptions")
	}
}

// --- decryptURL failure cases ---

func TestDecryptURL_WrongAlias(t *testing.T) {
	secret := make([]byte, 32)
	encrypted, _ := encryptURL(secret, "alias1", "http://example.com")
	_, err := decryptURL(secret, "alias2", encrypted)
	if err == nil {
		t.Fatal("expected decryption to fail with wrong alias, but it succeeded")
	}
}

func TestDecryptURL_WrongSecret(t *testing.T) {
	s1 := make([]byte, 32)
	s2 := make([]byte, 32)
	s2[0] = 1
	encrypted, _ := encryptURL(s1, "alias", "http://example.com")
	_, err := decryptURL(s2, "alias", encrypted)
	if err == nil {
		t.Fatal("expected decryption to fail with wrong secret, but it succeeded")
	}
}

func TestDecryptURL_TamperedCiphertext(t *testing.T) {
	secret := make([]byte, 32)
	encrypted, _ := encryptURL(secret, "alias", "http://example.com")
	// Flip a byte in the ciphertext body (after the nonce).
	tampered := make([]byte, len(encrypted))
	copy(tampered, encrypted)
	tampered[len(tampered)-1] ^= 0x01
	_, err := decryptURL(secret, "alias", tampered)
	if err == nil {
		t.Fatal("expected decryption to fail with tampered ciphertext, but it succeeded")
	}
}

func TestDecryptURL_TamperedNonce(t *testing.T) {
	secret := make([]byte, 32)
	encrypted, _ := encryptURL(secret, "alias", "http://example.com")
	// Flip a byte in the nonce (first 12 bytes).
	tampered := make([]byte, len(encrypted))
	copy(tampered, encrypted)
	tampered[0] ^= 0x01
	_, err := decryptURL(secret, "alias", tampered)
	if err == nil {
		t.Fatal("expected decryption to fail with tampered nonce, but it succeeded")
	}
}

func TestDecryptURL_ShortCiphertext(t *testing.T) {
	secret := make([]byte, 32)
	// GCM nonce size is 12 bytes; anything shorter should fail.
	_, err := decryptURL(secret, "alias", []byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("expected error for ciphertext shorter than nonce, got nil")
	}
}

func TestDecryptURL_EmptyCiphertext(t *testing.T) {
	secret := make([]byte, 32)
	_, err := decryptURL(secret, "alias", []byte{})
	if err == nil {
		t.Fatal("expected error for empty ciphertext, got nil")
	}
}

func TestDecryptURL_NonceOnlyNoCiphertext(t *testing.T) {
	// Exactly 12 bytes (nonce size) — valid nonce but empty ciphertext.
	// GCM should reject this because there's no authentication tag.
	secret := make([]byte, 32)
	_, err := decryptURL(secret, "alias", make([]byte, 12))
	if err == nil {
		t.Fatal("expected error for nonce-only ciphertext, got nil")
	}
}

// --- loadOrCreateSecret ---

func TestLoadOrCreateSecret_CreatesNew(t *testing.T) {
	t.Chdir(t.TempDir())
	secret, err := loadOrCreateSecret()
	if err != nil {
		t.Fatalf("loadOrCreateSecret: %v", err)
	}
	if len(secret) != 32 {
		t.Fatalf("expected 32-byte secret, got %d", len(secret))
	}
	// Verify the file was written.
	data, err := os.ReadFile(secretName)
	if err != nil {
		t.Fatalf("secret file not created: %v", err)
	}
	if len(data) != 32 {
		t.Fatalf("secret file is %d bytes, expected 32", len(data))
	}
	if !bytes.Equal(data, secret) {
		t.Fatal("file content does not match returned secret")
	}
	// File should have 0600 permissions.
	info, err := os.Stat(secretName)
	if err != nil {
		t.Fatalf("stat secret: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}
}

func TestLoadOrCreateSecret_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	existing := bytes.Repeat([]byte{0xAB}, 32)
	if err := os.WriteFile(filepath.Join(dir, secretName), existing, 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	secret, err := loadOrCreateSecret()
	if err != nil {
		t.Fatalf("loadOrCreateSecret: %v", err)
	}
	if !bytes.Equal(secret, existing) {
		t.Fatal("loaded secret does not match file content")
	}
}

func TestLoadOrCreateSecret_WrongSize(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Write a secret that is too short.
	if err := os.WriteFile(filepath.Join(dir, secretName), make([]byte, 16), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	_, err := loadOrCreateSecret()
	if err == nil {
		t.Fatal("expected error for wrong-size secret, got nil")
	}
}

func TestLoadOrCreateSecret_TooLong(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, secretName), make([]byte, 64), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	_, err := loadOrCreateSecret()
	if err == nil {
		t.Fatal("expected error for over-long secret, got nil")
	}
}

// --- randomAlias ---

func TestRandomAlias_Length(t *testing.T) {
	alias := randomAlias()
	if len(alias) != 5 {
		t.Fatalf("expected 5-char alias, got %d (%q)", len(alias), alias)
	}
}

func TestRandomAlias_ValidCharset(t *testing.T) {
	for range 1000 {
		alias := randomAlias()
		for _, c := range alias {
			if !strings.ContainsRune(aliasCharset, c) {
				t.Fatalf("character %q not in valid charset (alias %q)", c, alias)
			}
		}
	}
}

func TestRandomAlias_GeneratesVariety(t *testing.T) {
	// Generate many aliases; they should not all be the same (extremely
	// unlikely with 62^5 ≈ 916M possibilities).
	seen := make(map[string]bool)
	for range 1000 {
		seen[randomAlias()] = true
	}
	if len(seen) < 2 {
		t.Fatalf("randomAlias produced only %d distinct values in 1000 calls", len(seen))
	}
}
