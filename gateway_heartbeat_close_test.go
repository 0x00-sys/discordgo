package discordgo

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Hold a successful heartbeat write immediately before it returns to the
// caller, so Close can acquire the session lock in that window.
type pausedHeartbeatConn struct {
	net.Conn
	written chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *pausedHeartbeatConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && len(p) > 0 && p[0] == 0x81 {
		c.once.Do(func() {
			close(c.written)
			<-c.release
		})
	}
	return n, err
}

func TestHeartbeatDoesNotMarkClosedSessionReady(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		peer, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		peers <- peer
	}))
	defer server.Close()
	transport := &pausedHeartbeatConn{written: make(chan struct{}), release: make(chan struct{})}
	defer closeChannel(transport.release)
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		transport.Conn = conn
		return transport, err
	}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	peer := <-peers
	defer func() { _ = peer.Close() }()
	listening := make(chan interface{})
	session := &Session{
		wsConn: conn, listening: listening, sequence: new(int64),
		lastHeartbeatAck: time.Now().Add(time.Hour), LogLevel: -1,
	}
	heartbeatDone := make(chan struct{})
	go func() {
		session.heartbeat(conn, listening, 60000)
		close(heartbeatDone)
	}()
	select {
	case <-transport.written:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not finish its network write")
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case <-listening:
	case <-time.After(time.Second):
		t.Fatal("Close did not acquire the session lock")
	}
	close(transport.release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	select {
	case <-heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after Close")
	}
	session.RLock()
	ready, currentConn := session.DataReady, session.wsConn
	session.RUnlock()
	if currentConn != nil {
		t.Fatal("Close left the gateway connection installed")
	}
	if ready {
		t.Fatal("successful stale heartbeat marked a closed session ready")
	}
}
