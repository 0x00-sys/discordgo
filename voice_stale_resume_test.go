package discordgo

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type voiceResumeResultBarrier struct {
	net.Conn
	succeeded                bool
	active, entered, release chan struct{}
	once                     sync.Once
}

func (c *voiceResumeResultBarrier) pause() {
	select {
	case <-c.active:
		c.once.Do(func() { close(c.entered) })
		<-c.release
	default:
	}
}

func (c *voiceResumeResultBarrier) Read(p []byte) (n int, err error) {
	n, err = c.Conn.Read(p)
	if !c.succeeded {
		c.pause()
	}
	return
}

func (c *voiceResumeResultBarrier) SetReadDeadline(deadline time.Time) error {
	err := c.Conn.SetReadDeadline(deadline)
	if err == nil && c.succeeded && deadline.IsZero() {
		c.pause()
	}
	return err
}

func TestVoiceResumeResultDoesNotChangeReplacement(t *testing.T) {
	for _, outcome := range []string{"resumed", "failed", "opening"} {
		t.Run(outcome, func(t *testing.T) {
			barrier := &voiceResumeResultBarrier{succeeded: outcome == "resumed", active: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
			serverDone := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				var request voiceResumeOp
				if err := conn.ReadJSON(&request); err != nil {
					return
				}
				if request.Op != 7 {
					t.Errorf("received operation %d, want resume", request.Op)
					return
				}
				close(barrier.active)
				if barrier.succeeded {
					err = conn.WriteMessage(websocket.TextMessage, []byte(`{"op":9,"seq":7,"d":null}`))
				} else {
					err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4006, "invalid session"))
				}
				if err == nil {
					<-serverDone
				}
			}))
			defer server.Close()
			defer close(serverDone)
			tlsDialer := &tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig}
			session := &Session{VoiceConnections: make(map[string]*VoiceConnection), Dialer: &websocket.Dialer{
				HandshakeTimeout: time.Second,
				NetDialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					conn, err := tlsDialer.DialContext(ctx, network, address)
					barrier.Conn = conn
					return barrier, err
				},
			}}
			udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
			if err != nil {
				t.Fatal(err)
			}
			voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", sessionID: "session", token: "dummy-token",
				endpoint: strings.TrimPrefix(server.URL, "https://"), session: session,
				udpConn: udp, aead: aead, close: make(chan struct{}),
			}
			session.VoiceConnections[voice.GuildID] = voice
			newConn, _ := newGatewayTestConnection(t)
			done := make(chan struct{})
			oldClose := voice.close
			go func() {
				voice.reconnectWithResume(true, nil, oldClose)
				close(done)
			}()
			defer func() {
				closeChannel(barrier.release)
				session.Lock()
				delete(session.VoiceConnections, voice.GuildID)
				session.Unlock()
				voice.Close()
				<-done
			}()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				t.Fatal("resume did not reach the result barrier")
			}
			voice.Close()
			// Stop the test's fallback loop if the buggy worker closes the new
			// backend. This hook runs only after the destructive action occurs.
			backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
				session.Lock()
				delete(session.VoiceConnections, voice.GuildID)
				session.Unlock()
			}}
			installFakeDAVE(voice, backend, 1)
			newClose := make(chan struct{})
			var opening *voiceOpenAttempt
			voice.Lock()
			voice.wsConn, voice.close = newConn, newClose
			voice.Ready, voice.speaking = true, true
			voice.sequence, voice.sequenceSet = 42, true
			if outcome == "opening" {
				opening = &voiceOpenAttempt{conn: newConn, canceled: make(chan struct{}), cancel: func() {}}
				voice.opening = opening
				voice.wsConn, voice.close = nil, nil
				voice.Ready, voice.speaking = false, false
			}
			voice.Unlock()
			close(barrier.release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old reconnect worker did not finish")
			}
			voice.RLock()
			preserved := voice.wsConn == newConn && voice.close == newClose && voice.Ready && voice.speaking && voice.sequence == 42
			if opening != nil {
				preserved = voice.opening == opening && voice.wsConn == nil && voice.close == nil
			}
			reconnecting := voice.reconnecting
			voice.RUnlock()
			if !preserved {
				t.Error("old resume result changed the replacement voice connection")
			}
			if reconnecting {
				t.Error("old reconnect worker left reconnecting set")
			}
			backend.mu.Lock()
			closed := backend.closed
			backend.mu.Unlock()
			if closed != 0 {
				t.Errorf("old resume failure closed the replacement DAVE backend %d times", closed)
			}
		})
	}
}
