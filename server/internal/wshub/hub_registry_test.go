package wshub

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// newHub builds a hub backed by a temp store with a controllable clock.
func newHub(t *testing.T) (*Hub, *store.Store, *int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := store.New(db)
	clock := time.Now().Unix()
	h := New(st)
	h.now = func() time.Time { return time.Unix(clock, 0) }
	return h, st, &clock
}

// seedDevice creates a real user+device so last_seen writes have a target.
func seedDevice(t *testing.T, st *store.Store) (userID, deviceID string) {
	t.Helper()
	u, err := st.CreateUser("alice", "h")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	code, _ := st.CreatePairingCode(u.ID, "ch", 300)
	req, _ := st.CreatePairingRequest(code, "Mac", protocol.PlatformDarwin, "0.1.0", "pk", "ph")
	d, err := st.ConfirmPairingRequest(u.ID, req.ID)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	return u.ID, d.ID
}

// TestRegisterAndOnline verifies registration drives online state.
func TestRegisterAndOnline(t *testing.T) {
	h, st, _ := newHub(t)
	userID, deviceID := seedDevice(t, st)

	if h.IsDeviceOnline(deviceID) {
		t.Fatal("device online before registering")
	}
	c := h.RegisterDevice(userID, deviceID)
	if !h.IsDeviceOnline(deviceID) {
		t.Error("device not online after registering")
	}
	ids := h.OnlineDeviceIDs(userID)
	if len(ids) != 1 || ids[0] != deviceID {
		t.Errorf("online ids = %v, want [%s]", ids, deviceID)
	}

	// last_seen must have been persisted on register.
	d, _ := st.GetDeviceByID(deviceID)
	if d.LastSeenAt == nil {
		t.Error("last_seen not persisted on register")
	}

	h.Unregister(c)
	if h.IsDeviceOnline(deviceID) {
		t.Error("device still online after unregister")
	}
}

// TestSecondConnectionKicksFirst verifies a device's new connection evicts the old.
func TestSecondConnectionKicksFirst(t *testing.T) {
	h, st, _ := newHub(t)
	userID, deviceID := seedDevice(t, st)

	first := h.RegisterDevice(userID, deviceID)
	second := h.RegisterDevice(userID, deviceID)

	select {
	case <-first.Done:
		// expected: old connection signalled to close
	default:
		t.Error("first connection was not closed by the second")
	}
	if !h.IsDeviceOnline(deviceID) {
		t.Error("device should remain online via the second connection")
	}
	// Unregistering the stale first conn must not remove the live second.
	h.Unregister(first)
	if !h.IsDeviceOnline(deviceID) {
		t.Error("unregistering the kicked conn wrongly removed the live one")
	}
	_ = second
}

// TestCloseDeviceAndUser verifies forced disconnects on revoke/disable.
func TestCloseDeviceAndUser(t *testing.T) {
	h, st, _ := newHub(t)
	userID, deviceID := seedDevice(t, st)

	dc := h.RegisterDevice(userID, deviceID)
	wc := h.RegisterWeb(userID)

	h.CloseDevice(deviceID)
	select {
	case <-dc.Done:
	default:
		t.Error("CloseDevice did not signal close")
	}
	if h.IsDeviceOnline(deviceID) {
		t.Error("device still online after CloseDevice")
	}

	// Web connection still open until CloseUser.
	dc2 := h.RegisterDevice(userID, deviceID)
	h.CloseUser(userID)
	select {
	case <-dc2.Done:
	default:
		t.Error("CloseUser did not close device conn")
	}
	select {
	case <-wc.Done:
	default:
		t.Error("CloseUser did not close web conn")
	}
}

// TestNotifyDelivers verifies events reach the right connection's Out channel.
func TestNotifyDelivers(t *testing.T) {
	h, st, _ := newHub(t)
	userID, deviceID := seedDevice(t, st)
	c := h.RegisterDevice(userID, deviceID)

	h.NotifyDevice(deviceID, protocol.Event{Event: protocol.EventDeliveryCreated, Data: protocol.DeliveryCreatedData{DeliveryID: "d1"}})
	select {
	case frame := <-c.Out:
		if len(frame) == 0 {
			t.Error("empty frame delivered")
		}
	default:
		t.Error("no event delivered to device")
	}

	wc := h.RegisterWeb(userID)
	h.NotifyUserWeb(userID, protocol.Event{Event: protocol.EventConfigChanged})
	select {
	case <-wc.Out:
	default:
		t.Error("no event delivered to web conn")
	}
}

// TestHeartbeatThrottlesPersist verifies last_seen writes are throttled but the
// in-memory online timestamp always advances.
func TestHeartbeatThrottlesPersist(t *testing.T) {
	h, st, clock := newHub(t)
	userID, deviceID := seedDevice(t, st)
	c := h.RegisterDevice(userID, deviceID)

	// Heartbeat 5s later: within throttle window, no new DB write expected.
	*clock += 5
	h.Heartbeat(c)
	d1, _ := st.GetDeviceByID(deviceID)

	// Heartbeat past the throttle window: DB write expected.
	*clock += int64(lastSeenThrottle/time.Second) + 1
	h.Heartbeat(c)
	d2, _ := st.GetDeviceByID(deviceID)

	if d2.LastSeenAt == nil || d1.LastSeenAt == nil {
		t.Fatal("missing last_seen")
	}
	if *d2.LastSeenAt <= *d1.LastSeenAt {
		t.Errorf("last_seen not advanced past throttle: %d -> %d", *d1.LastSeenAt, *d2.LastSeenAt)
	}
}

// TestReapStaleEvicts verifies the janitor drops connections idle past timeout.
func TestReapStaleEvicts(t *testing.T) {
	h, st, clock := newHub(t)
	userID, deviceID := seedDevice(t, st)
	c := h.RegisterDevice(userID, deviceID)

	*clock += int64(OfflineTimeout/time.Second) + 1
	h.reapStale()

	select {
	case <-c.Done:
	default:
		t.Error("stale connection was not closed")
	}
	if h.IsDeviceOnline(deviceID) {
		t.Error("stale device still online after reap")
	}
}
