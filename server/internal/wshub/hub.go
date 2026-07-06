// Package wshub owns the in-memory WebSocket registry and event fan-out. It maps
// users to their online device connections, each device to its single active
// connection, and users to their Web console connections. It also tracks
// heartbeats, evicts connections idle past the offline timeout, and persists
// last_seen with write throttling. The hub never carries clipboard bodies; it
// only delivers lightweight notification events (see prd/05-api-and-events.md).
package wshub

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mokeyjay/clipbridge/server/internal/store"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// Upgrader is the shared WSS upgrader. Origin/identity is enforced at the auth
// layer (device token / session cookie) before Upgrade is called, so the hub
// itself performs no cross-origin checks.
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Timing constants from prd/12-decisions-and-risks.md §2.
const (
	// HeartbeatInterval is how often clients are expected to ping.
	HeartbeatInterval = 30 * time.Second
	// OfflineTimeout is the idle window after which a device is considered offline.
	OfflineTimeout = 90 * time.Second
	// lastSeenThrottle bounds how often a single device's last_seen is written.
	lastSeenThrottle = 30 * time.Second
	// sendBuffer is the per-connection outbound queue depth.
	sendBuffer = 16
)

// Kind distinguishes device connections from Web console connections.
type Kind int

const (
	// KindDevice is a paired-device WSS connection.
	KindDevice Kind = iota
	// KindWeb is a Web console (admin/user) WSS connection.
	KindWeb
)

// Conn is one registered connection. The transport (websocket write pump) reads
// from Out; the hub closes Done to signal the pump to tear the socket down.
type Conn struct {
	Kind     Kind
	UserID   string
	DeviceID string // set only for KindDevice

	Out  chan []byte   // outbound event frames (JSON)
	Done chan struct{} // closed exactly once when the hub wants this conn gone

	closeOnce   sync.Once
	lastSeen    int64 // unix seconds, guarded by hub.mu
	lastPersist int64 // unix seconds of last last_seen write, guarded by hub.mu
}

// signalClose closes Done at most once.
func (c *Conn) signalClose() { c.closeOnce.Do(func() { close(c.Done) }) }

// SignalClose lets the transport (write/read pump) trigger teardown of this
// connection, e.g. when the read loop detects the socket has closed.
func (c *Conn) SignalClose() { c.signalClose() }

// Hub is the concurrency-safe connection registry and event dispatcher.
type Hub struct {
	mu          sync.Mutex
	devices     map[string]*Conn            // deviceID -> active conn
	userDevices map[string]map[string]*Conn // userID -> deviceID -> conn
	userWeb     map[string]map[*Conn]bool   // userID -> set of web conns

	store *store.Store
	now   func() time.Time // injectable clock for tests
}

// New creates an empty hub bound to a store for last_seen persistence.
func New(st *store.Store) *Hub {
	return &Hub{
		devices:     make(map[string]*Conn),
		userDevices: make(map[string]map[string]*Conn),
		userWeb:     make(map[string]map[*Conn]bool),
		store:       st,
		now:         time.Now,
	}
}

// RegisterDevice registers a device connection, evicting any existing connection
// for the same device (a device has at most one active connection). It seeds the
// heartbeat clock and persists an initial last_seen.
func (h *Hub) RegisterDevice(userID, deviceID string) *Conn {
	c := &Conn{Kind: KindDevice, UserID: userID, DeviceID: deviceID, Out: make(chan []byte, sendBuffer), Done: make(chan struct{})}
	now := h.now().Unix()
	c.lastSeen = now
	c.lastPersist = now

	h.mu.Lock()
	if old := h.devices[deviceID]; old != nil {
		old.signalClose()
		h.removeDeviceLocked(old)
	}
	h.devices[deviceID] = c
	if h.userDevices[userID] == nil {
		h.userDevices[userID] = make(map[string]*Conn)
	}
	h.userDevices[userID][deviceID] = c
	h.mu.Unlock()

	if h.store != nil {
		_ = h.store.UpdateDeviceLastSeen(deviceID, now)
	}
	return c
}

// RegisterWeb registers a Web console connection for a user (admin sessions also
// key by their admin id namespace via userID).
func (h *Hub) RegisterWeb(userID string) *Conn {
	c := &Conn{Kind: KindWeb, UserID: userID, Out: make(chan []byte, sendBuffer), Done: make(chan struct{})}
	h.mu.Lock()
	if h.userWeb[userID] == nil {
		h.userWeb[userID] = make(map[*Conn]bool)
	}
	h.userWeb[userID][c] = true
	h.mu.Unlock()
	return c
}

// Unregister removes a connection from the registry. Safe to call more than once
// and regardless of who initiated the close.
func (h *Hub) Unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch c.Kind {
	case KindDevice:
		// Only remove if this exact conn is still the registered one (a newer
		// connection may have already replaced it).
		if cur := h.devices[c.DeviceID]; cur == c {
			h.removeDeviceLocked(c)
		}
	case KindWeb:
		if set := h.userWeb[c.UserID]; set != nil {
			delete(set, c)
			if len(set) == 0 {
				delete(h.userWeb, c.UserID)
			}
		}
	}
}

