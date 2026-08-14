package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// --- serveIndex ---

func TestServeIndex_ReturnsHTML(t *testing.T) {
	a := newTestApp(t)
	w := httptest.NewRecorder()
	a.serveIndex(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "link shortener") {
		t.Fatal("response body does not contain expected HTML content")
	}
}

// --- handleCreate: URL validation ---

func TestHandleCreate_ValidHTTP(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com")
	if !strings.HasPrefix(resp.ShortLink, "/") {
		t.Fatalf("unexpected short link: %q", resp.ShortLink)
	}
}

func TestHandleCreate_ValidHTTPS(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=https://example.com/path?q=1")
	if !strings.HasPrefix(resp.ShortLink, "/") {
		t.Fatalf("unexpected short link: %q", resp.ShortLink)
	}
}

func TestHandleCreate_URLWithPort(t *testing.T) {
	a := newTestApp(t)
	createLink(t, a, "?url=http://example.com:8080/path")
}

func TestHandleCreate_URLWithFragment(t *testing.T) {
	a := newTestApp(t)
	createLink(t, a, "?url=http://example.com/path#section")
}

func TestHandleCreate_ValidLongURL(t *testing.T) {
	a := newTestApp(t)
	longURL := "http://example.com/" + strings.Repeat("a", 2000)
	createLink(t, a, "?url="+longURL)
}

// --- handleCreate: URL rejection ---

func TestHandleCreate_MissingURL(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleCreate_EmptyURL(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleCreate_WhitespaceURL(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=%20%20%20")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for whitespace-only URL, got %d", w.Code)
	}
}

func TestHandleCreate_URLTrimmed(t *testing.T) {
	// Whitespace around a valid URL should be trimmed and accepted.
	a := newTestApp(t)
	resp := createLink(t, a, "?url=%20http://example.com%20")
	if !strings.HasPrefix(resp.ShortLink, "/") {
		t.Fatalf("unexpected short link: %q", resp.ShortLink)
	}
}

func TestHandleCreate_InvalidScheme_FTP(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=ftp://example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ftp:// URL, got %d", w.Code)
	}
}

func TestHandleCreate_InvalidScheme_JavaScript(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=javascript:alert(1)")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for javascript: URL, got %d", w.Code)
	}
}

func TestHandleCreate_InvalidScheme_File(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=file:///etc/passwd")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for file:// URL, got %d", w.Code)
	}
}

func TestHandleCreate_InvalidScheme_Data(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=data:text/html,<script>alert(1)</script>")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for data: URL, got %d", w.Code)
	}
}

func TestHandleCreate_UppercaseScheme(t *testing.T) {
	// The regex only accepts lowercase http/https.
	a := newTestApp(t)
	w := doCreate(t, a, "?url=HTTP://example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for uppercase scheme, got %d", w.Code)
	}
}

func TestHandleCreate_URLWithSpace(t *testing.T) {
	// Space is outside the [\x21-\x7E] range and should be rejected.
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com/a%20b")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for URL containing space, got %d", w.Code)
	}
}

func TestHandleCreate_URLWithNewline(t *testing.T) {
	// CRLF injection attempt — newline is outside printable range.
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com/%0D%0ASet-Cookie:evil=1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for URL with CRLF, got %d", w.Code)
	}
}

func TestHandleCreate_URLWithNullByte(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com/%00")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for URL with null byte, got %d", w.Code)
	}
}

func TestHandleCreate_URLSchemeOnly(t *testing.T) {
	// "http://" with nothing after the // — regex requires at least one
	// printable character after ://.
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for scheme-only URL, got %d", w.Code)
	}
}

func TestHandleCreate_NoScheme(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for URL without scheme, got %d", w.Code)
	}
}

// --- handleCreate: alias validation ---

func TestHandleCreate_CustomAlias(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias=mylink")
	if resp.ShortLink != "/mylink" {
		t.Fatalf("expected /mylink, got %q", resp.ShortLink)
	}
}

func TestHandleCreate_AliasBoundaryMin5(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias=abcde")
	if resp.ShortLink != "/abcde" {
		t.Fatalf("expected /abcde, got %q", resp.ShortLink)
	}
}

