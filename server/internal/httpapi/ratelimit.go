package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Pairing submission rate limit: at most 3 attempts per client IP per minute.
const (
	pairingRateMaxAttempts = 3
	pairingRateWindow      = time.Minute
)

// rateLimiter is a small fixed-window per-key limiter. It is concurrency-safe and
// bounds memory by sweeping fully-elapsed windows (at most once per window, so
// the cost is amortized) — otherwise a key per unique client IP would accumulate
// forever.
type rateLimiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	windows   map[string]*window
	lastPrune time.Time
	now       func() time.Time // injectable clock for tests
}

// window tracks the count and start for one key's current fixed window.
type window struct {
	count int
	start time.Time
}

// newRateLimiter builds a limiter allowing max events per window per key.
func newRateLimiter(max int, w time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: w, windows: make(map[string]*window), now: time.Now}
}

// Allow reports whether an event for key is permitted now, recording it if so.
func (rl *rateLimiter) Allow(key string) bool {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.pruneLocked(now)
	w := rl.windows[key]
	if w == nil || now.Sub(w.start) >= rl.window {
		rl.windows[key] = &window{count: 1, start: now}
		return true
	}
	if w.count >= rl.max {
		return false
	}
	w.count++
	return true
}

// pruneLocked deletes windows whose period has fully elapsed. It runs at most
// once per window so the O(n) sweep is amortized across calls. Caller holds mu.
func (rl *rateLimiter) pruneLocked(now time.Time) {
	if now.Sub(rl.lastPrune) < rl.window {
		return
	}
	rl.lastPrune = now
	for k, w := range rl.windows {
		if now.Sub(w.start) >= rl.window {
			delete(rl.windows, k)
		}
	}
}

// clientIP extracts the client IP for rate limiting. The device port is reached
// directly, so RemoteAddr is authoritative there. The Web port may sit behind a
// reverse proxy, but we deliberately use RemoteAddr rather than the spoofable
// X-Forwarded-For; a deployment that needs proxy-aware limiting should add it
// behind an explicit trusted-proxy allowlist.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
