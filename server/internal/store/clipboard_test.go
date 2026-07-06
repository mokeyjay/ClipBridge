package store

import (
	"testing"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// seedItemWithTwoDeliveries creates a user, three devices, and an item delivered
// to two of them. Returns the item and the two delivery target device ids.
func seedItem(t *testing.T, st *Store, ttlSeconds int64) (*ClipboardItem, []Delivery) {
	t.Helper()
	u, _ := st.CreateUser("alice", "h")
	mk := func(name string) *Device {
		code, _ := st.CreatePairingCode(u.ID, "c-"+name, 300)
		req, _ := st.CreatePairingRequest(code, name, protocol.PlatformDarwin, "0.1.0", "pk-"+name, "ph-"+name)
		d, err := st.ConfirmPairingRequest(u.ID, req.ID)
		if err != nil {
			t.Fatalf("device %s: %v", name, err)
		}
		return d
	}
	src, a, b := mk("src"), mk("a"), mk("b")
	now := st.nowUnix()
	item := &ClipboardItem{
		ID: newID(), UserID: u.ID, SourceDeviceID: src.ID, ContentType: protocol.ContentText,
		CiphertextSizeBytes: 10, CiphertextPath: "x.bin", CiphertextSHA256: "abc",
		ChunkSizeBytes: 65536, TotalChunks: 1, ExpiresAt: now + ttlSeconds, CreatedAt: now,
	}
	deliveries, err := st.CreateItemWithDeliveries(item, []NewDeliveryTarget{
		{TargetDeviceID: a.ID, WrappedDEK: "wa"}, {TargetDeviceID: b.ID, WrappedDEK: "wb"},
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	return item, deliveries
}

// TestResolveCompletesOnLastDelivery verifies the item completes only after the
// last pending delivery resolves, and the path is returned for deletion.
func TestResolveCompletesOnLastDelivery(t *testing.T) {
	st, _ := newTestStore(t)
	item, deliveries := seedItem(t, st, 300)

	// First ack: item not yet complete.
	res, err := st.ResolveDelivery(deliveries[0].ID, deliveries[0].TargetDeviceID, protocol.DeliveryAcked, nil)
	if err != nil {
		t.Fatalf("resolve 1: %v", err)
	}
	if res.ItemCompleted {
		t.Error("item completed after only one of two deliveries")
	}

	// Re-resolving the same delivery is rejected (no longer pending).
	if _, err := st.ResolveDelivery(deliveries[0].ID, deliveries[0].TargetDeviceID, protocol.DeliveryAcked, nil); err != ErrNotFound {
		t.Errorf("double-resolve err = %v, want ErrNotFound", err)
	}

	// Second reject: now complete, path + source returned.
	reason := string(protocol.RejectUserDeclined)
	res, err = st.ResolveDelivery(deliveries[1].ID, deliveries[1].TargetDeviceID, protocol.DeliveryRejected, &reason)
	if err != nil {
		t.Fatalf("resolve 2: %v", err)
	}
	if !res.ItemCompleted || res.CiphertextPath != item.CiphertextPath || res.SourceDeviceID != item.SourceDeviceID {
		t.Errorf("completion result = %+v", res)
	}
}

// TestExpireDueItems verifies TTL expiry returns due items and flips statuses.
func TestExpireDueItems(t *testing.T) {
	st, clock := newTestStore(t)
	item, _ := seedItem(t, st, 300)

	// Before TTL: nothing due.
	if due, _ := st.ExpireDueItems(); len(due) != 0 {
		t.Errorf("due before TTL = %v, want none", due)
	}

	*clock += 301
	due, err := st.ExpireDueItems()
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if len(due) != 1 || due[0].ID != item.ID || due[0].CiphertextPath != item.CiphertextPath {
		t.Fatalf("due = %+v", due)
	}

	// Idempotent: a second pass finds nothing (status now expired).
	if due, _ := st.ExpireDueItems(); len(due) != 0 {
		t.Errorf("second expire = %v, want none", due)
	}

	// Pending deliveries were expired too.
	active, _ := st.ActiveCiphertextPaths()
	if active[item.CiphertextPath] {
		t.Error("expired item still listed as active")
	}
}

// TestActiveCiphertextPaths verifies only active items are reported (for orphan
// sweeping).
func TestActiveCiphertextPaths(t *testing.T) {
	st, _ := newTestStore(t)
	item, deliveries := seedItem(t, st, 300)

	active, _ := st.ActiveCiphertextPaths()
	if !active[item.CiphertextPath] {
		t.Error("active item not listed")
	}

	// Complete the item; it should drop from the active set.
	for _, d := range deliveries {
		_, _ = st.ResolveDelivery(d.ID, d.TargetDeviceID, protocol.DeliveryAcked, nil)
	}
	active, _ = st.ActiveCiphertextPaths()
	if active[item.CiphertextPath] {
		t.Error("completed item still listed as active")
	}
}
