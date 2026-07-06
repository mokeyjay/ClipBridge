package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// TestRateLimiterAllow covers the fixed-window count and the window reset.
func TestRateLimiterAllow(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(3, time.Minute)
	rl.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !rl.Allow("ip") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if rl.Allow("ip") {
		t.Error("4th attempt within the window should be blocked")
	}
	now = now.Add(time.Minute) // next window
	if !rl.Allow("ip") {
		t.Error("attempt in a new window should be allowed")
	}
}

// TestRateLimiterPrunesStaleKeys verifies the limiter doesn't accumulate a window
// per unique IP forever: once a window elapses, the next Allow sweeps stale keys.
func TestRateLimiterPrunesStaleKeys(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(3, time.Minute)
	rl.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		rl.Allow(fmt.Sprintf("ip-%d", i))
	}
	now = now.Add(2 * time.Minute) // all 50 windows are now stale
	rl.Allow("fresh")              // triggers a prune, then records "fresh"

	rl.mu.Lock()
	n := len(rl.windows)
	rl.mu.Unlock()
	if n != 1 {
		t.Errorf("stale keys not pruned: %d windows remain, want 1", n)
	}
}
