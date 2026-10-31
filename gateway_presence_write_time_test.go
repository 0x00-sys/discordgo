package discordgo

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type delayedPresenceWriteConn struct {
	net.Conn
	now  *atomic.Int64
	once sync.Once
}

func (c *delayedPresenceWriteConn) Write(p []byte) (int, error) {
	if len(p) > 0 && p[0] == 0x81 {
		// Model a half-second delay between admission and transmission using
		// the same clock as the limiter. The WebSocket frame itself is real.
		c.once.Do(func() { c.now.Add(int64(500 * time.Millisecond)) })
	}
	return c.Conn.Write(p)
}

func TestPresenceRateLimitStartsAtCompletedWrite(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(1, 0).UnixNano())
	received := make(chan time.Time, 6)
	peerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(peerDone)
		defer close(received)
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = peer.Close() }()
		for i := range 8 {
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Error(err)
				return
			}
			var frame gatewayWrite
			if err := peer.ReadJSON(&frame); err != nil {
				t.Error(err)
				return
			}
			want := 3
			switch i {
			case 5:
				want = 8
			case 6:
				want = 1
			}
			if frame.Op != want {
				t.Errorf("frame %d opcode = %d, want %d", i, frame.Op, want)
			}
			if want == 3 {
				received <- time.Unix(0, now.Load())
			}
		}
	}))
	defer server.Close()
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &delayedPresenceWriteConn{Conn: conn, now: &now}, nil
	}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); <-peerDone }()
	session := &Session{LogLevel: -1, wsConn: conn, sequence: new(int64)}
	session.gatewaySendRateLimiter.now = func() time.Time { return time.Unix(0, now.Load()) }
	session.gatewaySendRateLimiter.wait = func(delay time.Duration, _ <-chan struct{}) bool {
		// Waiting for a presence slot must leave both ordinary gateway
		// messages and the heartbeat path able to acquire the write lock.
		if err := session.RequestGuildMembers("guild", "", 1, "nonce", false); err != nil {
			t.Error(err)
			return false
		}
		if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":1,"d":null}`)); err != nil {
			t.Error(err)
			return false
		}
		now.Add(int64(delay))
		return true
	}
	var first time.Time
	for i := range 6 {
		if err := session.UpdateGameStatus(0, "game"); err != nil {
			t.Fatal(err)
		}
		// Join each peer read before the next call can advance the clock.
		at, ok := <-received
		if !ok {
			t.Fatal("peer stopped before receiving all presence updates")
		}
		if i == 0 {
			first = at
		}
		if i == 5 && at.Sub(first) < 20*time.Second {
			t.Errorf("peer received six presence updates in %v, want at least 20s", at.Sub(first))
		}
	}
}
