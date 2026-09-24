package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- clientIP ---

func TestClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	ip := clientIP(req)
	if ip != "192.168.1.100" {
		t.Fatalf("expected 192.168.1.100, got %q", ip)
	}
}

func TestClientIP_RemoteAddrWithoutPort(t *testing.T) {
	// If RemoteAddr has no port, SplitHostPort fails and we fall back to
	// the raw RemoteAddr.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1"
	ip := clientIP(req)
	if ip != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %q", ip)
	}
}

func TestClientIP_RemoteAddrIPv6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:12345"
	ip := clientIP(req)
	if ip != "::1" {
		t.Fatalf("expected ::1, got %q", ip)
	}
}

func TestClientIP_XRealIP_Ignored(t *testing.T) {
	// X-Real-IP is not trusted: the server is directly exposed, so only
	// r.RemoteAddr is used.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("X-Real-IP", "203.0.113.5")
	ip := clientIP(req)
	if ip != "192.168.1.100" {
		t.Fatalf("expected 192.168.1.100 (X-Real-IP should be ignored), got %q", ip)
	}
}

func TestClientIP_XForwardedFor_Ignored(t *testing.T) {
	// X-Forwarded-For is not trusted for the same reason as X-Real-IP.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	ip := clientIP(req)
	if ip != "192.168.1.100" {
		t.Fatalf("expected 192.168.1.100 (X-Forwarded-For should be ignored), got %q", ip)
	}
}

func TestClientIP_BothHeadersIgnored(t *testing.T) {
	// When both X-Real-IP and X-Forwarded-For are present, neither is used.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Real-IP", "203.0.113.5")
	ip := clientIP(req)
	if ip != "192.168.1.100" {
		t.Fatalf("expected 192.168.1.100 (both headers should be ignored), got %q", ip)
	}
}

// --- proxy support ---

func TestClientIP_Localhost_UsesXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Fatalf("expected 203.0.113.5 from X-Forwarded-For, got %q", ip)
	}
}

func TestClientIP_Localhost_UsesXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Real-IP", "203.0.113.5")
	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Fatalf("expected 203.0.113.5 from X-Real-IP, got %q", ip)
	}
}

func TestClientIP_Localhost_FallsBackToRemoteAddr(t *testing.T) {
	// No forwarded headers: fall back to RemoteAddr host.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	ip := clientIP(req)
	if ip != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1 (fallback), got %q", ip)
	}
}

func TestClientIP_Localhost_XForwardedForInvalidIP(t *testing.T) {
	// Invalid IP in X-Forwarded-For should fall back to X-Real-IP or RemoteAddr.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	req.Header.Set("X-Real-IP", "203.0.113.5")
	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Fatalf("expected 203.0.113.5 from X-Real-IP (XFF invalid), got %q", ip)
	}
}

func TestClientIP_Localhost_BothHeadersInvalid(t *testing.T) {
	// Both headers invalid: fall back to RemoteAddr.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	req.Header.Set("X-Real-IP", "also-not-an-ip")
	ip := clientIP(req)
	if ip != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1 (both headers invalid), got %q", ip)
	}
}

func TestClientIP_IPv6Localhost_UsesXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:12345"
	req.Header.Set("X-Forwarded-For", "2001:db8::1")
	ip := clientIP(req)
	if ip != "2001:db8::1" {
		t.Fatalf("expected 2001:db8::1 from X-Forwarded-For, got %q", ip)
	}
}

func TestClientIP_NonLocalhost_IgnoresHeaders(t *testing.T) {
	// Non-localhost RemoteAddr: headers are ignored.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	ip := clientIP(req)
	if ip != "192.168.1.1" {
		t.Fatalf("expected 192.168.1.1 (headers ignored), got %q", ip)
	}
}

