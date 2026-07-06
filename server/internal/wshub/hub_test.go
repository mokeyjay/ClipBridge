package wshub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// TestUpgradeAndEcho validates the gorilla/websocket dependency: a real HTTP
// server upgrades the connection and a client completes the handshake and a
// message round-trip. This is the M0 "WebSocket builds and handshakes" check.
func TestUpgradeAndEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		defer conn.Close()
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		_ = conn.WriteMessage(mt, msg) // echo
	}))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer ws.Close()

	const want = "ping"
	if err := ws.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
		t.Fatalf("client write: %v", err)
	}
	_, got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(got) != want {
		t.Errorf("echo = %q, want %q", got, want)
	}
}
