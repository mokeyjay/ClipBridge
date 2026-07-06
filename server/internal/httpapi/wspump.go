package httpapi

import (
	"time"

	"github.com/gorilla/websocket"
	"github.com/mokeyjay/clipbridge/server/internal/wshub"
)

// WSS pump timing. pongWait must exceed the client's heartbeat interval; the
// server pings at pingPeriod and expects a pong (or any frame) within pongWait.
const (
	writeWait  = 10 * time.Second
	pongWait   = wshub.OfflineTimeout
	pingPeriod = wshub.HeartbeatInterval
)

// servePumps bridges a websocket connection to its hub registration: a write
// pump drains hub events and sends server pings, while the read loop treats any
// inbound frame (message or pong) as a heartbeat. It blocks until the connection
// ends, then unregisters. heartbeat may be nil for Web connections.
func (s *Server) servePumps(ws *websocket.Conn, c *wshub.Conn, heartbeat func()) {
	defer func() {
		s.hub.Unregister(c)
		_ = ws.Close()
	}()

	// Writer goroutine: hub events + periodic pings + hub-initiated close.
	writerDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		defer close(writerDone)
		for {
			select {
			case frame, ok := <-c.Out:
				if !ok {
					return
				}
				_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.TextMessage, frame); err != nil {
					return
				}
			case <-ticker.C:
				_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-c.Done:
				// Hub asked us to close (revoke/disable/replaced/reaped).
				_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
		}
	}()

	// Reader loop drives heartbeats and detects disconnect.
	_ = ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(pongWait))
		if heartbeat != nil {
			heartbeat()
		}
		return nil
	})
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
		_ = ws.SetReadDeadline(time.Now().Add(pongWait))
		if heartbeat != nil {
			heartbeat()
		}
	}

	c.SignalClose()
	<-writerDone
}
