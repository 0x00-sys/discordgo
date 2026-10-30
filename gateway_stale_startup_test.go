package discordgo

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Pause after the transport has read a response, before the WebSocket reader
// can act on it. Closing the transport cannot retract bytes already read.
type gatewayReadResultBarrier struct {
	net.Conn
	active, entered, release chan struct{}
	once                     sync.Once
}

func (c *gatewayReadResultBarrier) Read(p []byte) (n int, err error) {
	n, err = c.Conn.Read(p)
	select {
	case <-c.active:
		c.once.Do(func() { close(c.entered) })
		<-c.release
	default:
	}
	return
}

func TestOpenStaleFailureDoesNotResetReplacement(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    int
		payload []byte
	}{
		{"invalid session", websocket.TextMessage, []byte(`{"op":9,"d":false}`)},
		{"malformed session", websocket.TextMessage, []byte(`{"op":9,"d":{}}`)},
		{"invalid sequence close", websocket.CloseMessage, websocket.FormatCloseMessage(4007, "invalid sequence")},
	} {
		t.Run(test.name, func(t *testing.T) {
			barrier := &gatewayReadResultBarrier{active: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
			serverDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
					return
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				close(barrier.active)
				if err := conn.WriteMessage(test.kind, test.payload); err != nil {
					return
				}
				<-serverDone
			}))
			defer server.Close()
			defer close(serverDone)
			session, err := newGatewayOpenTestSession(server, "Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.ShouldReconnectOnError = false
			session.Dialer = &websocket.Dialer{HandshakeTimeout: time.Second, NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				barrier.Conn = conn
				return barrier, err
			}}
			newConn, _ := newGatewayTestConnection(t)
			done := make(chan error, 1)
			go func() { done <- session.Open() }()
			defer func() {
				closeChannel(barrier.release)
				_ = session.Close()
			}()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				t.Fatal("Open did not read the startup failure")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			// Install the state a newer Open owns while the old reader is paused.
			session.Lock()
			session.wsConn = newConn
			session.sessionID = "replacement"
			session.resumeGatewayURL = "wss://replacement.gateway"
			atomic.StoreInt64(session.sequence, 42)
			session.Unlock()
			close(barrier.release)
			select {
			case err := <-done:
				if err == nil {
					t.Error("replaced Open reported success")
				}
			case <-time.After(time.Second):
				t.Fatal("old Open did not return")
			}
			session.RLock()
			preserved := session.wsConn == newConn && session.sessionID == "replacement" && session.resumeGatewayURL == "wss://replacement.gateway" && atomic.LoadInt64(session.sequence) == 42
			session.RUnlock()
			if !preserved {
				t.Error("old startup failure erased the replacement resume state")
			}
		})
	}
}
