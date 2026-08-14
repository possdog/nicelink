package main

import (
	"log"
	"net"
	"net/http"
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

// middleware wraps an http.Handler with per-IP rate limiting.  Clients that
// exceed their allowance receive 429 Too Many Requests.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.allow(ip) {
			log.Printf("WARN: rate limit exceeded for %s — %s %s", ip, r.Method, r.URL.Path)
			writeJSON(w, http.StatusTooManyRequests, errorResponse{
				Detail: "Rate limit exceeded. Please slow down.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the caller's IP from r.RemoteAddr.
//
// The server is designed to be exposed directly to the internet without a
// reverse proxy.  In that topology r.RemoteAddr is the kernel-supplied TCP
// source IP, which the client cannot spoof without breaking the TCP handshake.
//
// X-Forwarded-For and X-Real-IP headers are intentionally NOT trusted: because
// there is no proxy in front to overwrite them, any client could set these
// headers to arbitrary values and obtain a fresh rate-limit bucket on each
// request, making the limiter trivially bypassable.
//
// If reverse-proxy support is added in the future, re-introduce parsing of
// X-Forwarded-For / X-Real-IP here — but only after verifying that the proxy
// is configured to strip or overwrite client-supplied values, or by gating
// on a trusted-proxy IP list checked against r.RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
