package discordgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOpenReturnsOnInvalidSessionDuringOpen(t *testing.T) {
	server := newGatewayOpenTestServer(t, []byte(`{"op":9,"d":false}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.sessionID = "old-session"
	session.resumeGatewayURL = session.gateway
	atomic.StoreInt64(session.sequence, 42)

	err = openWithTimeout(t, session)
	if err == nil {
		t.Fatal("expected Open to return an invalid session error")
	}

	if session.wsConn != nil {
		t.Fatal("Open returned an error without clearing the websocket")
	}
	if session.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", session.sessionID)
	}
	if session.resumeGatewayURL != "" {
		t.Fatalf("resumeGatewayURL = %q, want empty", session.resumeGatewayURL)
	}
	if sequence := atomic.LoadInt64(session.sequence); sequence != 0 {
		t.Fatalf("sequence = %d, want 0", sequence)
	}
}

func TestOpenReturnsOnHeartbeatAckDuringOpen(t *testing.T) {
	server := newGatewayOpenTestServer(t, []byte(`{"op":11,"d":null}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	err = openWithTimeout(t, session)
	if err == nil {
		t.Fatal("expected Open to return an unexpected operation error")
	}
	if session.wsConn != nil {
		t.Fatal("Open returned an error without clearing the websocket")
	}
}

func TestOpenReturnsOnUnexpectedDispatchDuringOpen(t *testing.T) {
	server := newGatewayOpenTestServer(t, []byte(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"guild","user":{"id":"user"},"status":"online","activities":[],"client_status":{}}}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.SyncEvents = true

	called := false
	session.AddHandler(func(s *Session, p *PresenceUpdate) {
		called = true
		_ = s.Close()
	})

	err = openWithTimeout(t, session)
	if err == nil {
		t.Fatal("expected Open to return an unexpected dispatch error")
	}
	if called {
		t.Fatal("unexpected dispatch handler was called during Open")
	}
}

func newGatewayOpenTestServer(t *testing.T, startupPacket []byte) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":1000}}`)); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, startupPacket); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}))

	t.Cleanup(server.Close)
	return server
}

func newGatewayOpenTestSession(server *httptest.Server, token string) (*Session, error) {
	session, err := New(token)
	if err != nil {
		return nil, err
	}
	session.gateway = "ws" + strings.TrimPrefix(server.URL, "http")
	return session, nil
}

func openWithTimeout(t *testing.T, session *Session) error {
	t.Helper()

	var err error
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Open()
	}()

	select {
	case err = <-errCh:
		return err
	case <-time.After(time.Second):
		t.Fatal("Open did not return")
	}

	return err
}