func TestHandleCreate_AliasBoundaryMax20(t *testing.T) {
	alias := strings.Repeat("a", 20)
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias="+alias)
	if resp.ShortLink != "/"+alias {
		t.Fatalf("expected /%s, got %q", alias, resp.ShortLink)
	}
}

func TestHandleCreate_AliasTooShort4(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=abcd")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for 4-char alias, got %d", w.Code)
	}
}

func TestHandleCreate_AliasTooLong21(t *testing.T) {
	alias := strings.Repeat("a", 21)
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias="+alias)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for 21-char alias, got %d", w.Code)
	}
}

func TestHandleCreate_AliasWithDash(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=my-link")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for alias with dash, got %d", w.Code)
	}
}

func TestHandleCreate_AliasWithDot(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=my.link")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for alias with dot, got %d", w.Code)
	}
}

func TestHandleCreate_AliasWithSpecialChars(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=p@ssw0rd")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for alias with @, got %d", w.Code)
	}
}

func TestHandleCreate_AliasSQLInjection(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=%27%3B%20DROP%20TABLE--")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for SQL injection alias, got %d", w.Code)
	}
}

func TestHandleCreate_AliasPathTraversal(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=../../../etc/passwd")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal alias, got %d", w.Code)
	}
}

func TestHandleCreate_AliasWithUnicode(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=c%C3%A9f%C3%A9")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for Unicode alias, got %d", w.Code)
	}
}

func TestHandleCreate_AliasEmpty(t *testing.T) {
	// An empty alias param is treated as "no alias" — the handler
	// auto-generates one and returns 201.
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias=")
	if !strings.HasPrefix(resp.ShortLink, "/") {
		t.Fatalf("expected auto-generated short link, got %q", resp.ShortLink)
	}
	// Should NOT be "/".
	if resp.ShortLink == "/" {
		t.Fatal("expected a non-empty auto-generated alias")
	}
}

func TestHandleCreate_AliasWhitespaceOnly(t *testing.T) {
	// A whitespace-only alias is trimmed to empty before the emptiness
	// check, so it is treated as "no alias provided" and auto-generated.
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias=%20%20")
	if !strings.HasPrefix(resp.ShortLink, "/") {
		t.Fatalf("expected auto-generated short link, got %q", resp.ShortLink)
	}
	alias := strings.TrimPrefix(resp.ShortLink, "/")
	if !aliasRegex.MatchString(alias) {
		t.Fatalf("auto-generated alias %q does not match regex", alias)
	}
}

func TestHandleCreate_AliasTrimmed(t *testing.T) {
	// Whitespace around a valid alias should be trimmed and accepted.
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias=%20mylink%20")
	if resp.ShortLink != "/mylink" {
		t.Fatalf("expected /mylink after trimming, got %q", resp.ShortLink)
	}
}

func TestHandleCreate_AliasWithUnderscore(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com&alias=my_link_01")
	if resp.ShortLink != "/my_link_01" {
		t.Fatalf("expected /my_link_01, got %q", resp.ShortLink)
	}
}

// --- handleCreate: duplicate alias ---

func TestHandleCreate_DuplicateAlias(t *testing.T) {
	a := newTestApp(t)
	createLink(t, a, "?url=http://example.com&alias=mylink")
	w := doCreate(t, a, "?url=http://other.com&alias=mylink")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate alias, got %d", w.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(strings.ToLower(body.Detail), "exists") {
		t.Fatalf("expected 'exists' in detail, got %q", body.Detail)
	}
}

func TestHandleCreate_DuplicateAliasDoesNotOverwrite(t *testing.T) {
	// Verify that attempting to re-create an existing alias does not change
	// the stored destination URL.
	a := newTestApp(t)
	createLink(t, a, "?url=http://first.com&alias=mylink")
	doCreate(t, a, "?url=http://second.com&alias=mylink")

	w := doRedirect(t, a, "mylink")
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected 308, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "http://first.com" {
		t.Fatalf("expected redirect to http://first.com, got %q", loc)
	}
}

// --- handleCreate: auto-generated alias ---

