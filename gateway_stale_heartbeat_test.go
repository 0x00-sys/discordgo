package discordgo

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestGatewayHeartbeatEventsDoNotChangeReplacement(t *testing.T) {
	for _, test := range []struct {
		name, payload string
	}{
		{"request", `{"op":1,"d":null}`},
		{"acknowledgement", `{"op":11,"d":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldConn, oldPeer := newGatewayTestConnection(t)
			newConn, _ := newGatewayTestConnection(t)
			oldListening := make(chan interface{})
			session := &Session{wsConn: oldConn, listening: oldListening, sequence: new(int64), LogLevel: LogDebug}
			defer func() { _ = session.Close() }()
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			oldLogger := Logger
			Logger = func(_, _ int, format string, _ ...interface{}) {
				if strings.HasPrefix(format, "Op: %d, Seq:") {
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
			if err := oldPeer.WriteMessage(websocket.TextMessage, []byte(test.payload)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-paused:
			case <-time.After(time.Second):
				t.Fatal("old listener did not reach the heartbeat event")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			sentAt := time.Now().Add(-time.Minute)
			ackAt := sentAt.Add(-time.Second)
			session.Lock()
			session.wsConn = newConn
			session.listening = make(chan interface{})
			session.lastHeartbeatSent, session.LastHeartbeatSent = sentAt, sentAt.UTC()
			session.lastHeartbeatAck, session.LastHeartbeatAck = ackAt, ackAt.UTC()
			session.Unlock()
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old listener did not exit")
			}
			session.RLock()
			defer session.RUnlock()
			if session.lastHeartbeatSent != sentAt || session.LastHeartbeatSent != sentAt.UTC() {
				t.Error("old heartbeat request changed replacement send timestamps")
			}
			if session.lastHeartbeatAck != ackAt || session.LastHeartbeatAck != ackAt.UTC() {
				t.Error("old heartbeat acknowledgement changed replacement ACK timestamps")
			}
		})
	}
}
