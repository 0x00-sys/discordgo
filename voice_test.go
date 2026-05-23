package discordgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestVoiceClose4022DoesNotReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade returned error: %v", err)
			return
		}
		defer conn.Close()

		err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, ""))
		if err != nil {
			t.Errorf("WriteMessage returned error: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	s := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	v := &VoiceConnection{
		GuildID:  "guild",
		LogLevel: -1,
		close:    make(chan struct{}),
		session:  s,
		wsConn:   conn,
	}
	s.VoiceConnections[v.GuildID] = v

	v.wsListen(conn, v.close)

	if v.wsConn != nil {
		t.Fatal("wsConn is not nil")
	}
	if _, ok := s.VoiceConnections[v.GuildID]; ok {
		t.Fatal("voice connection was not removed")
	}
	if v.reconnecting {
		t.Fatal("voice connection started reconnecting")
	}
}
