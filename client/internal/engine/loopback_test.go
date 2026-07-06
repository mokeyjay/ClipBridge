package engine

import (
	"fmt"
	"testing"
	"time"
)

// TestLoopbackGuard covers the one-shot suppression, expiry and bounded-growth
// behavior of the loop-back guard.
func TestLoopbackGuard(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newLoopbackGuard(func() time.Time { return now })

	// One-shot: a remembered fingerprint suppresses exactly one event.
	g.remember("fp1")
	if !g.suppress("fp1") {
		t.Error("first suppress should hit")
	}
	if g.suppress("fp1") {
		t.Error("second suppress should miss (one-shot)")
	}

	// An unknown fingerprint is never suppressed.
	if g.suppress("unknown") {
		t.Error("unknown fingerprint should not be suppressed")
	}

	// Expiry: an entry older than loopbackTTL is not honored.
	g.remember("stale")
	now = now.Add(loopbackTTL + time.Second)
	if g.suppress("stale") {
		t.Error("stale entry should have expired")
	}
}

// TestLoopbackGuardBounded verifies that never-echoed fingerprints don't pile up:
// a later remember sweeps out entries past the TTL.
func TestLoopbackGuardBounded(t *testing.T) {
	now := time.Unix(0, 0)
	g := newLoopbackGuard(func() time.Time { return now })

	for i := 0; i < 100; i++ {
		g.remember(fmt.Sprintf("ephemeral-%d", i))
	}
	now = now.Add(loopbackTTL + time.Second)
	g.remember("trigger-sweep") // remember evicts the now-stale entries first

	g.mu.Lock()
	n := len(g.seen)
	g.mu.Unlock()
	if n != 1 {
		t.Errorf("guard not bounded: %d entries remain, want 1", n)
	}
}
