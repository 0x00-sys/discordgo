package discordgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceWsListenManualCloseDoesNotReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	vc := &VoiceConnection{
		close:   make(chan struct{}),
		session: &Session{},
		wsConn:  conn,
	}

	closeChan := vc.close
	done := make(chan struct{})
	go func() {
		defer close(done)
		vc.wsListen(conn, closeChan)
	}()

	vc.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wsListen did not exit after Close")
	}

	vc.RLock()
	reconnecting := vc.reconnecting
	vc.RUnlock()
	if reconnecting {
		t.Fatal("wsListen started reconnect after manual Close")
	}
}
