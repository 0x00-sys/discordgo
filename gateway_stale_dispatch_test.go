package discordgo

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestGatewayDispatchDoesNotChangeReplacement(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		startup       bool
		decoded       bool
	}{
		{"ready", `{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"old-session","resume_gateway_url":"wss://old.invalid","user":{"id":"old-user"},"guilds":[]}}`, true, false},
		{"guild delete", `{"op":0,"s":1,"t":"GUILD_DELETE","d":{"id":"replacement-guild","unavailable":false}}`, false, false},
		{"ready after payload decode", `{"op":0,"s":1,"t":"READY","d":{"v":"invalid","session_id":"old-session","resume_gateway_url":"wss://old.invalid","user":{"id":"old-user"},"guilds":[]}}`, true, true},
		{"guild delete after payload decode", `{"op":0,"s":1,"t":"GUILD_DELETE","d":{"id":"replacement-guild","unavailable":"invalid"}}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldConn, oldPeer := newGatewayTestConnection(t)
			newConn, _ := newGatewayTestConnection(t)
			oldListening := make(chan interface{})
			session := &Session{wsConn: oldConn, listening: oldListening, sequence: new(int64), LogLevel: LogDebug, State: NewState(), StateEnabled: true, SyncEvents: true}
			defer func() { _ = session.Close() }()
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			oldLogger := Logger
			Logger = func(_, _ int, format string, _ ...interface{}) {
				if (!test.decoded && strings.HasPrefix(format, "Op: %d, Seq:")) || (test.decoded && format == "error unmarshalling %s event, %s") {
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
				if test.startup {
					_, _ = session.waitForGatewayReady(oldConn, false, 0)
				} else {
					session.listen(oldConn, oldListening)
				}
				close(done)
			}()
			if err := oldPeer.WriteMessage(websocket.TextMessage, []byte(test.payload)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-paused:
			case <-time.After(time.Second):
				t.Fatal("old event did not reach dispatch barrier")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			session.Lock()
			session.wsConn, session.listening = newConn, make(chan interface{})
			session.sessionID, session.resumeGatewayURL = "replacement-session", "wss://replacement.invalid"
			atomic.StoreInt64(session.sequence, 100)
			session.Unlock()
			ready := &Ready{SessionID: "replacement-session", User: &User{ID: "replacement-user"}, Guilds: []*Guild{{ID: "replacement-guild"}}}
			if err := session.State.OnInterface(session, ready); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old reader did not finish")
			}
			if got := atomic.LoadInt64(session.sequence); got != 100 {
				t.Errorf("old dispatch changed replacement sequence to %d", got)
			}
			session.RLock()
			preserved := session.sessionID == "replacement-session" && session.resumeGatewayURL == "wss://replacement.invalid"
			session.RUnlock()
			if !preserved {
				t.Error("old READY changed replacement resume data")
			}
			session.State.RLock()
			statePreserved := session.State.SessionID == "replacement-session" && session.State.User != nil && session.State.User.ID == "replacement-user"
			session.State.RUnlock()
			if !statePreserved {
				t.Error("old READY changed replacement cache")
			}
			if _, err := session.State.Guild("replacement-guild"); err != nil {
				t.Errorf("old dispatch removed replacement guild: %v", err)
			}
		})
	}
}
