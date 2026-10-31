package discordgo

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
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

func TestVoiceHandshakeCanceledOnClose(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, phase := range []string{"TLS", "HTTP"} {
			t.Run(fmt.Sprintf("resume=%v/%s", resume, phase), func(t *testing.T) {
				entered := make(chan struct{})
				dialer := &websocket.Dialer{}
				endpoint := "voice.invalid"
				if phase == "TLS" {
					client, peer := net.Pipe()
					defer func() { _ = client.Close() }()
					defer func() { _ = peer.Close() }()
					dialer.NetDialContext = func(context.Context, string, string) (net.Conn, error) {
						close(entered)
						return client, nil
					}
				} else {
					release := make(chan struct{})
					server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
						close(entered)
						select {
						case <-release:
						case <-r.Context().Done():
						}
					}))
					defer server.Close()
					defer close(release)
					endpoint = strings.TrimPrefix(server.URL, "https://")
					dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
				}
				voice := &VoiceConnection{
					GuildID: "guild", ChannelID: "1", UserID: "user",
					sessionID: "session", token: "dummy-token", endpoint: endpoint,
					close: make(chan struct{}), LogLevel: -1, session: &Session{Dialer: dialer},
				}
				done := make(chan error, 1)
				go func() {
					if resume {
						done <- voice.resumeVoice()
					} else {
						done <- voice.open()
					}
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("handshake did not begin")
				}
				voice.Close()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("canceled handshake succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("Close left the handshake running")
				}
				if voice.wsConn != nil {
					t.Fatal("canceled handshake installed a connection")
				}
			})
		}
	}
}

func TestVoiceResumeDialCanceledOnClose(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	voice := &VoiceConnection{
		GuildID: "guild", sessionID: "session", token: "dummy-token",
		endpoint: "voice.invalid", close: make(chan struct{}), LogLevel: -1,
		session: &Session{Dialer: &websocket.Dialer{
			NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				close(entered)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return nil, errors.New("test released stalled dial")
				}
			},
		}},
	}
	done := make(chan error, 1)
	go func() { done <- voice.resumeVoice() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not enter local dial stub")
	}
	voice.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("resume error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left the voice resume dial running")
	}
}

func TestVoiceDialSuccessfulConnectionSurvivesContextCleanup(t *testing.T) {
	for _, customTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("customTLS=%t", customTLS), func(t *testing.T) {
			testWebsocketConnectionSurvivesContextCleanup(t, customTLS)
		})
	}
}

func testWebsocketConnectionSurvivesContextCleanup(t *testing.T, customTLS bool) {
	t.Helper()
	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		peers <- conn
	}))
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second}
	if customTLS {
		server.StartTLS()
		tlsDialer := &tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig}
		dialer.NetDialTLSContext = tlsDialer.DialContext
	} else {
		server.Start()
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := dialWebsocket(ctx, dialer, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	peer := <-peers
	defer func() { _ = peer.Close() }()
	// Both the dialer's internal timeout context and our caller's context
	// are cleaned up once dialing returns. Neither owns the live socket.
	cancel()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("still connected")); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, message, err := peer.ReadMessage()
	if err != nil || string(message) != "still connected" {
		t.Fatalf("live socket after cleanup: message=%q, err=%v", message, err)
	}
}

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

func TestVoiceResumeStalledWriteTimesOut(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	handshake := make(chan error, 1)
	go func() {
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			handshake <- err
			return
		}
		_ = request.Body.Close()
		accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(peer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
		handshake <- err
		// Keep the peer open without reading the resume packet.
	}()
	transport := &observedVoiceWriteConn{Conn: client, entered: make(chan struct{})}
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second, NetDialTLSContext: func(context.Context, string, string) (net.Conn, error) {
		// This fixture supplies an established transport, without real TLS.
		return transport, nil
	}}
	voice := &VoiceConnection{
		LogLevel: -1, GuildID: "guild", sessionID: "session", token: "dummy-token",
		endpoint: "voice.invalid", close: make(chan struct{}), session: &Session{Dialer: dialer},
	}
	done := make(chan struct{})
	var resumeErr error
	go func() {
		defer close(done)
		resumeErr = voice.resumeVoice()
	}()
	t.Cleanup(func() { voice.Close(); _ = client.Close(); <-done })
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not reach its transport write")
	}
	select {
	case <-done:
		var timeout net.Error
		if !errors.As(resumeErr, &timeout) || !timeout.Timeout() {
			t.Fatalf("resume error = %v, want a write timeout", resumeErr)
		}
	case <-time.After(2 * websocketWriteTimeout):
		t.Fatal("stalled resume write did not time out")
	}
	if voice.wsConn != nil {
		t.Error("failed resume installed a websocket")
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read error = %v, want EOF after resume cleanup", err)
	}
}

func TestVoiceResumeClearsWriteDeadline(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var resume voiceResumeOp
		if err := conn.ReadJSON(&resume); err != nil {
			return
		}
		if resume.Op != 7 {
			t.Errorf("resume opcode = %d, want 7", resume.Op)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":9,"d":null}`)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	transport := &activatedWriteBlockingConn{deadlines: make(chan time.Time, 2)}
	tlsDialer := &tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig}
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second, NetDialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := tlsDialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		transport.Conn = conn
		return transport, nil
	}}
	voice := &VoiceConnection{
		LogLevel: -1, GuildID: "guild", sessionID: "session", token: "dummy-token",
		endpoint: strings.TrimPrefix(server.URL, "https://"), close: make(chan struct{}), session: &Session{Dialer: dialer},
	}
	defer voice.Close()
	if err := voice.resumeVoice(); err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; deadline.IsZero() {
		t.Error("resume write had no deadline")
	}
	voice.RLock()
	conn := voice.wsConn
	voice.RUnlock()
	if conn == nil {
		t.Fatal("successful resume did not install a websocket")
	}
	voice.wsMutex.Lock()
	err := conn.WriteJSON(struct{}{})
	voice.wsMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; !deadline.IsZero() {
		t.Error("resume left its deadline on the next write")
	}
}
