package discordgo

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceResumedQueuedTeardownPreservesNewerJoin(t *testing.T) {
	for _, newerJoin := range []bool{false, true} {
		name := "current_request"
		if newerJoin {
			name = "newer_join"
		}
		t.Run(name, func(t *testing.T) {
			gateway, gatewayPeer := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn, session.DataReady = gateway, true
			serverDone := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(serverDone)
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				var resume voiceResumeOp
				if err := conn.ReadJSON(&resume); err != nil || resume.Op != 7 {
					t.Errorf("resume request = %+v, %v", resume, err)
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":9,"seq":7,"d":null}`)); err != nil {
					return
				}
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}))
			defer server.Close()
			dialing, allowDial := make(chan struct{}), make(chan struct{})
			tlsDialer := &tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig}
			session.Dialer = &websocket.Dialer{HandshakeTimeout: time.Second, NetDialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				close(dialing)
				select {
				case <-allowDial:
					return tlsDialer.DialContext(ctx, network, address)
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
			udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
			if err != nil {
				t.Fatal(err)
			}
			lifetime := make(chan struct{})
			voice := &VoiceConnection{LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session,
				sessionID: "session", token: "dummy-token", endpoint: strings.TrimPrefix(server.URL, "https://"),
				Ready: true, udpConn: udp, aead: aead, close: lifetime,
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
				// End fallback only after destructive teardown has happened.
				session.Lock()
				delete(session.VoiceConnections, "guild")
				session.Unlock()
			}}
			installFakeDAVE(voice, backend, 1)
			paused, release, listenerStarted, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var resumed, pauseOnce atomic.Bool
			oldLogger := Logger
			Logger = func(_, _ int, format string, _ ...interface{}) {
				if format == "successfully resumed voice websocket" {
					resumed.Store(true)
				}
				if format != "called" {
					return
				}
				pc, _, _, _ := runtime.Caller(3)
				caller := runtime.FuncForPC(pc).Name()
				if strings.HasSuffix(caller, ".wsListen") {
					close(listenerStarted)
				}
				if strings.HasSuffix(caller, ".closeLocked") && resumed.Load() && pauseOnce.CompareAndSwap(false, true) {
					close(paused)
					<-release
				}
			}
			defer func() { Logger = oldLogger }()
			go func() {
				defer close(done)
				voice.reconnectWithResume(true, nil, lifetime)
			}()
			defer func() {
				closeChannel(allowDial)
				closeChannel(release)
				voice.Close()
				_ = session.Close()
				<-done
			}()
			select {
			case <-dialing:
			case <-time.After(3 * time.Second):
				t.Fatal("resume did not start dialing")
			}
			// Model a listener's previously queued non-resumable recovery worker
			// running while the gateway-triggered resume has no socket installed.
			voice.reconnectWithResume(false, nil, lifetime)
			close(allowDial)
			select {
			case <-paused:
			case <-time.After(3 * time.Second):
				t.Fatal("resumed recovery did not reach queued teardown")
			}
			<-listenerStarted
			voice.RLock()
			resumedConn := voice.wsConn
			voice.RUnlock()
			if newerJoin {
				joined, err := session.ChannelVoiceJoin("guild", "channel", false, false)
				if err != nil || joined != voice {
					t.Fatalf("newer join = %p, %v", joined, err)
				}
				_ = gatewayPeer.SetReadDeadline(time.Now().Add(time.Second))
				var join voiceChannelJoinOp
				if err := gatewayPeer.ReadJSON(&join); err != nil || join.Op != 4 || join.Data.ChannelID == nil || *join.Data.ChannelID != "channel" {
					t.Fatalf("newer join wire request = %+v, %v", join, err)
				}
			}
			close(release)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("resumed recovery did not finish")
			}
			voice.RLock()
			preserved := voice.wsConn == resumedConn && resumedConn != nil && voice.udpConn == udp && voice.close == lifetime && voice.Ready
			reconnecting := voice.reconnecting || voice.reconnectAgain
			voice.RUnlock()
			backend.mu.Lock()
			closed := backend.closed
			backend.mu.Unlock()
			if newerJoin && (!preserved || closed != 0) {
				t.Errorf("queued resume teardown invalidated newer successful join: preserved=%t, backend closes=%d", preserved, closed)
			} else if !newerJoin && (preserved || closed != 1) {
				t.Errorf("current queued teardown did not close transport: preserved=%t, backend closes=%d", preserved, closed)
			}
			if reconnecting {
				t.Error("recovery flags remained set")
			}
			voice.Close()
			<-serverDone
		})
	}
}