func TestClientIP_XForwardedFor_MultipleIPs(t *testing.T) {
	// X-Forwarded-For may contain a chain; leftmost is the client.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.1, 192.0.2.1")
	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Fatalf("expected 203.0.113.5 (first IP), got %q", ip)
	}
}

func TestClientIP_XForwardedFor_MultipleIPsWithSpaces(t *testing.T) {
	// X-Forwarded-For may have spaces around commas.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.1")
	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Fatalf("expected 203.0.113.5 (first IP with spaces), got %q", ip)
	}
}

// --- rateLimiter ---

func TestRateLimiter_AllowsWithinBurst(t *testing.T) {
	rl := newRateLimiter()
	ip := "192.168.1.1"
	for i := range defaultBurst {
		if !rl.allow(ip) {
			t.Fatalf("request %d within burst was denied", i+1)
		}
	}
}

func TestRateLimiter_BlocksAfterBurst(t *testing.T) {
	rl := newRateLimiter()
	ip := "192.168.1.1"
	for range defaultBurst {
		rl.allow(ip)
	}
	if rl.allow(ip) {
		t.Fatal("request after burst was allowed (should have been denied)")
	}
}

func TestRateLimiter_DifferentIPsSeparate(t *testing.T) {
	rl := newRateLimiter()
	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Exhaust ip1's burst.
	for range defaultBurst {
		rl.allow(ip1)
	}
	if rl.allow(ip1) {
		t.Fatal("ip1 should be rate-limited")
	}
	// ip2 should still be allowed.
	if !rl.allow(ip2) {
		t.Fatal("ip2 should not be rate-limited")
	}
}

func TestRateLimiter_SameIPLimitedAcrossCalls(t *testing.T) {
	// Verify that the same IP is tracked across multiple allow() calls.
	rl := newRateLimiter()
	ip := "10.0.0.1"

	// Use half the burst.
	for range defaultBurst / 2 {
		if !rl.allow(ip) {
			t.Fatal("denied within burst")
		}
	}
	// Should still have remaining tokens.
	if !rl.allow(ip) {
		t.Fatal("denied with remaining tokens")
	}
}

// --- middleware ---

func TestRateLimiter_Middleware_PassesThrough(t *testing.T) {
	rl := newRateLimiter()
	called := false
	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("handler was not called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRateLimiter_Middleware_Returns429(t *testing.T) {
	rl := newRateLimiter()
	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Exhaust the burst.
	for range defaultBurst {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request within burst returned %d", w.Code)
		}
	}

	// Next request should be 429 and the downstream handler should not run.
	called := false
	wrapped := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst, got %d", w.Code)
	}
	if called {
		t.Fatal("downstream handler should not have been called on rate-limited request")
	}
}

func TestRateLimiter_Middleware_DifferentIPsIndependent(t *testing.T) {
	rl := newRateLimiter()
	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "192.168.1.1:12345"
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.168.1.2:12345"

	// Exhaust IP1.
	for range defaultBurst {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req1)
	}

	// IP2 should still be allowed.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req2)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for different IP, got %d", w.Code)
	}

	// IP1 should be blocked.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req1)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for exhausted IP, got %d", w.Code)
	}
}

func TestRateLimiter_Middleware_XForwardedForDoesNotBypass(t *testing.T) {
	// A client cannot bypass rate limiting by spoofing X-Forwarded-For with
	// a different value on each request.  Both requests share the same
	// r.RemoteAddr, so they share the same rate-limit bucket.
	rl := newRateLimiter()
	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "192.168.1.1:12345"
	req1.Header.Set("X-Forwarded-For", "203.0.113.5")

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.168.1.1:12345"
	req2.Header.Set("X-Forwarded-For", "203.0.113.6")

	// Exhaust the rate limit via req1.
	for range defaultBurst {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req1)
	}

	// req2 has a different XFF but the same RemoteAddr — it should be
	// rate-limited, not treated as a new client.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req2)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 (XFF spoofing should not bypass rate limit), got %d", w.Code)
	}
}
