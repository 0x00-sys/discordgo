package discordgo

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestGatewayReconnectEventDoesNotCloseReplacement(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
	}{
		{"reconnect", `{"op":7,"d":null}`},
		{"resumable", `{"op":9,"d":true}`},
		{"invalid", `{"op":9,"d":false}`},
		{"malformed", `{"op":9,"d":{"unexpected":true}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldConn, oldPeer := newGatewayTestConnection(t)
			newConn, _ := newGatewayTestConnection(t)
			oldListening := make(chan interface{})
			newListening := make(chan interface{})
			session := &Session{
				wsConn: oldConn, listening: oldListening, sequence: new(int64),
				LogLevel: LogInformational, SyncEvents: true,
			}
			defer func() { _ = session.Close() }()
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			oldLogger := Logger
			Logger = func(_, _ int, format string, _ ...interface{}) {
				if strings.HasPrefix(format, "Closing and reconnecting in response to Op") {
					close(paused)
					<-release
				}
			}
			defer func() {
				closeChannel(release)
				_ = oldConn.Close()
				<-done
				Logger = oldLogger
			}()
			go func() {
				session.listen(oldConn, oldListening)
				close(done)
			}()
			if err := oldPeer.WriteMessage(websocket.TextMessage, []byte(test.message)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-paused:
			case <-time.After(time.Second):
				t.Fatal("listener did not reach the reconnect event")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			session.Lock()
			session.wsConn = newConn
			session.listening = newListening
			session.DataReady = true
			session.sessionID = "replacement-session"
			session.resumeGatewayURL = "wss://replacement.gateway"
			atomic.StoreInt64(session.sequence, 42)
			session.Unlock()
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old listener did not exit")
			}
			session.RLock()
			preserved := session.wsConn == newConn && session.listening == newListening && session.DataReady
			resumePreserved := session.sessionID == "replacement-session" && session.resumeGatewayURL == "wss://replacement.gateway" && atomic.LoadInt64(session.sequence) == 42
			session.RUnlock()
			if !preserved || !resumePreserved {
				t.Errorf("stale event changed replacement: connection preserved=%t, resume state preserved=%t", preserved, resumePreserved)
			}
			select {
			case <-newListening:
				t.Error("stale event closed the replacement listening channel")
			default:
			}
			if err := newConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				t.Errorf("replacement socket is unusable: %v", err)
			}
		})
	}
}
