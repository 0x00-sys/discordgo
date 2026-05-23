package discordgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestHeartbeatDoesNotReconnectAfterListeningClosed(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	_ = conn.Close()

	listening := make(chan interface{})
	close(listening)

	session := &Session{
		ShouldReconnectOnError: false,
		SyncEvents:             true,
		sequence:               new(int64),
	}

	disconnected := false
	session.AddHandler(func(*Session, *Disconnect) {
		disconnected = true
	})

	session.heartbeat(conn, listening, 1)

	if disconnected {
		t.Fatal("heartbeat emitted disconnect after listening channel was closed")
	}
}