// removeDeviceLocked unlinks a device conn from both maps. Caller holds h.mu.
func (h *Hub) removeDeviceLocked(c *Conn) {
	delete(h.devices, c.DeviceID)
	if set := h.userDevices[c.UserID]; set != nil {
		delete(set, c.DeviceID)
		if len(set) == 0 {
			delete(h.userDevices, c.UserID)
		}
	}
}

// Heartbeat records activity for a device connection, persisting last_seen at
// most once per throttle window.
func (h *Hub) Heartbeat(c *Conn) {
	if c.Kind != KindDevice {
		return
	}
	now := h.now().Unix()
	h.mu.Lock()
	c.lastSeen = now
	persist := now-c.lastPersist >= int64(lastSeenThrottle/time.Second)
	if persist {
		c.lastPersist = now
	}
	h.mu.Unlock()
	if persist && h.store != nil {
		_ = h.store.UpdateDeviceLastSeen(c.DeviceID, now)
	}
}

// IsDeviceOnline reports whether a device currently has an active connection.
func (h *Hub) IsDeviceOnline(deviceID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.devices[deviceID] != nil
}

// OnlineDeviceCount returns the total number of devices currently connected
// across all users (for the admin overview).
func (h *Hub) OnlineDeviceCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.devices)
}

// OnlineDeviceIDs returns the user's currently online device IDs (excludes none;
// callers filter out the source device and disabled/revoked devices).
func (h *Hub) OnlineDeviceIDs(userID string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.userDevices[userID]
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}

// CloseDevice forcibly disconnects a device (used on disable/revoke). The conn is
// removed from the registry and its pump signalled to close the socket.
func (h *Hub) CloseDevice(deviceID string) {
	h.mu.Lock()
	c := h.devices[deviceID]
	if c != nil {
		h.removeDeviceLocked(c)
	}
	h.mu.Unlock()
	if c != nil {
		c.signalClose()
	}
}

// CloseUser forcibly disconnects all of a user's device and Web connections (used
// when a user is disabled).
func (h *Hub) CloseUser(userID string) {
	h.mu.Lock()
	var doomed []*Conn
	for _, c := range h.userDevices[userID] {
		doomed = append(doomed, c)
	}
	for c := range h.userWeb[userID] {
		doomed = append(doomed, c)
	}
	for _, c := range doomed {
		if c.Kind == KindDevice {
			h.removeDeviceLocked(c)
		} else {
			delete(h.userWeb[userID], c)
		}
	}
	delete(h.userWeb, userID)
	h.mu.Unlock()
	for _, c := range doomed {
		c.signalClose()
	}
}

// deliver enqueues a pre-encoded frame, dropping it if the conn's buffer is full
// (a slow consumer recovers full state on reconnect, so dropping is acceptable).
func deliver(c *Conn, frame []byte) {
	select {
	case c.Out <- frame:
	default:
	}
}

// NotifyDevice sends an event to a single device if it is online.
func (h *Hub) NotifyDevice(deviceID string, event protocol.Event) {
	frame, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	c := h.devices[deviceID]
	h.mu.Unlock()
	if c != nil {
		deliver(c, frame)
	}
}

// NotifyUserDevices sends an event to all of a user's online devices.
func (h *Hub) NotifyUserDevices(userID string, event protocol.Event) {
	frame, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.userDevices[userID]))
	for _, c := range h.userDevices[userID] {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		deliver(c, frame)
	}
}

// NotifyUserWeb sends an event to all of a user's Web console connections.
func (h *Hub) NotifyUserWeb(userID string, event protocol.Event) {
	frame, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.userWeb[userID]))
	for c := range h.userWeb[userID] {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		deliver(c, frame)
	}
}

// NotifyAllDevices sends an event to every online device across all users. Used
// for instance-wide changes such as the server max-sync-size ceiling.
func (h *Hub) NotifyAllDevices(event protocol.Event) {
	frame, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.devices))
	for _, c := range h.devices {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		deliver(c, frame)
	}
}

// Run starts the background janitor that evicts device connections idle beyond
// OfflineTimeout. It returns when ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.reapStale()
		}
	}
}

// reapStale closes device connections whose last heartbeat is older than the
// offline timeout.
func (h *Hub) reapStale() {
	cutoff := h.now().Unix() - int64(OfflineTimeout/time.Second)
	h.mu.Lock()
	var doomed []*Conn
	for _, c := range h.devices {
		if c.lastSeen < cutoff {
			doomed = append(doomed, c)
		}
	}
	for _, c := range doomed {
		h.removeDeviceLocked(c)
	}
	h.mu.Unlock()
	for _, c := range doomed {
		c.signalClose()
	}
}
