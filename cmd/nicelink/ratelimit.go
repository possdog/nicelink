package main

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// Per-client rate limiting defaults.  Each client IP gets its own token-bucket
// limiter; requests beyond the burst are rejected with 429.
const (
	defaultRate  = rate.Limit(5) // 5 requests/sec sustained
	defaultBurst = 10            // allow short bursts of up to 10
)

// ipLimiter wraps a per-IP rate.Limiter.
type ipLimiter struct {
	limiter *rate.Limiter
}

// rateLimiter is a per-client-IP rate limiter backed by golang.org/x/time/rate.
// It uses a token-bucket algorithm: each IP accumulates tokens at defaultRate
// per second up to defaultBurst.  Requests that arrive when the bucket is
// empty receive 429 Too Many Requests.
type rateLimiter struct {
	mu  sync.Mutex
	ips map[string]*ipLimiter
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{ips: make(map[string]*ipLimiter)}
}

// limiterFor returns the rate.Limiter for the given IP, creating one with the
// default rate and burst if it does not yet exist.  Callers must hold rl.mu.
func (rl *rateLimiter) limiterFor(ip string) *rate.Limiter {
	entry, ok := rl.ips[ip]
	if !ok {
		entry = &ipLimiter{
			limiter: rate.NewLimiter(defaultRate, defaultBurst),
		}
		rl.ips[ip] = entry
	}
	return entry.limiter
}

// allow reports whether the IP is allowed to make a request right now.
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.limiterFor(ip).Allow()
}

// isTrustedProxy reports whether the given host is a trusted reverse proxy.
// Currently only localhost (127.0.0.1 and ::1) is trusted.
func isTrustedProxy(host string) bool {
	return host == "127.0.0.1" || host == "::1"
}

// validIP reports whether the given string is a valid IP address.
func validIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// middleware wraps an http.Handler with per-IP rate limiting.  Clients that
// exceed their allowance receive 429 Too Many Requests.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.allow(ip) {
			// Deliberately do not log the client IP or request path —
			// doing so would create an (IP, alias) access log, defeating
			// the privacy model that never persists the alias.
			log.Printf("WARN: rate limit exceeded")
			writeJSON(w, http.StatusTooManyRequests, errorResponse{
				Detail: "Rate limit exceeded. Please slow down.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the caller's IP from the request.
//
// When the server is directly exposed to the internet (no reverse proxy),
// r.RemoteAddr is the kernel-supplied TCP source IP and is used directly.
//
// When running behind a trusted reverse proxy (e.g., Caddy on localhost),
// r.RemoteAddr will be the proxy's IP. Proxies append the address of their
// immediate peer to the X-Forwarded-For chain, so with a single trusted hop
// the rightmost entry is the one the trusted proxy appended and everything to
// its left may be client-supplied and spoofable. We therefore use the rightmost
// entry; taking the leftmost would let a client rotate fake IPs and get a
// fresh rate-limit bucket per request. X-Real-IP is never trusted because
// proxies do not strip a client-supplied X-Real-IP, so honoring it would
// reintroduce the same spoof. Only localhost (127.0.0.1 and ::1) is considered
// a trusted proxy.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr has no port; fall back to the raw value.
		host = r.RemoteAddr
	}

	// If the direct connection is from a trusted proxy, try forwarded headers.
	if isTrustedProxy(host) {
		// Use the rightmost X-Forwarded-For entry: the trusted proxy
		// appends the real client IP at the end of the chain, so anything
		// to its left is attacker-controlled. If the rightmost entry is
		// not a valid IP, the proxy did not append one and the whole
		// chain is untrustworthy — fall back to the proxy's own IP.
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		ip := strings.TrimSpace(parts[len(parts)-1])
		if validIP(ip) {
			return ip
		}
		return host
	}

	// Direct connection or untrusted proxy: use RemoteAddr's host.
	return host
}