func TestHandleCreate_AutoGeneratedAlias(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com")
	// Auto-generated aliases are 5 chars from the alphanumeric charset.
	if len(resp.ShortLink) != 6 { // 5 chars + leading "/"
		t.Fatalf("expected 6-char short link (5 alias + /), got %q", resp.ShortLink)
	}
	alias := strings.TrimPrefix(resp.ShortLink, "/")
	if !aliasRegex.MatchString(alias) {
		t.Fatalf("auto-generated alias %q does not match alias regex", alias)
	}
}

func TestHandleCreate_AutoGeneratedAliasIsRedirectable(t *testing.T) {
	a := newTestApp(t)
	resp := createLink(t, a, "?url=http://example.com")
	alias := strings.TrimPrefix(resp.ShortLink, "/")
	w := doRedirect(t, a, alias)
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected 308 for auto-generated alias, got %d", w.Code)
	}
	if w.Header().Get("Location") != "http://example.com" {
		t.Fatalf("expected redirect to http://example.com, got %q", w.Header().Get("Location"))
	}
}

// --- handleCreate: response format ---

func TestHandleCreate_ResponseTypeJSON(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=mylink")
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json content type, got %q", ct)
	}
}

func TestHandleCreate_BadRequestResponseTypeJSON(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=invalid")
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json content type for error, got %q", ct)
	}
}

func TestHandleCreate_StatusCreated(t *testing.T) {
	a := newTestApp(t)
	w := doCreate(t, a, "?url=http://example.com&alias=mylink")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", w.Code)
	}
}

// --- handleRedirect ---

func TestHandleRedirect_KnownAlias(t *testing.T) {
	a := newTestApp(t)
	createLink(t, a, "?url=http://example.com&alias=mylink")
	w := doRedirect(t, a, "mylink")
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected 308, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "http://example.com" {
		t.Fatalf("expected redirect to http://example.com, got %q", loc)
	}
}

func TestHandleRedirect_UnknownAlias(t *testing.T) {
	a := newTestApp(t)
	w := doRedirect(t, a, "nonexistent")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(strings.ToLower(body.Detail), "not found") {
		t.Fatalf("expected 'not found' in detail, got %q", body.Detail)
	}
}

func TestHandleRedirect_KnownAliasDifferentURL(t *testing.T) {
	a := newTestApp(t)
	createLink(t, a, "?url=https://github.com/golang/go&alias=golang")
	w := doRedirect(t, a, "golang")
	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("expected 308, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://github.com/golang/go" {
		t.Fatalf("expected redirect to https://github.com/golang/go, got %q", loc)
	}
}

func TestHandleRedirect_DecryptFailure(t *testing.T) {
	// Corrupt the stored value so decryption fails → 500.
	a := newTestApp(t)
	createLink(t, a, "?url=http://example.com&alias=mylink")

	// Overwrite the stored value with garbage that is long enough to pass
	// the nonce-length check but will fail GCM authentication.
	overwriteStoredValue(t, a, "mylink", []byte("this_is_definitely_not_valid_ciphertext_data!!"))

	w := doRedirect(t, a, "mylink")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for corrupt stored value, got %d", w.Code)
	}
}

func TestHandleRedirect_WrongSecret(t *testing.T) {
	// If the server secret changes after links were created, decryption
	// should fail → 500.
	a := newTestApp(t)
	createLink(t, a, "?url=http://example.com&alias=mylink")

	// Swap the secret to a different value.
	newSecret := make([]byte, 32)
	for i := range newSecret {
		newSecret[i] = byte(255 - i)
	}
	a.secret = newSecret

	w := doRedirect(t, a, "mylink")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for wrong secret, got %d", w.Code)
	}
}

// overwriteStoredValue replaces the encrypted value for the given alias with
// arbitrary data, simulating database corruption.
func overwriteStoredValue(t *testing.T, a *app, alias string, value []byte) {
	t.Helper()
	keyHash := hashAlias(alias)
	err := a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}
		return b.Put(keyHash, value)
	})
	if err != nil {
		t.Fatalf("overwrite stored value: %v", err)
	}
}
