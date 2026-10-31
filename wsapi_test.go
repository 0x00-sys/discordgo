package discordgo

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
)

func TestGatewayStartupReadTimeout(t *testing.T) {
	tests := []struct {
		name   string
		dialer *websocket.Dialer
		want   time.Duration
	}{
		{name: "nil dialer", dialer: nil, want: 45 * time.Second},
		{name: "configured handshake timeout", dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second}, want: 10 * time.Second},
		{name: "zero handshake timeout", dialer: &websocket.Dialer{}, want: 45 * time.Second},
		{name: "negative handshake timeout", dialer: &websocket.Dialer{HandshakeTimeout: -time.Second}, want: 45 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gatewayStartupReadTimeout(tt.dialer); got != tt.want {
				t.Fatalf("gatewayStartupReadTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOnEventSkipsRedactionWhenDebugDisabled(t *testing.T) {
	payload := strings.Builder{}
	payload.WriteString(`{"op":11,"d":{`)
	for i := 0; i < 1000; i++ {
		if i > 0 {
			payload.WriteString(",")
		}
		fmt.Fprintf(&payload, `"field%d":"value%d"`, i, i)
	}
	payload.WriteString(`}}`)
	message := []byte(payload.String())

	s := &Session{} // default LogLevel is LogError

	allocs := testing.AllocsPerRun(20, func() {
		if _, err := s.onEvent(websocket.TextMessage, message); err != nil {
			t.Fatalf("onEvent() error = %v", err)
		}
	})

	// Redacting the payload unmarshals and re-marshals every field
	// (thousands of allocations); the plain envelope decode stays far
	// below that.
	if allocs > 500 {
		t.Fatalf("onEvent() allocations = %v with debug logging disabled, want <= 500", allocs)
	}
}

func TestOnEventLogsRedactedDataWhenDebugEnabled(t *testing.T) {
	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	var logged []string
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, a...))
	}

	s := &Session{LogLevel: LogDebug}
	message := []byte(`{"op":11,"d":{"token":"secret-token"}}`)
	if _, err := s.onEvent(websocket.TextMessage, message); err != nil {
		t.Fatalf("onEvent() error = %v", err)
	}

	found := false
	for _, line := range logged {
		if strings.Contains(line, "field is redacted") || strings.Contains(line, "REDACTED") {
			found = true
		}
		if strings.Contains(line, "secret-token") {
			t.Fatalf("debug log leaked token: %q", line)
		}
	}
	if !found {
		t.Fatalf("debug log did not include redacted gateway data: %q", logged)
	}
}

func TestOnEventOp1NilWsConn(t *testing.T) {
	seq := int64(0)
	s := &Session{sequence: &seq}

	_, err := s.onEvent(websocket.TextMessage, []byte(`{"op":1,"s":0,"t":"","d":null}`))
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("onEvent() error = %v, want %v", err, ErrWSNotFound)
	}
	if !s.LastHeartbeatSent.IsZero() {
		t.Fatalf("LastHeartbeatSent = %v after failed write, want zero", s.LastHeartbeatSent)
	}
}

func TestOnEventOp1RecordsHeartbeatSent(t *testing.T) {
	conn, peer := newGatewayTestConnection(t)
	s := &Session{sequence: new(int64), wsConn: conn}

	read := make(chan error, 1)
	go func() {
		_, _, err := peer.ReadMessage()
		read <- err
	}()

	if _, err := s.onEvent(websocket.TextMessage, []byte(`{"op":1,"d":null}`)); err != nil {
		t.Fatalf("onEvent() error = %v", err)
	}
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("gateway peer ReadMessage returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("gateway peer did not receive requested heartbeat")
	}
	if s.LastHeartbeatSent.IsZero() {
		t.Fatal("LastHeartbeatSent was not recorded for requested heartbeat")
	}
}

func TestOnEventRejectsNullEvent(t *testing.T) {
	s := &Session{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("onEvent() panicked: %v", r)
		}
	}()

	_, err := s.onEvent(websocket.TextMessage, []byte(`null`))
	if err == nil {
		t.Fatal("onEvent() error = nil, want non-nil")
	}
}

func TestOnEventReconnectsAfterMalformedInvalidSession(t *testing.T) {
	var attempts int32
	server := newGatewayCloseAfterIdentifyTestServer(t, 4014, &attempts)
	s, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	s.sessionID = "old-session"
	s.resumeGatewayURL = s.gateway
	atomic.StoreInt64(s.sequence, 42)

	_, err = s.onEvent(websocket.TextMessage, []byte(`{"op":9,"d":{"unexpected":true}}`))
	if err == nil {
		t.Fatal("onEvent() error = nil, want malformed invalid session error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("gateway reconnect attempts = %d, want 1", got)
	}
	if s.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", s.sessionID)
	}
	if s.resumeGatewayURL != "" {
		t.Fatalf("resumeGatewayURL = %q, want empty", s.resumeGatewayURL)
	}
	if sequence := atomic.LoadInt64(s.sequence); sequence != 0 {
		t.Fatalf("sequence = %d, want 0", sequence)
	}
}

func TestOnVoiceStateUpdateHandlesMalformedEvent(t *testing.T) {
	tests := []struct {
		name  string
		event *VoiceStateUpdate
	}{
		{name: "nil event", event: nil},
		{name: "missing voice state", event: &VoiceStateUpdate{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("onVoiceStateUpdate panicked: %v", r)
				}
			}()

			s.onVoiceStateUpdate(tt.event)
		})
	}
}

func TestVoiceEventHandlersHandleIncompleteState(t *testing.T) {
	t.Run("nil server update", func(t *testing.T) {
		new(Session).onVoiceServerUpdate(nil, nil, nil, 0)
	})

	t.Run("nil voice connection", func(t *testing.T) {
		s := &Session{VoiceConnections: map[string]*VoiceConnection{"guild": nil}}
		s.onVoiceServerUpdate(nil, &VoiceServerUpdate{GuildID: "guild"}, nil, 0)
	})

	t.Run("state update before READY", func(t *testing.T) {
		s := &Session{
			State:            NewState(),
			VoiceConnections: map[string]*VoiceConnection{"guild": {}},
		}
		s.onVoiceStateUpdate(&VoiceStateUpdate{VoiceState: &VoiceState{
			GuildID:   "guild",
			ChannelID: "channel",
			UserID:    "user",
		}})
	})
}

func TestInternalVoiceEventPanicIsRecovered(t *testing.T) {
	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	panicLogged := make(chan struct{}, 1)
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		if strings.Contains(fmt.Sprintf(format, a...), "panic in VOICE_SERVER_UPDATE handler") {
			panicLogged <- struct{}{}
		}
	}

	closed := make(chan struct{})
	close(closed)
	s := &Session{
		State: NewState(),
		VoiceConnections: map[string]*VoiceConnection{
			"guild": {LogLevel: -1, close: closed},
		},
	}
	s.onInterface(&VoiceServerUpdate{GuildID: "guild"})

	select {
	case <-panicLogged:
	case <-time.After(time.Second):
		t.Fatal("internal voice event panic was not recovered")
	}
}

func TestChannelVoiceJoinManualNilWsConn(t *testing.T) {
	s := &Session{}

	err := s.ChannelVoiceJoinManual("guild", "channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChannelVoiceJoinManual() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestChannelVoiceJoinManualWaitsForVoiceOperation(t *testing.T) {
	s := &Session{}
	s.voiceMutex.Lock()

	done := make(chan error, 1)
	go func() {
		done <- s.ChannelVoiceJoinManual("guild", "channel", false, false)
	}()

	select {
	case err := <-done:
		s.voiceMutex.Unlock()
		t.Fatalf("ChannelVoiceJoinManual returned before voice operation completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	s.voiceMutex.Unlock()
	if err := <-done; !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChannelVoiceJoinManual() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestChannelVoiceJoinReplacesDisconnectingConnection(t *testing.T) {
	stale := &VoiceConnection{disconnecting: true}
	s := &Session{
		VoiceConnections: map[string]*VoiceConnection{
			"guild": stale,
		},
	}

	voice, err := s.ChannelVoiceJoin("guild", "channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChannelVoiceJoin() error = %v, want %v", err, ErrWSNotFound)
	}
	if voice == stale {
		t.Fatal("ChannelVoiceJoin reused a disconnecting voice connection")
	}

	s.RLock()
	registered := s.VoiceConnections["guild"]
	s.RUnlock()
	if registered != voice {
		t.Fatal("replacement voice connection was not registered")
	}
}

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

func TestOpenClearsResumeStateOnMalformedInvalidSessionDuringOpen(t *testing.T) {
	server := newGatewayOpenTestServer(t, []byte(`{"op":9,"d":{"unexpected":true}}`))
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

func TestOpenClearsResumeStateOnNewSessionCloseCodes(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{
			name: "invalid sequence",
			code: 4007,
		},
		{
			name: "session timed out",
			code: 4009,
		},
		{
			name: "not authenticated",
			code: 4003,
		},
		{
			name: "already authenticated",
			code: 4005,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts int32
			server := newGatewayCloseAfterIdentifyTestServer(t, tt.code, &attempts)
			session, err := newGatewayOpenTestSession(server, "Bot test")
			if err != nil {
				t.Fatalf("error creating session: %v", err)
			}
			session.sessionID = "old-session"
			session.resumeGatewayURL = session.gateway
			atomic.StoreInt64(session.sequence, 42)

			err = openWithTimeout(t, session)
			if err == nil {
				t.Fatal("expected Open to return a gateway close error")
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
			if got := atomic.LoadInt32(&attempts); got != 1 {
				t.Fatalf("gateway connection attempts = %d, want 1", got)
			}
		})
	}
}

func TestCloseWithCodeClearsResumeStateOnNormalCloseCodes(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{
			name: "normal closure",
			code: websocket.CloseNormalClosure,
		},
		{
			name: "going away",
			code: websocket.CloseGoingAway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{
				sessionID:        "old-session",
				resumeGatewayURL: "wss://resume.gateway",
				sequence:         new(int64),
			}
			atomic.StoreInt64(session.sequence, 42)

			if err := session.CloseWithCode(tt.code); err != nil {
				t.Fatalf("CloseWithCode() error = %v", err)
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
		})
	}
}

func TestCloseWithCodePreservesResumeStateOnReconnectableCloseCode(t *testing.T) {
	session := &Session{
		sessionID:        "old-session",
		resumeGatewayURL: "wss://resume.gateway",
		sequence:         new(int64),
	}
	atomic.StoreInt64(session.sequence, 42)

	if err := session.CloseWithCode(websocket.CloseServiceRestart); err != nil {
		t.Fatalf("CloseWithCode() error = %v", err)
	}
	if session.sessionID != "old-session" {
		t.Fatalf("sessionID = %q, want old-session", session.sessionID)
	}
	if session.resumeGatewayURL != "wss://resume.gateway" {
		t.Fatalf("resumeGatewayURL = %q, want wss://resume.gateway", session.resumeGatewayURL)
	}
	if sequence := atomic.LoadInt64(session.sequence); sequence != 42 {
		t.Fatalf("sequence = %d, want 42", sequence)
	}
}

func TestInvalidSessionClearsResumeStateConcurrentRead(t *testing.T) {
	session := &Session{
		ShouldReconnectOnError: false,
		sequence:               new(int64),
	}

	done := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-done:
				return
			default:
				session.RLock()
				_, _ = session.sessionID, session.resumeGatewayURL
				session.RUnlock()
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		session.Lock()
		session.sessionID = "session"
		session.resumeGatewayURL = "wss://gateway.example"
		session.Unlock()

		if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":9,"d":false}`)); err != nil {
			close(done)
			<-readerDone
			t.Fatalf("onEvent returned error: %v", err)
		}
	}

	close(done)
	<-readerDone
}

func TestOpenHandlesHeartbeatAckDuringOpen(t *testing.T) {
	server := newGatewayOpenTestServer(t,
		[]byte(`{"op":11,"d":null}`),
		[]byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`),
	)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	if err = openWithTimeout(t, session); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()
}

func TestOpenResetsHeartbeatTimingAfterClockAdjustment(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return time.Hour })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)
	server := newGatewayOpenTestServer(t,
		[]byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","user":{"id":"user"},"guilds":[]}}`),
	)
	s, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatal(err)
	}
	s.ShouldReconnectOnError = false
	defer s.Close()
	if s.lastHeartbeatAck == s.lastHeartbeatAck.Round(0) || !s.LastHeartbeatAck.Equal(s.lastHeartbeatAck) || s.LastHeartbeatAck.Location() != time.UTC {
		t.Fatal("New must initialize monotonic ACK time and its public UTC timestamp")
	}
	for attempt := 0; attempt < 2; attempt++ {
		// Leave a pending heartbeat from the previous connection, then move
		// its wall time forwards (equivalent to a backwards clock step).
		s.Lock()
		s.lastHeartbeatSent = time.Now()
		s.LastHeartbeatSent = s.lastHeartbeatSent.UTC()
		s.Unlock()
		adjustHeartbeatClock(t, s, -time.Hour)
		if err := openWithTimeout(t, s); err != nil {
			t.Fatal(err)
		}
		s.RLock()
		reset := s.lastHeartbeatSent.IsZero() && s.LastHeartbeatSent.IsZero()
		monotonic := s.lastHeartbeatAck != s.lastHeartbeatAck.Round(0)
		utc := s.LastHeartbeatAck.Location() == time.UTC && s.LastHeartbeatAck.Equal(s.lastHeartbeatAck)
		s.RUnlock()
		if !reset || !monotonic || !utc {
			t.Fatalf("Open attempt %d: reset=%v monotonic=%v UTC=%v", attempt, reset, monotonic, utc)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsInvalidHeartbeatInterval(t *testing.T) {
	server := newGatewayOpenTestServerWithHello(t, []byte(`{"op":10,"d":{"heartbeat_interval":0}}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	err = openWithTimeout(t, session)
	if err == nil {
		t.Fatal("Open returned nil error, want invalid heartbeat interval error")
	}
	if !strings.Contains(err.Error(), "invalid gateway heartbeat interval") {
		t.Fatalf("Open returned error %q, want invalid heartbeat interval", err)
	}
}

func TestOpenRejectsHeartbeatIntervalOverflow(t *testing.T) {
	// 1e13 msec overflows the millisecond-to-Duration conversion.
	server := newGatewayOpenTestServerWithHello(t, []byte(`{"op":10,"d":{"heartbeat_interval":10000000000000}}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	err = openWithTimeout(t, session)
	if err == nil {
		session.Close()
		t.Fatal("Open returned nil error, want invalid heartbeat interval error")
	}
	if !strings.Contains(err.Error(), "invalid gateway heartbeat interval") {
		t.Fatalf("Open returned error %q, want invalid heartbeat interval", err)
	}
}

func TestMaxGatewayHeartbeatIntervalCannotOverflowDuration(t *testing.T) {
	if FailedHeartbeatAcks != time.Millisecond {
		t.Fatalf("FailedHeartbeatAcks = %v, want one interval", FailedHeartbeatAcks)
	}
	if maxGatewayHeartbeatIntervalMsec > time.Duration(1<<63-1)/time.Millisecond {
		t.Fatalf("maxGatewayHeartbeatIntervalMsec %d allows interval*time.Millisecond to overflow", maxGatewayHeartbeatIntervalMsec)
	}
}

func TestSessionCloseWithLockedWSMutex(t *testing.T) {
	server := newCloseFrameTestServer(t)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	session := &Session{
		LogLevel:  -1,
		wsConn:    conn,
		listening: make(chan interface{}),
		sequence:  new(int64),
	}

	session.wsMutex.Lock()
	done := make(chan error, 1)
	go func() {
		done <- session.Close()
	}()

	requireCloseDoneWhileWSMutexLocked(t, func() {
		session.wsMutex.Unlock()
	}, done)
}

func TestSessionCloseDisconnectsVoiceConnections(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}

	closeChan := make(chan struct{})
	session := &Session{LogLevel: -1, VoiceConnections: make(map[string]*VoiceConnection), sequence: new(int64)}
	voice := &VoiceConnection{
		GuildID:  "guild",
		LogLevel: -1,
		session:  session,
		close:    closeChan,
		udpConn:  udpConn,
		OpusSend: make(chan []byte),
		OpusRecv: make(chan *Packet),
	}
	defer voice.Close()
	session.VoiceConnections[voice.GuildID] = voice

	if err = session.Close(); err != nil {
		t.Fatalf("Session.Close() error = %v", err)
	}

	select {
	case <-closeChan:
	default:
		t.Fatal("Session.Close did not stop voice goroutines")
	}
	voice.RLock()
	udpConn = voice.udpConn
	disconnecting := voice.disconnecting
	opusSend := voice.OpusSend
	opusRecv := voice.OpusRecv
	voice.RUnlock()
	if udpConn != nil || !disconnecting || opusSend != nil || opusRecv != nil {
		t.Fatalf("voice teardown = udp %v, disconnecting %t, opus send %v, opus recv %v", udpConn, disconnecting, opusSend, opusRecv)
	}
	session.RLock()
	_, registered := session.VoiceConnections[voice.GuildID]
	session.RUnlock()
	if registered {
		t.Fatal("Session.Close left voice connection registered")
	}
}

func TestCloseWithCodeDoesNotSleepAfterCloseFrame(t *testing.T) {
	closeCodes := make(chan int, 1)
	server := newCloseCodeTestServer(t, closeCodes)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	session := &Session{
		LogLevel:  -1,
		wsConn:    conn,
		listening: make(chan interface{}),
		sequence:  new(int64),
	}

	start := time.Now()
	if err := session.CloseWithCode(websocket.CloseServiceRestart); err != nil {
		t.Fatalf("CloseWithCode() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("CloseWithCode() took %v, want under 500ms", elapsed)
	}

	select {
	case code := <-closeCodes:
		if code != websocket.CloseServiceRestart {
			t.Fatalf("close code = %d, want %d", code, websocket.CloseServiceRestart)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive websocket close frame")
	}
}

func TestGatewayWriteDoesNotBlockCloseWithLockedWSMutex(t *testing.T) {
	server := newCloseFrameTestServer(t)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	session := &Session{
		LogLevel:  -1,
		wsConn:    conn,
		listening: make(chan interface{}),
		sequence:  new(int64),
	}

	session.wsMutex.Lock()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- session.GatewayWriteStruct(struct {
			Op int `json:"op"`
		}{Op: 3})
	}()

	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close()
	}()

	requireCloseDoneWhileWSMutexLocked(t, func() {
		session.wsMutex.Unlock()
	}, closeDone)

	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("GatewayWriteStruct did not return after close")
	}
}

func TestGatewayWriteTimesOutWhenPeerStopsReading(t *testing.T) {
	unblock := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-unblock
	}))
	defer server.Close()
	defer close(unblock)

	dialer := websocket.Dialer{NetDial: func(network, address string) (net.Conn, error) {
		conn, err := net.Dial(network, address)
		if err != nil {
			return nil, err
		}
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			if err = tcpConn.SetWriteBuffer(1024); err != nil {
				conn.Close()
				return nil, err
			}
		}
		return conn, nil
	}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	session := &Session{LogLevel: -1, wsConn: conn}
	done := make(chan error, 1)
	go func() {
		done <- session.GatewayWriteStruct(strings.Repeat("x", 8<<20))
	}()

	select {
	case err = <-done:
		if err == nil {
			t.Fatal("GatewayWriteStruct() error = nil, want write timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GatewayWriteStruct did not time out when peer stopped reading")
	}
}

func TestOpenSendsHeartbeatsBeforeReady(t *testing.T) {
	heartbeatRead := make(chan struct{}, 1)
	server := newGatewayOpenAfterHeartbeatTestServer(t, heartbeatRead)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	if err = openWithTimeout(t, session); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()

	select {
	case <-heartbeatRead:
	case <-time.After(time.Second):
		t.Fatal("gateway did not receive a heartbeat before READY")
	}
}

func TestOpenIdentifyWriteDoesNotBlockCloseWithLockedWSMutex(t *testing.T) {
	server := newGatewayOpenTestServer(t)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	session.wsMutex.Lock()
	openDone := make(chan error, 1)
	go func() {
		openDone <- session.Open()
	}()

	waitForWSConn(t, session)
	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close()
	}()

	requireCloseDoneWhileWSMutexLocked(t, func() {
		session.wsMutex.Unlock()
	}, closeDone)

	select {
	case <-openDone:
	case <-time.After(time.Second):
		t.Fatal("Open did not return after close")
	}
}

func TestOpenReturnsOnUnexpectedDispatchDuringIdentify(t *testing.T) {
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

func TestOpenAllowsDispatchReplayBeforeResumed(t *testing.T) {
	server := newGatewayOpenTestServer(t,
		[]byte(`{"op":0,"s":43,"t":"MESSAGE_CREATE","d":{"id":"message","channel_id":"channel","guild_id":"guild","content":"hello","timestamp":"2026-01-01T00:00:00.000000+00:00","edited_timestamp":null,"tts":false,"mention_everyone":false,"mentions":[],"mention_roles":[],"attachments":[],"embeds":[],"pinned":false,"type":0,"author":{"id":"user","username":"user","discriminator":"0001","bot":false}}}`),
		[]byte(`{"op":0,"s":44,"t":"RESUMED","d":{}}`),
	)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.ShouldReconnectOnError = false
	session.SyncEvents = true
	session.sessionID = "old-session"
	session.resumeGatewayURL = session.gateway
	atomic.StoreInt64(session.sequence, 42)

	called := false
	session.AddHandler(func(s *Session, m *MessageCreate) {
		s.RLock()
		s.RUnlock()
		called = true
	})

	if err = openWithTimeout(t, session); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()

	if !called {
		t.Fatal("replayed dispatch handler was not called")
	}
	if sequence := atomic.LoadInt64(session.sequence); sequence != 44 {
		t.Fatalf("sequence = %d, want 44", sequence)
	}
}

func TestOpenReadyHandlerCanUseSessionLock(t *testing.T) {
	server := newGatewayOpenTestServer(t, []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.ShouldReconnectOnError = false
	session.SyncEvents = true

	called := false
	session.AddHandler(func(s *Session, r *Ready) {
		s.RLock()
		s.RUnlock()
		called = true
	})

	if err = openWithTimeout(t, session); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()

	if !called {
		t.Fatal("READY handler was not called")
	}
}

func TestOpenConnectHandlerCanUseSessionLock(t *testing.T) {
	server := newGatewayOpenTestServer(t, []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`))
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.ShouldReconnectOnError = false
	session.SyncEvents = true

	called := false
	session.AddHandler(func(s *Session, c *Connect) {
		s.RLock()
		s.RUnlock()
		called = true
	})

	if err = openWithTimeout(t, session); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer session.Close()

	if !called {
		t.Fatal("Connect handler was not called")
	}
}

func TestReconnectStopsAfterTerminalCloseDuringOpen(t *testing.T) {
	var attempts int32
	server := newGatewayCloseAfterIdentifyTestServer(t, 4014, &attempts)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	done := make(chan struct{})
	go func() {
		session.reconnect(session.reconnectCancelSignal())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reconnect did not stop after terminal gateway close")
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("gateway connection attempts = %d, want 1", got)
	}
}

func TestReconnectVoiceSchedulingDoesNotHoldSessionLock(t *testing.T) {
	serverDone := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		ready := []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`)
		if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
			return
		}
		<-serverDone
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { closeChannel(serverDone) })

	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.ShouldReconnectVoiceOnSessionError = true
	voice := &VoiceConnection{GuildID: "guild", session: session}
	session.VoiceConnections = map[string]*VoiceConnection{
		"guild": voice,
	}
	t.Cleanup(func() { _ = session.Close() })
	t.Cleanup(func() {
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
	})

	done := make(chan struct{})
	go func() {
		session.reconnect(session.reconnectCancelSignal())
		close(done)
	}()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(2 * time.Second)
	for {
		voice.RLock()
		reconnecting := voice.reconnecting
		voice.RUnlock()
		if reconnecting {
			break
		}
		select {
		case <-ticker.C:
		case <-timeout:
			t.Fatal("reconnect did not start voice reconnection")
		}
	}

	lockAcquired := make(chan struct{})
	go func() {
		session.Lock()
		close(lockAcquired)
		session.Unlock()
	}()

	select {
	case <-lockAcquired:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("voice reconnect scheduling blocked the session write lock")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not return")
	}
}

func TestReconnectVoiceConnectionsSkipsNil(t *testing.T) {
	session := &Session{
		VoiceConnections: map[string]*VoiceConnection{"guild": nil},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("reconnectVoiceConnections panicked: %v", recovered)
		}
	}()

	session.reconnectVoiceConnections(session.reconnectCancelSignal())
}

func newGatewayTestConnection(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()

	peerReady := make(chan *websocket.Conn, 1)
	done := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		peerReady <- conn
		<-done
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(done) })

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	peer := <-peerReady
	t.Cleanup(func() {
		_ = conn.Close()
		_ = peer.Close()
	})

	return conn, peer
}

func closeChannel(channel chan struct{}) {
	select {
	case <-channel:
	default:
		close(channel)
	}
}

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

func TestListenDoesNotCloseReplacementConnection(t *testing.T) {
	oldConn, oldPeer := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	oldListening := make(chan interface{})
	newListening := make(chan interface{})

	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	logReached := make(chan struct{})
	releaseLog := make(chan struct{})
	defer closeChannel(releaseLog)
	var blockLog sync.Once
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		if strings.Contains(fmt.Sprintf(format, a...), "error reading from gateway") {
			blockLog.Do(func() {
				close(logReached)
				<-releaseLog
			})
		}
	}

	session := &Session{
		LogLevel:               LogWarning,
		ShouldReconnectOnError: false,
		SyncEvents:             true,
		sequence:               new(int64),
		wsConn:                 oldConn,
		listening:              oldListening,
	}

	disconnected := false
	session.AddHandler(func(*Session, *Disconnect) {
		disconnected = true
	})

	done := make(chan struct{})
	go func() {
		session.listen(oldConn, oldListening)
		close(done)
	}()

	if err := oldPeer.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4007, "invalid sequence"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("old peer WriteControl returned error: %v", err)
	}

	select {
	case <-logReached:
	case <-time.After(time.Second):
		t.Fatal("listener did not reach gateway read error")
	}

	session.Lock()
	session.DataReady = true
	session.resumeGatewayURL = "wss://replacement.gateway"
	session.sessionID = "replacement-session"
	atomic.StoreInt64(session.sequence, 42)
	session.wsConn = newConn
	session.listening = newListening
	session.Unlock()
	close(releaseLog)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not return")
	}

	session.RLock()
	gotConn := session.wsConn
	gotListening := session.listening
	gotDataReady := session.DataReady
	gotResumeGatewayURL := session.resumeGatewayURL
	gotSessionID := session.sessionID
	session.RUnlock()
	if gotConn != newConn {
		t.Fatal("stale listener replaced the current gateway connection")
	}
	if gotListening != newListening {
		t.Fatal("stale listener replaced the current listening channel")
	}
	if !gotDataReady {
		t.Fatal("stale listener marked the replacement connection not ready")
	}
	if gotResumeGatewayURL != "wss://replacement.gateway" || gotSessionID != "replacement-session" || atomic.LoadInt64(session.sequence) != 42 {
		t.Fatal("stale listener reset the replacement gateway session")
	}
	select {
	case <-newListening:
		t.Fatal("stale listener closed the replacement listening channel")
	default:
	}
	if disconnected {
		t.Fatal("stale listener emitted a disconnect for the replacement connection")
	}
	if err := newConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("replacement connection WriteControl returned error: %v", err)
	}

	close(oldListening)
	close(newListening)
}

func TestHeartbeatDoesNotCloseReplacementConnection(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	oldConn, _ := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	oldListening := make(chan interface{})
	newListening := make(chan interface{})

	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	logReached := make(chan struct{})
	releaseLog := make(chan struct{})
	defer closeChannel(releaseLog)
	var blockLog sync.Once
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		if strings.Contains(fmt.Sprintf(format, a...), "error sending heartbeat") {
			blockLog.Do(func() {
				close(logReached)
				<-releaseLog
			})
		}
	}

	session := &Session{
		lastHeartbeatAck:       time.Now().Add(time.Hour),
		LogLevel:               LogError,
		ShouldReconnectOnError: false,
		SyncEvents:             true,
		sequence:               new(int64),
		wsConn:                 oldConn,
		listening:              oldListening,
	}

	disconnected := false
	session.AddHandler(func(*Session, *Disconnect) {
		disconnected = true
	})

	if err := oldConn.Close(); err != nil {
		t.Fatalf("old connection Close returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		session.heartbeat(oldConn, oldListening, 1)
		close(done)
	}()

	select {
	case <-logReached:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not reach gateway write error")
	}

	session.Lock()
	session.DataReady = true
	session.wsConn = newConn
	session.listening = newListening
	session.Unlock()
	close(releaseLog)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not return")
	}

	session.RLock()
	gotConn := session.wsConn
	gotListening := session.listening
	gotDataReady := session.DataReady
	session.RUnlock()
	if gotConn != newConn {
		t.Fatal("stale heartbeat replaced the current gateway connection")
	}
	if gotListening != newListening {
		t.Fatal("stale heartbeat replaced the current listening channel")
	}
	if !gotDataReady {
		t.Fatal("stale heartbeat marked the replacement connection not ready")
	}
	select {
	case <-newListening:
		t.Fatal("stale heartbeat closed the replacement listening channel")
	default:
	}
	if disconnected {
		t.Fatal("stale heartbeat emitted a disconnect for the replacement connection")
	}
	if err := newConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("replacement connection WriteControl returned error: %v", err)
	}

	close(oldListening)
	close(newListening)
}

func TestRequestGuildMembersUsesSingleGuildID(t *testing.T) {
	writes := captureRequestGuildMembersWrites(t, 1, func(session *Session) error {
		return session.RequestGuildMembers("guild", "", 0, "nonce", false)
	})

	if writes[0].Op != 8 {
		t.Fatalf("op = %d, want 8", writes[0].Op)
	}
	if writes[0].Data.GuildID != "guild" {
		t.Fatalf("guild_id = %q, want guild", writes[0].Data.GuildID)
	}
	if writes[0].Data.Query == nil || *writes[0].Data.Query != "" {
		t.Fatalf("query = %v, want empty string", writes[0].Data.Query)
	}
}

func TestRequestGuildMembersListUsesSingleGuildID(t *testing.T) {
	writes := captureRequestGuildMembersWrites(t, 1, func(session *Session) error {
		return session.RequestGuildMembersList("guild", []string{"user"}, 1, "nonce", false)
	})

	if writes[0].Data.GuildID != "guild" {
		t.Fatalf("guild_id = %q, want guild", writes[0].Data.GuildID)
	}
	if len(writes[0].Data.UserIDs) != 1 || writes[0].Data.UserIDs[0] != "user" {
		t.Fatalf("user_ids = %v, want [user]", writes[0].Data.UserIDs)
	}
}

func TestRequestGuildMembersBatchSendsOnePayloadPerGuild(t *testing.T) {
	writes := captureRequestGuildMembersWrites(t, 2, func(session *Session) error {
		return session.RequestGuildMembersBatch([]string{"guild-1", "guild-2"}, "a", 100, "nonce", false)
	})

	for i, guildID := range []string{"guild-1", "guild-2"} {
		if writes[i].Data.GuildID != guildID {
			t.Fatalf("write %d guild_id = %q, want %s", i, writes[i].Data.GuildID, guildID)
		}
	}
}

func TestRequestGuildMembersBatchListSendsOnePayloadPerGuild(t *testing.T) {
	writes := captureRequestGuildMembersWrites(t, 2, func(session *Session) error {
		return session.RequestGuildMembersBatchList([]string{"guild-1", "guild-2"}, []string{"user"}, 1, "nonce", false)
	})

	for i, guildID := range []string{"guild-1", "guild-2"} {
		if writes[i].Data.GuildID != guildID {
			t.Fatalf("write %d guild_id = %q, want %s", i, writes[i].Data.GuildID, guildID)
		}
		if len(writes[i].Data.UserIDs) != 1 || writes[i].Data.UserIDs[0] != "user" {
			t.Fatalf("write %d user_ids = %v, want [user]", i, writes[i].Data.UserIDs)
		}
	}
}

func TestRequestSoundboardSoundsWritesGatewayOp(t *testing.T) {
	writes := captureGatewayWrites(t, 1, func(session *Session) error {
		return session.RequestSoundboardSounds([]string{"guild-1", "guild-2"})
	})

	if writes[0].Op != 31 {
		t.Fatalf("op = %d, want 31", writes[0].Op)
	}
	var data requestSoundboardSoundsData
	if err := json.Unmarshal(writes[0].Data, &data); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(data.GuildIDs) != 2 || data.GuildIDs[0] != "guild-1" || data.GuildIDs[1] != "guild-2" {
		t.Fatalf("guild_ids = %#v, want [guild-1 guild-2]", data.GuildIDs)
	}
}

func TestRequestChannelInfoWritesGatewayOp(t *testing.T) {
	writes := captureGatewayWrites(t, 1, func(session *Session) error {
		return session.RequestChannelInfo("guild", []string{"status", "voice_start_time"})
	})

	if writes[0].Op != 43 {
		t.Fatalf("op = %d, want 43", writes[0].Op)
	}
	var data requestChannelInfoData
	if err := json.Unmarshal(writes[0].Data, &data); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if data.GuildID != "guild" {
		t.Fatalf("guild_id = %q, want guild", data.GuildID)
	}
	if len(data.Fields) != 2 || data.Fields[0] != "status" || data.Fields[1] != "voice_start_time" {
		t.Fatalf("fields = %#v, want [status voice_start_time]", data.Fields)
	}
}

type gatewayWrite struct {
	Op   int             `json:"op"`
	Data json.RawMessage `json:"d"`
}

func TestGatewayWriteWaitsAfterSendRateLimit(t *testing.T) {
	now := time.Unix(1, 0)
	waits := make([]time.Duration, 0, 1)
	writes := captureGatewayWrites(t, gatewaySendRateLimit+1, func(session *Session) error {
		session.gatewaySendRateLimiter.now = func() time.Time { return now }
		session.gatewaySendRateLimiter.wait = func(delay time.Duration, _ <-chan struct{}) bool {
			waits = append(waits, delay)
			now = now.Add(delay)
			return true
		}

		for i := 0; i <= gatewaySendRateLimit; i++ {
			if err := session.GatewayWriteStruct(struct {
				Op int `json:"op"`
			}{Op: 3}); err != nil {
				return err
			}
		}
		return nil
	})

	if len(waits) != 1 || waits[0] != gatewaySendRateLimitWindow {
		t.Fatalf("rate limit waits = %v, want [%v]", waits, gatewaySendRateLimitWindow)
	}
	for i, write := range writes {
		if write.Op != 3 {
			t.Fatalf("write %d op = %d, want 3", i, write.Op)
		}
	}
}

func TestGatewaySendRateLimiterConcurrentReset(t *testing.T) {
	now := time.Unix(1, 0)
	waitStarted := make(chan struct{}, 1)
	limiter := gatewaySendRateLimiter{
		now: func() time.Time { return now },
		wait: func(_ time.Duration, cancel <-chan struct{}) bool {
			waitStarted <- struct{}{}
			<-cancel
			return false
		},
	}
	oldConnection := &websocket.Conn{}
	limiter.reset(oldConnection)

	results := make(chan bool, gatewaySendRateLimit+1)
	for i := 0; i <= gatewaySendRateLimit; i++ {
		go func() {
			results <- limiter.acquire(oldConnection, false)
		}()
	}

	select {
	case <-waitStarted:
	case <-time.After(time.Second):
		t.Fatal("rate limited write did not wait")
	}
	for i := 0; i < gatewaySendRateLimit; i++ {
		select {
		case acquired := <-results:
			if !acquired {
				t.Fatalf("acquire %d was canceled before reset", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("acquire %d did not complete", i)
		}
	}

	newConnection := &websocket.Conn{}
	limiter.reset(newConnection)
	select {
	case acquired := <-results:
		if acquired {
			t.Fatal("waiting write acquired a slot after its connection was reset")
		}
	case <-time.After(time.Second):
		t.Fatal("waiting write was not canceled by connection reset")
	}
	if !limiter.acquire(newConnection, false) {
		t.Fatal("first write on replacement connection was rate limited")
	}
}

func TestGatewayRateLimitWaitStopsOnClose(t *testing.T) {
	server := newCloseFrameTestServer(t)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	waitStarted := make(chan struct{})
	session := &Session{
		LogLevel:  -1,
		wsConn:    conn,
		listening: make(chan interface{}),
		sequence:  new(int64),
	}
	session.gatewaySendRateLimiter.wait = func(_ time.Duration, cancel <-chan struct{}) bool {
		close(waitStarted)
		<-cancel
		return false
	}
	for i := 0; i < gatewaySendRateLimit; i++ {
		if !session.gatewaySendRateLimiter.acquire(conn, false) {
			t.Fatalf("acquire %d was unexpectedly rate limited", i)
		}
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- session.GatewayWriteStruct(struct {
			Op int `json:"op"`
		}{Op: 3})
	}()

	select {
	case <-waitStarted:
	case <-time.After(time.Second):
		t.Fatal("rate limited write did not wait")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-writeDone:
		if !errors.Is(err, ErrWSNotFound) {
			t.Fatalf("GatewayWriteStruct() error = %v, want %v", err, ErrWSNotFound)
		}
	case <-time.After(time.Second):
		t.Fatal("rate limited write did not return after close")
	}
	if session.gatewaySendRateLimiter.connection != nil {
		t.Fatal("gateway send limiter retained the closed websocket")
	}
	if session.gatewaySendRateLimiter.acquire(conn, false) {
		t.Fatal("stale gateway write rearmed the stopped limiter")
	}

	replacement := &websocket.Conn{}
	session.gatewaySendRateLimiter.reset(replacement)
	if !session.gatewaySendRateLimiter.acquire(replacement, false) {
		t.Fatal("reset did not rearm the limiter for a replacement connection")
	}
}

func TestGatewayStartupAndHeartbeatBypassSendRateLimit(t *testing.T) {
	tests := []struct {
		name string
		op   int
		call func(*Session) error
	}{
		{
			name: "identify",
			op:   2,
			call: func(session *Session) error {
				session.Lock()
				err := session.writeGatewayStartupPacket(session.wsConn, identifyOp{Op: 2})
				session.Unlock()
				return err
			},
		},
		{
			name: "resume",
			op:   6,
			call: func(session *Session) error {
				session.Lock()
				err := session.writeGatewayStartupPacket(session.wsConn, resumePacket{Op: 6})
				session.Unlock()
				return err
			},
		},
		{
			name: "heartbeat",
			op:   1,
			call: func(session *Session) error {
				_, err := session.onEvent(websocket.TextMessage, []byte(`{"op":1,"d":null}`))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writes := captureGatewayWrites(t, 1, func(session *Session) error {
				session.gatewaySendRateLimiter.wait = func(time.Duration, <-chan struct{}) bool {
					t.Fatal("critical gateway write used the general send limit")
					return false
				}
				for i := 0; i < gatewaySendRateLimit; i++ {
					if !session.gatewaySendRateLimiter.acquire(session.wsConn, false) {
						t.Fatalf("acquire %d was unexpectedly rate limited", i)
					}
				}
				return tt.call(session)
			})
			if writes[0].Op != tt.op {
				t.Fatalf("op = %d, want %d", writes[0].Op, tt.op)
			}
		})
	}
}

type requestGuildMembersWrite struct {
	Op   int `json:"op"`
	Data struct {
		GuildID string   `json:"guild_id"`
		Query   *string  `json:"query,omitempty"`
		UserIDs []string `json:"user_ids,omitempty"`
		Limit   int      `json:"limit"`
		Nonce   string   `json:"nonce,omitempty"`
	} `json:"d"`
}

func captureRequestGuildMembersWrites(t *testing.T, count int, call func(*Session) error) []requestGuildMembersWrite {
	t.Helper()

	rawWrites := captureGatewayWrites(t, count, call)
	writes := make([]requestGuildMembersWrite, count)
	for i, rawWrite := range rawWrites {
		writes[i].Op = rawWrite.Op
		if err := json.Unmarshal(rawWrite.Data, &writes[i].Data); err != nil {
			t.Fatalf("message %d data = %s, unmarshal error: %v", i, rawWrite.Data, err)
		}
	}

	return writes
}

func captureGatewayWrites(t *testing.T, count int, call func(*Session) error) []gatewayWrite {
	t.Helper()

	messages := make(chan []byte, count)
	readErr := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			readErr <- err
			return
		}
		defer conn.Close()

		for i := 0; i < count; i++ {
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				readErr <- err
				return
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			messages <- message
		}
		readErr <- nil
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	session := &Session{wsConn: conn, sequence: new(int64)}
	if err := call(session); err != nil {
		t.Fatalf("call returned error: %v", err)
	}

	if err := <-readErr; err != nil {
		t.Fatalf("ReadMessage returned error: %v", err)
	}

	writes := make([]gatewayWrite, count)
	for i := 0; i < count; i++ {
		message := <-messages
		if err := json.Unmarshal(message, &writes[i]); err != nil {
			t.Fatalf("message %d = %s, unmarshal error: %v", i, message, err)
		}
	}

	return writes
}

func TestHeartbeatLatencyConcurrentHeartbeat(t *testing.T) {
	heartbeatRead := make(chan struct{}, 1)

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
			select {
			case heartbeatRead <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	listening := make(chan interface{})
	t.Cleanup(func() {
		close(listening)
	})

	session := &Session{
		lastHeartbeatAck: time.Now().Add(time.Hour),
		wsConn:           conn,
		sequence:         new(int64),
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = session.HeartbeatLatency()
			}
		}
	}()

	go session.heartbeat(conn, listening, 1)

	select {
	case <-heartbeatRead:
	case <-time.After(time.Second):
		t.Fatal("heartbeat was not written")
	}

	close(stop)
	<-done
}

func TestHeartbeatWaitsForInitialJitter(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration {
		return 50 * time.Millisecond
	})
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	heartbeatRead := make(chan struct{}, 1)

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
			select {
			case heartbeatRead <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	listening := make(chan interface{})
	t.Cleanup(func() {
		close(listening)
	})

	session := &Session{
		lastHeartbeatAck: time.Now().Add(time.Hour),
		wsConn:           conn,
		sequence:         new(int64),
	}

	go session.heartbeat(conn, listening, 1000)

	select {
	case <-heartbeatRead:
		t.Fatal("heartbeat was written before initial jitter")
	case <-time.After(10 * time.Millisecond):
	}

	select {
	case <-heartbeatRead:
	case <-time.After(time.Second):
		t.Fatal("heartbeat was not written after initial jitter")
	}
}

func TestHeartbeatReconnectsAfterOneMissedAck(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	conn, peer := newGatewayTestConnection(t)
	closeCode := make(chan int, 1)
	heartbeats := make(chan int, 16)
	peer.SetCloseHandler(func(code int, text string) error {
		closeCode <- code
		return nil
	})
	go func() {
		for count := 1; ; count++ {
			if _, _, err := peer.ReadMessage(); err != nil {
				return
			}
			select {
			case heartbeats <- count:
			default:
			}
		}
	}()

	listening := make(chan interface{})
	session := &Session{
		ShouldReconnectOnError: false,
		sequence:               new(int64),
		wsConn:                 conn,
		listening:              listening,
	}
	session.LastHeartbeatAck = time.Now().UTC()

	done := make(chan struct{})
	go func() {
		session.heartbeat(conn, listening, 20)
		close(done)
	}()

	select {
	case count := <-heartbeats:
		if count != 1 {
			t.Fatalf("first heartbeat count = %d, want 1", count)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive first heartbeat")
	}

	select {
	case code := <-closeCode:
		if code != websocket.CloseServiceRestart {
			t.Fatalf("close code = %d, want %d", code, websocket.CloseServiceRestart)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive heartbeat close frame")
	}
	select {
	case count := <-heartbeats:
		t.Fatalf("received heartbeat %d before reconnect, want exactly one", count)
	default:
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not return")
	}
}

func TestHeartbeatContinuesAfterAck(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	conn, peer := newGatewayTestConnection(t)
	listening := make(chan interface{})
	session := &Session{
		ShouldReconnectOnError: false,
		sequence:               new(int64),
		wsConn:                 conn,
		listening:              listening,
		LastHeartbeatAck:       time.Now().UTC(),
	}

	serverErr := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			if _, _, err := peer.ReadMessage(); err != nil {
				serverErr <- err
				return
			}
			if err := peer.WriteJSON(map[string]interface{}{"op": 11, "d": nil}); err != nil {
				serverErr <- err
				return
			}
		}
		serverErr <- nil
	}()

	ackErr := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				ackErr <- err
				return
			}
			if _, err = session.onEvent(messageType, message); err != nil {
				ackErr <- err
				return
			}
		}
		ackErr <- nil
	}()

	done := make(chan struct{})
	go func() {
		session.heartbeat(conn, listening, 50)
		close(done)
	}()

	if err := <-serverErr; err != nil {
		t.Fatalf("gateway peer returned error: %v", err)
	}
	if err := <-ackErr; err != nil {
		t.Fatalf("heartbeat ACK reader returned error: %v", err)
	}
	close(listening)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after listening closed")
	}

	session.RLock()
	lastAck := session.LastHeartbeatAck
	lastSent := session.LastHeartbeatSent
	currentConn := session.wsConn
	session.RUnlock()
	if lastAck.Before(lastSent) {
		t.Fatalf("last heartbeat ACK %v is before sent heartbeat %v", lastAck, lastSent)
	}
	if currentConn != conn {
		t.Fatal("acknowledged heartbeat closed the gateway connection")
	}
}

// shiftHeartbeatWallTime moves only the wall reading of a recorded timestamp.
// Go has no public wall-only adjustment API: preserve its monotonic ext field
// in this test helper. Fail explicitly if time.Time's representation changes.
func shiftHeartbeatWallTime(t *testing.T, stamp time.Time, delta time.Duration) time.Time {
	t.Helper()
	if stamp.IsZero() {
		return stamp
	}
	shifted := stamp.Add(delta)
	if stamp != stamp.Round(0) {
		ext := reflect.ValueOf(&shifted).Elem().FieldByName("ext")
		if !ext.IsValid() || ext.Kind() != reflect.Int64 {
			t.Fatal("time.Time monotonic representation changed")
		}
		*(*int64)(unsafe.Pointer(ext.UnsafeAddr())) = reflect.ValueOf(stamp).FieldByName("ext").Int()
		if shifted.Sub(stamp) != 0 {
			t.Fatal("wall adjustment changed monotonic elapsed time")
		}
	}
	if shifted.Round(0).Sub(stamp.Round(0)) != delta {
		t.Fatal("wall adjustment did not apply")
	}
	return shifted
}

// Rebasing earlier timestamps by -delta is equivalent to moving the clock by
// delta before the next time.Now(), without changing the machine's clock.
func adjustHeartbeatClock(t *testing.T, s *Session, delta time.Duration) {
	t.Helper()
	s.Lock()
	defer s.Unlock()
	for _, stamp := range []*time.Time{&s.LastHeartbeatSent, &s.LastHeartbeatAck, &s.lastHeartbeatSent, &s.lastHeartbeatAck} {
		*stamp = shiftHeartbeatWallTime(t, *stamp, -delta)
	}
}

func TestHeartbeatClockAdjustment(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	for _, tt := range []struct {
		name        string
		adjustment  time.Duration
		requested   bool
		withholdAck bool
	}{
		{name: "normal"},
		{name: "backwards", adjustment: -time.Second},
		{name: "forwards", adjustment: time.Second},
		{name: "backwards_missing_ack", adjustment: -time.Second, withholdAck: true},
		{name: "forwards_missing_ack", adjustment: time.Second, withholdAck: true},
		{name: "backwards_requested", adjustment: -time.Second, requested: true},
		{name: "forwards_requested", adjustment: time.Second, requested: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn, peer := newGatewayTestConnection(t)
			s, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			s.ShouldReconnectOnError = false
			listening := make(chan interface{})
			s.wsConn, s.listening = conn, listening
			done := make(chan struct{})
			go func() {
				defer close(done)
				s.heartbeat(conn, listening, 200)
			}()
			t.Cleanup(func() {
				s.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("heartbeat did not stop")
				}
			})
			readHeartbeat := func() error {
				if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					return err
				}
				var packet heartbeatOp
				if err := peer.ReadJSON(&packet); err != nil {
					return err
				}
				if packet.Op != 1 {
					return fmt.Errorf("got opcode %d, want heartbeat", packet.Op)
				}
				return nil
			}
			if err := readHeartbeat(); err != nil {
				t.Fatal(err)
			}
			adjustHeartbeatClock(t, s, tt.adjustment)
			if tt.requested {
				s.RLock()
				previous := s.LastHeartbeatSent
				s.RUnlock()
				if _, err := s.onEvent(websocket.TextMessage, []byte(`{"op":1,"d":null}`)); err != nil {
					t.Fatal(err)
				}
				if err := readHeartbeat(); err != nil {
					t.Fatal(err)
				}
				s.RLock()
				updated := s.LastHeartbeatSent
				s.RUnlock()
				if updated.Equal(previous) {
					t.Error("opcode 1 rejected the newer heartbeat timestamp")
				}
				adjustHeartbeatClock(t, s, tt.adjustment)
			}
			if !tt.withholdAck {
				if err := peer.WriteMessage(websocket.TextMessage, []byte(`{"op":11,"d":null}`)); err != nil {
					t.Fatal(err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				mt, message, err := conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.onEvent(mt, message); err != nil {
					t.Fatal(err)
				}
				if latency := s.HeartbeatLatency(); latency < 0 || latency >= 200*time.Millisecond {
					t.Errorf("latency = %v, want elapsed time unaffected by clock adjustment", latency)
				}
				s.RLock()
				utc := s.LastHeartbeatAck.Location() == time.UTC && s.LastHeartbeatSent.Location() == time.UTC
				s.RUnlock()
				if !utc {
					t.Error("public heartbeat timestamps must remain UTC")
				}
			}
			// This is the next actual heartbeat decision, after opcode 11 ran.
			err = readHeartbeat()
			if tt.withholdAck {
				if !websocket.IsCloseError(err, websocket.CloseServiceRestart) {
					t.Fatalf("missing ACK: got %v, want service-restart close at next interval", err)
				}
			} else if err != nil {
				t.Fatalf("received ACK but next heartbeat failed: %v", err)
			}
		})
	}
}

func TestMissedHeartbeatAckDoesNotCloseReplacementConnection(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	oldConn, _ := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	oldListening := make(chan interface{})
	newListening := make(chan interface{})

	oldLogger := Logger
	defer func() { Logger = oldLogger }()
	logReached := make(chan struct{})
	releaseLog := make(chan struct{})
	defer closeChannel(releaseLog)
	var blockLog sync.Once
	var elapsed time.Duration
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		if strings.Contains(fmt.Sprintf(format, a...), "haven't gotten a heartbeat ACK") {
			blockLog.Do(func() {
				elapsed = a[0].(time.Duration)
				close(logReached)
				<-releaseLog
			})
		}
	}

	session := &Session{
		lastHeartbeatAck:       time.Now().Add(-time.Second),
		lastHeartbeatSent:      time.Now(),
		LogLevel:               LogError,
		ShouldReconnectOnError: false,
		SyncEvents:             true,
		sequence:               new(int64),
		wsConn:                 oldConn,
		listening:              oldListening,
	}

	adjustHeartbeatClock(t, session, -time.Hour)

	disconnected := int32(0)
	session.AddHandler(func(*Session, *Disconnect) {
		atomic.StoreInt32(&disconnected, 1)
	})

	done := make(chan struct{})
	go func() {
		session.heartbeat(oldConn, oldListening, 20)
		close(done)
	}()

	select {
	case <-logReached:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not detect the missed ACK")
	}

	if elapsed < 0 || elapsed >= time.Second {
		t.Errorf("missed ACK diagnostic elapsed = %v, want monotonic elapsed time", elapsed)
	}

	session.Lock()
	session.DataReady = true
	session.wsConn = newConn
	session.listening = newListening
	session.Unlock()
	close(releaseLog)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not return")
	}

	session.RLock()
	currentConn := session.wsConn
	currentListening := session.listening
	dataReady := session.DataReady
	session.RUnlock()
	if currentConn != newConn || currentListening != newListening || !dataReady {
		t.Fatal("stale heartbeat changed the replacement gateway state")
	}
	select {
	case <-newListening:
		t.Fatal("stale heartbeat closed the replacement listening channel")
	default:
	}
	if atomic.LoadInt32(&disconnected) != 0 {
		t.Fatal("stale heartbeat emitted a disconnect for the replacement connection")
	}
	if err := newConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("replacement connection WriteControl returned error: %v", err)
	}

	close(oldListening)
	close(newListening)
}

func TestReconnectCanceledAfterOpenKeepsNewerSession(t *testing.T) {
	// A user Close()+Open() that lands while an automatic reconnect has
	// an Open in flight must win: the reconnect may not tear down the
	// session the user just opened. The synchronous Connect handler
	// runs inside the reconnect-driven Open's unlocked window, which
	// reproduces the interleaving deterministically.
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		if err := c.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
			return
		}
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
		ready := []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`)
		if err := c.WriteMessage(websocket.TextMessage, ready); err != nil {
			return
		}
		// Keep the connection alive until the client closes it.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.SyncEvents = true

	var handled int32
	session.AddHandler(func(s *Session, _ *Connect) {
		if atomic.AddInt32(&handled, 1) != 1 {
			return
		}
		if err := s.Close(); err != nil {
			t.Errorf("Close returned error: %v", err)
		}
		if err := s.Open(); err != nil {
			t.Errorf("Open returned error: %v", err)
		}
	})

	done := make(chan struct{})
	go func() {
		session.reconnect(session.reconnectCancelSignal())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not return")
	}

	session.RLock()
	open := session.wsConn != nil
	session.RUnlock()
	if !open {
		t.Fatal("reconnect closed the session opened by the concurrent Open")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("final Close returned error: %v", err)
	}
}

func TestReconnectStopsWhenCloseCalled(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	attempted := make(chan struct{}, 1)
	var attempts int32
	session.gateway = "ws://discord.invalid/gateway"
	session.Dialer = &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			atomic.AddInt32(&attempts, 1)
			select {
			case attempted <- struct{}{}:
			default:
			}
			return nil, errors.New("dial failed")
		},
	}

	done := make(chan struct{})
	go func() {
		session.reconnect(session.reconnectCancelSignal())
		close(done)
	}()

	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not attempt to dial")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reconnect did not stop after Close")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("dial attempts = %d, want 1", got)
	}
}

func TestReconnectDoesNotStartAfterClose(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	attempted := make(chan struct{}, 1)
	session.gateway = "ws://discord.invalid/gateway"
	session.Dialer = &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			select {
			case attempted <- struct{}{}:
			default:
			}
			return nil, errors.New("dial failed")
		},
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		session.reconnect(session.reconnectCancelSignal())
		close(done)
	}()

	select {
	case <-done:
	case <-attempted:
		_ = session.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("reconnect did not stop after second Close")
		}
		t.Fatal("reconnect dialed after Close")
	case <-time.After(200 * time.Millisecond):
		_ = session.Close()
		t.Fatal("reconnect did not return after Close")
	}
}

func TestOpenAfterCloseCanDial(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	var attempts int32
	session.gateway = "ws://discord.invalid/gateway"
	session.Dialer = &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			atomic.AddInt32(&attempts, 1)
			return nil, errors.New("dial failed")
		},
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if err := session.Open(); err == nil {
		t.Fatal("Open returned nil error, want dial failure")
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("dial attempts = %d, want 1", got)
	}
}

func TestOpenGatewayLookupDoesNotBlockClose(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	lookupStarted := make(chan struct{})
	releaseLookup := make(chan struct{})
	session.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		close(lookupStarted)
		<-releaseLookup
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"url":"ws://discord.invalid/gateway"}`)),
			Request:    req,
		}, nil
	})}

	var dialAttempts int32
	session.Dialer = &websocket.Dialer{NetDial: func(string, string) (net.Conn, error) {
		atomic.AddInt32(&dialAttempts, 1)
		return nil, errors.New("unexpected dial")
	}}

	openDone := make(chan error, 1)
	go func() {
		openDone <- session.Open()
	}()

	select {
	case <-lookupStarted:
	case <-time.After(time.Second):
		t.Fatal("Open did not start the gateway lookup")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close()
	}()

	select {
	case err = <-closeDone:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(releaseLookup)
		<-closeDone
		<-openDone
		t.Fatal("Close blocked behind a stalled gateway lookup")
	}

	close(releaseLookup)
	select {
	case err = <-openDone:
		if !errors.Is(err, ErrWSNotFound) {
			t.Fatalf("Open returned %v after Close, want %v", err, ErrWSNotFound)
		}
	case <-time.After(time.Second):
		t.Fatal("Open did not return after the gateway lookup completed")
	}
	if got := atomic.LoadInt32(&dialAttempts); got != 0 {
		t.Fatalf("gateway dial attempts = %d, want 0", got)
	}
}

func TestOpenDialDoesNotBlockClose(t *testing.T) {
	ready := []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`)
	server := newGatewayOpenTestServer(t, ready)
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{}, 1)
	defer func() {
		select {
		case releaseDial <- struct{}{}:
		default:
		}
	}()
	session.gateway = "ws" + strings.TrimPrefix(server.URL, "http")
	session.Dialer = &websocket.Dialer{NetDial: func(network, address string) (net.Conn, error) {
		close(dialStarted)
		<-releaseDial
		return net.Dial(network, address)
	}}

	openDone := make(chan error, 1)
	go func() {
		openDone <- session.Open()
	}()

	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("Open did not start dialing")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close()
	}()

	select {
	case err = <-closeDone:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close blocked behind a stalled gateway dial")
	}

	releaseDial <- struct{}{}
	select {
	case err = <-openDone:
		if err == nil {
			t.Fatal("Open returned nil after Close canceled its stalled dial")
		}
	case <-time.After(time.Second):
		t.Fatal("Open did not return after its canceled dial completed")
	}
}

func TestOpenHelloDoesNotBlockClose(t *testing.T) {
	connected := make(chan struct{})
	unblockServer := make(chan struct{})
	defer close(unblockServer)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		close(connected)
		<-unblockServer
	}))
	defer server.Close()

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.gateway = "ws" + strings.TrimPrefix(server.URL, "http")

	openDone := make(chan error, 1)
	go func() {
		openDone <- session.Open()
	}()

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("Open did not connect to gateway")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close()
	}()

	select {
	case err = <-closeDone:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close blocked behind a stalled gateway Hello")
	}

	select {
	case err = <-openDone:
		if err == nil {
			t.Fatal("Open returned nil after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Open did not return after Close")
	}
}

func newGatewayOpenTestServer(t *testing.T, startupPackets ...[]byte) *httptest.Server {
	return newGatewayOpenTestServerWithHello(t, []byte(`{"op":10,"d":{"heartbeat_interval":1000}}`), startupPackets...)
}

func newGatewayOpenTestServerWithHello(t *testing.T, hello []byte, startupPackets ...[]byte) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for _, startupPacket := range startupPackets {
			if err := conn.WriteMessage(websocket.TextMessage, startupPacket); err != nil {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}))

	t.Cleanup(server.Close)
	return server
}

func newGatewayTricklingStartupTestServer(t *testing.T, gap time.Duration, trickleCount int) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for i := 0; i < trickleCount; i++ {
			time.Sleep(gap)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":11,"d":null}`)); err != nil {
				return
			}
		}
		time.Sleep(gap)
		ready := []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`)
		if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}))

	t.Cleanup(server.Close)
	return server
}

func TestOpenSurvivesSlowStartupReplayWithinPerReadDeadline(t *testing.T) {
	// Total startup traffic takes ~500ms, exceeding the 200ms startup
	// read timeout, but every individual gap stays well below it. The
	// deadline must bound time-without-data, not the whole replay.
	server := newGatewayTricklingStartupTestServer(t, 100*time.Millisecond, 4)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("error creating session: %v", err)
	}
	session.Dialer = &websocket.Dialer{HandshakeTimeout: 200 * time.Millisecond}

	if err = openWithTimeout(t, session); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	session.Close()
}

func newGatewayOpenAfterHeartbeatTestServer(t *testing.T, heartbeatRead chan<- struct{}) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":10}}`)); err != nil {
			return
		}

		for i := 0; i < 2; i++ {
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				return
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var event Event
			if err := json.Unmarshal(message, &event); err != nil {
				return
			}
			if event.Operation == 1 {
				select {
				case heartbeatRead <- struct{}{}:
				default:
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":11,"d":null}`)); err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":"wss://resume.gateway","user":{"id":"user"},"guilds":[]}}`)); err != nil {
					return
				}
				time.Sleep(50 * time.Millisecond)
				return
			}
		}
	}))

	t.Cleanup(server.Close)
	return server
}

func newGatewayCloseAfterIdentifyTestServer(t *testing.T, closeCode int, attempts *int32) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(attempts, 1)

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
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, ""), time.Now().Add(time.Second))
	}))

	t.Cleanup(server.Close)
	return server
}

func newGatewayStalledOpenTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	done := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		<-done
	}))

	t.Cleanup(func() {
		close(done)
		server.Close()
	})
	return server
}

func newGatewayStalledReadyTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	done := make(chan struct{})
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

		<-done
	}))

	t.Cleanup(func() {
		close(done)
		server.Close()
	})
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

func TestOpenReturnsWhenGatewayHelloStalls(t *testing.T) {
	server := newGatewayStalledOpenTestServer(t)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Dialer = &websocket.Dialer{HandshakeTimeout: 25 * time.Millisecond}

	start := time.Now()
	err = openWithTimeout(t, session)
	if err == nil {
		t.Fatal("Open returned nil error, want startup read timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Open returned after %v, want bounded startup timeout", elapsed)
	}
	if session.wsConn != nil {
		t.Fatal("Open left wsConn set after startup timeout")
	}
}

func TestOpenReturnsWhenGatewayReadyStalls(t *testing.T) {
	server := newGatewayStalledReadyTestServer(t)
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Dialer = &websocket.Dialer{HandshakeTimeout: 25 * time.Millisecond}

	start := time.Now()
	err = openWithTimeout(t, session)
	if err == nil {
		t.Fatal("Open returned nil error, want startup read timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Open returned after %v, want bounded startup timeout", elapsed)
	}
	if session.wsConn != nil {
		t.Fatal("Open left wsConn set after gateway ready timeout")
	}
}

func TestShouldReconnectOnGatewayClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "authentication failed",
			err:  &websocket.CloseError{Code: 4004},
			want: false,
		},
		{
			name: "invalid shard",
			err:  &websocket.CloseError{Code: 4010},
			want: false,
		},
		{
			name: "sharding required",
			err:  &websocket.CloseError{Code: 4011},
			want: false,
		},
		{
			name: "invalid api version",
			err:  &websocket.CloseError{Code: 4012},
			want: false,
		},
		{
			name: "invalid intents",
			err:  &websocket.CloseError{Code: 4013},
			want: false,
		},
		{
			name: "disallowed intents",
			err:  &websocket.CloseError{Code: 4014},
			want: false,
		},
		{
			name: "session timed out",
			err:  &websocket.CloseError{Code: 4009},
			want: true,
		},
		{
			name: "network error",
			err:  websocket.ErrCloseSent,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldReconnectOnGatewayClose(tt.err); got != tt.want {
				t.Fatalf("shouldReconnectOnGatewayClose() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldStartNewGatewaySessionOnClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "invalid sequence",
			err:  &websocket.CloseError{Code: 4007},
			want: true,
		},
		{
			name: "session timed out",
			err:  &websocket.CloseError{Code: 4009},
			want: true,
		},
		{
			name: "not authenticated",
			err:  &websocket.CloseError{Code: 4003},
			want: true,
		},
		{
			name: "already authenticated",
			err:  &websocket.CloseError{Code: 4005},
			want: true,
		},
		{
			name: "reconnect",
			err:  &websocket.CloseError{Code: 4000},
			want: false,
		},
		{
			name: "authentication failed",
			err:  &websocket.CloseError{Code: 4004},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldStartNewGatewaySessionOnClose(tt.err); got != tt.want {
				t.Fatalf("shouldStartNewGatewaySessionOnClose() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedactedGatewayData(t *testing.T) {
	data := []byte(`{"id":"interaction","token":"interaction-token","nested":{"access_token":"access-token","refresh_token":"refresh-token","safe":"ok"},"items":[{"token":"voice-token"}]}`)

	got := redactedGatewayData(data)
	for _, secret := range []string{"interaction-token", "access-token", "refresh-token", "voice-token"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedGatewayData() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedGatewayData() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedGatewayData() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedGatewayDataInvalidJSON(t *testing.T) {
	data := []byte("not-json")

	if got := redactedGatewayData(data); got != string(data) {
		t.Fatalf("redactedGatewayData() = %q, want %q", got, string(data))
	}
}

func newCloseFrameTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func newCloseCodeTestServer(t *testing.T, closeCodes chan<- int) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) {
					closeCodes <- closeErr.Code
				}
				return
			}
		}
	}))
}

func requireCloseDoneWhileWSMutexLocked(t *testing.T, unlock func(), done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		unlock()
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("Close did not return while wsMutex was locked")
	}
}

func waitForWSConn(t *testing.T, session *Session) {
	t.Helper()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(time.Second)
	for {
		session.RLock()
		ready := session.wsConn != nil
		session.RUnlock()
		if ready {
			return
		}

		select {
		case <-ticker.C:
		case <-timeout:
			t.Fatal("session websocket was not opened")
		}
	}
}

func compressedGatewayPayload(t testing.TB, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestGatewayCompressedDispatchReuseAfterError(t *testing.T) {
	message := compressedGatewayPayload(t, []byte(`{"op":11,"s":42,"d":null}`))
	session, err := New("Bot token")
	if err != nil {
		t.Fatal(err)
	}
	session.StateEnabled = false

	if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
		t.Fatalf("initial valid payload failed: %v", err)
	}
	if _, err = session.onEvent(websocket.BinaryMessage, []byte("invalid")); err == nil {
		t.Fatal("invalid compressed payload returned nil error")
	}
	if _, err = session.onEvent(websocket.BinaryMessage, message[:len(message)/2]); err == nil {
		t.Fatal("truncated compressed payload returned nil error")
	}
	for i := 0; i < 100; i++ {
		if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
			t.Fatalf("valid payload %d failed after invalid payloads: %v", i, err)
		}
	}
}

func TestPeekGatewayCompressedEvent(t *testing.T) {
	message := compressedGatewayPayload(t, []byte(`{"op":10,"s":42,"t":"READY","d":{}}`))
	event, err := peekGatewayEvent(websocket.BinaryMessage, message)
	if err != nil {
		t.Fatalf("peekGatewayEvent() error = %v", err)
	}
	if event == nil || event.Operation != 10 || event.Type != "READY" {
		t.Fatalf("peekGatewayEvent() = %#v", event)
	}
	if _, err = peekGatewayEvent(websocket.BinaryMessage, message[:len(message)/2]); err == nil {
		t.Fatal("peekGatewayEvent() accepted a truncated payload")
	}
}

func TestGatewayCompressedDispatchConcurrentReuse(t *testing.T) {
	message := compressedGatewayPayload(t, []byte(`{"op":11,"s":42,"d":null}`))
	const goroutines = 32
	const iterations = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := New("Bot token")
			if err != nil {
				errs <- err
				return
			}
			session.StateEnabled = false
			for j := 0; j < iterations; j++ {
				if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func BenchmarkGatewayCompressedDispatch(b *testing.B) {
	payload := []byte(`{"op":0,"s":42,"t":"MESSAGE_CREATE","d":{"id":"100","channel_id":"200","guild_id":"300","author":{"id":"400","username":"bot"},"content":"hello","timestamp":"2026-07-21T00:00:00Z"}}`)
	session, err := New("Bot token")
	if err != nil {
		b.Fatal(err)
	}
	session.StateEnabled = false
	session.SyncEvents = true
	message := compressedGatewayPayload(b, payload)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err = session.onEvent(websocket.BinaryMessage, message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGatewayTextDispatch(b *testing.B) {
	message := []byte(`{"op":0,"s":42,"t":"MESSAGE_CREATE","d":{"id":"100","channel_id":"200","guild_id":"300","author":{"id":"400","username":"bot"},"content":"hello","timestamp":"2026-07-21T00:00:00Z"}}`)
	session, err := New("Bot token")
	if err != nil {
		b.Fatal(err)
	}
	session.StateEnabled = false
	session.SyncEvents = true

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err = session.onEvent(websocket.TextMessage, message); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGatewayErrorLoggingConcurrentEndpointUpdate(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)
	for _, path := range []string{"heartbeat", "listener"} {
		t.Run(path, func(t *testing.T) {
			conn, _ := newGatewayTestConnection(t)
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			session := &Session{LogLevel: -1, sequence: new(int64)}
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					// Open refreshes or clears the cached endpoint under this lock.
					session.Lock()
					session.gateway = "wss://replacement.gateway.example"
					session.Unlock()
					runtime.Gosched()
				}
			}()
			for i := 0; i < 100; i++ {
				listening := make(chan interface{})
				session.Lock()
				session.wsConn = conn
				session.listening = listening
				session.lastHeartbeatAck = time.Now().Add(time.Hour)
				session.Unlock()
				if path == "heartbeat" {
					session.heartbeat(conn, listening, 1)
				} else {
					session.listen(conn, listening)
				}
			}
			close(stop)
			<-done
		})
	}
}

// Hold a successful heartbeat write immediately before it returns to the
// caller, so Close can acquire the session lock in that window.
type pausedHeartbeatConn struct {
	net.Conn
	written chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *pausedHeartbeatConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && len(p) > 0 && p[0] == 0x81 {
		c.once.Do(func() {
			close(c.written)
			<-c.release
		})
	}
	return n, err
}

func TestHeartbeatDoesNotMarkClosedSessionReady(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)

	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		peer, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		peers <- peer
	}))
	defer server.Close()
	transport := &pausedHeartbeatConn{written: make(chan struct{}), release: make(chan struct{})}
	defer closeChannel(transport.release)
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		transport.Conn = conn
		return transport, err
	}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	peer := <-peers
	defer func() { _ = peer.Close() }()
	listening := make(chan interface{})
	session := &Session{
		wsConn: conn, listening: listening, sequence: new(int64),
		lastHeartbeatAck: time.Now().Add(time.Hour), LogLevel: -1,
	}
	heartbeatDone := make(chan struct{})
	go func() {
		session.heartbeat(conn, listening, 60000)
		close(heartbeatDone)
	}()
	select {
	case <-transport.written:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not finish its network write")
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case <-listening:
	case <-time.After(time.Second):
		t.Fatal("Close did not acquire the session lock")
	}
	close(transport.release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	select {
	case <-heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after Close")
	}
	session.RLock()
	ready, currentConn := session.DataReady, session.wsConn
	session.RUnlock()
	if currentConn != nil {
		t.Fatal("Close left the gateway connection installed")
	}
	if ready {
		t.Fatal("successful stale heartbeat marked a closed session ready")
	}
}

func TestStatusHelpersSendIdleStatus(t *testing.T) {
	helpers := []struct {
		name string
		call func(*Session, int) error
		kind ActivityType
		url  string
	}{
		{"game", func(s *Session, idle int) error { return s.UpdateGameStatus(idle, "activity") }, ActivityTypeGame, ""},
		{"watch", func(s *Session, idle int) error { return s.UpdateWatchStatus(idle, "activity") }, ActivityTypeWatching, ""},
		{"stream", func(s *Session, idle int) error {
			return s.UpdateStreamingStatus(idle, "activity", "https://www.twitch.tv/example")
		}, ActivityTypeStreaming, "https://www.twitch.tv/example"},
	}
	for _, helper := range helpers {
		for _, test := range []struct {
			idle   int
			status string
		}{
			{91879201, "idle"},
			{0, "online"},
			{-1, "online"},
		} {
			t.Run(fmt.Sprintf("%s/idle=%d", helper.name, test.idle), func(t *testing.T) {
				writes := captureGatewayWrites(t, 1, func(s *Session) error {
					return helper.call(s, test.idle)
				})
				if writes[0].Op != 3 {
					t.Fatalf("opcode = %d, want 3", writes[0].Op)
				}
				var data struct {
					Status     string `json:"status"`
					IdleSince  *int   `json:"since"`
					Activities []*struct {
						Name string       `json:"name"`
						Type ActivityType `json:"type"`
						URL  string       `json:"url"`
					} `json:"activities"`
				}
				if err := json.Unmarshal(writes[0].Data, &data); err != nil {
					t.Fatal(err)
				}
				if data.Status != test.status {
					t.Errorf("status = %q, want %q", data.Status, test.status)
				}
				if test.idle > 0 {
					if data.IdleSince == nil || *data.IdleSince != test.idle {
						t.Errorf("idle timestamp was not preserved: %s", writes[0].Data)
					}
				} else if data.IdleSince != nil {
					t.Errorf("non-idle status has timestamp %d", *data.IdleSince)
				}
				if len(data.Activities) != 1 || data.Activities[0] == nil {
					t.Fatalf("activities = %#v, want one activity", data.Activities)
				}
				activity := data.Activities[0]
				if activity.Name != "activity" || activity.Type != helper.kind || activity.URL != helper.url {
					t.Errorf("activity changed: %+v", activity)
				}
			})
		}
	}
}

func TestGatewayOpenCanceledDuringNetworkSetup(t *testing.T) {
	for _, stage := range []string{"lookup", "dial", "TLS", "custom TLS", "HTTP"} {
		t.Run(stage, func(t *testing.T) {
			session, err := New("Bot dummy-token")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.gateway = "wss://gateway.invalid/"
			session.Dialer = &websocket.Dialer{}
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			canceled := func(ctx context.Context) error {
				close(entered)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
					return errors.New("test released stalled operation")
				}
			}
			switch stage {
			case "lookup":
				session.gateway = ""
				session.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					return nil, canceled(req.Context())
				})}
			case "dial":
				session.Dialer.NetDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
					return nil, canceled(ctx)
				}
			case "TLS":
				client, peer := net.Pipe()
				defer func() { _ = client.Close() }()
				defer func() { _ = peer.Close() }()
				session.Dialer.NetDialContext = func(context.Context, string, string) (net.Conn, error) {
					close(entered)
					return client, nil
				}
			case "custom TLS":
				client, peer := net.Pipe()
				defer func() { _ = client.Close() }()
				defer func() { _ = peer.Close() }()
				session.Dialer.NetDialTLSContext = func(context.Context, string, string) (net.Conn, error) {
					close(entered)
					return client, nil
				}
			case "HTTP":
				server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
					_ = canceled(req.Context())
				}))
				// CloseClientConnections also makes failure-path cleanup bounded.
				defer server.Close()
				defer server.CloseClientConnections()
				session.gateway = "ws" + strings.TrimPrefix(server.URL, "http")
			}
			done := make(chan error, 1)
			go func() { done <- session.Open() }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("Open did not reach network setup")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled Open succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("Close left Open blocked in " + stage)
			}
			if session.wsConn != nil {
				t.Fatal("canceled Open installed a socket")
			}
		})
	}
}

func TestStatusHelpersSharePresenceRateLimit(t *testing.T) {
	for _, staggered := range []bool{false, true} {
		t.Run(fmt.Sprintf("staggered=%t", staggered), func(t *testing.T) {
			testStatusHelpersSharePresenceRateLimit(t, staggered)
		})
	}
}

func testStatusHelpersSharePresenceRateLimit(t *testing.T, staggered bool) {
	t.Helper()
	now := time.Unix(1, 0)
	var waits []time.Duration
	completed := 0
	writes := captureGatewayWrites(t, 8, func(session *Session) error {
		session.gatewaySendRateLimiter.now = func() time.Time { return now }
		session.gatewaySendRateLimiter.wait = func(delay time.Duration, _ <-chan struct{}) bool {
			if completed < 5 || completed > 6 {
				t.Errorf("unexpected presence wait after %d status writes", completed)
			}
			waits = append(waits, delay)
			now = now.Add(delay)
			return true
		}
		updates := []func() error{
			func() error { return session.UpdateGameStatus(0, "game") },
			func() error { return session.UpdateWatchStatus(0, "watch") },
			func() error { return session.UpdateStreamingStatus(0, "stream", "https://www.twitch.tv/example") },
			func() error { return session.UpdateListeningStatus("listen") },
			func() error { return session.UpdateCustomStatus("custom") },
			func() error { return session.UpdateStatusComplex(UpdateStatusData{Status: "online"}) },
			func() error { return session.UpdateGameStatus(0, "next") },
		}
		for i, update := range updates {
			if staggered && i == 1 {
				now = now.Add(19 * time.Second)
			}
			if i == 5 {
				// An exhausted presence budget must not delay other gateway events.
				if err := session.RequestGuildMembers("guild", "", 1, "nonce", false); err != nil {
					return err
				}
				if len(waits) != 0 {
					t.Error("presence budget delayed a guild member request")
				}
			}
			if err := update(); err != nil {
				return err
			}
			completed++
		}
		return nil
	})
	wantWaits := []time.Duration{20 * time.Second}
	if staggered {
		// Only the first update expires at t=20; the other four expire at t=39.
		wantWaits = []time.Duration{time.Second, 19 * time.Second}
	}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Errorf("status rate limit waits = %v, want %v", waits, wantWaits)
	}
	for i, write := range writes {
		want := 3
		if i == 5 {
			want = 8
		}
		if write.Op != want {
			t.Errorf("write %d opcode = %d, want %d", i, write.Op, want)
		}
	}
}

func TestPresenceRateLimitWaitStopsOnClose(t *testing.T) {
	conn, peer := newGatewayTestConnection(t)
	session := &Session{LogLevel: -1, wsConn: conn, sequence: new(int64)}
	waiting := make(chan struct{})
	session.gatewaySendRateLimiter.wait = func(_ time.Duration, cancel <-chan struct{}) bool {
		close(waiting)
		<-cancel
		return false
	}
	for i := 0; i < 5; i++ {
		if err := session.UpdateGameStatus(0, "game"); err != nil {
			t.Fatal(err)
		}
		if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		var write gatewayWrite
		if err := peer.ReadJSON(&write); err != nil {
			t.Fatal(err)
		}
		if write.Op != 3 {
			t.Fatalf("opcode = %d, want presence", write.Op)
		}
	}
	done := make(chan struct{})
	var writeErr error
	go func() {
		defer close(done)
		writeErr = session.UpdateCustomStatus("sixth")
	}()
	defer func() {
		_ = session.Close()
		<-done
	}()
	select {
	case <-waiting:
	case <-done:
		t.Fatalf("sixth status update returned without waiting: %v", writeErr)
	case <-time.After(time.Second):
		t.Fatal("sixth status update did not reach the limiter")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if !errors.Is(writeErr, ErrWSNotFound) {
			t.Errorf("canceled status update error = %v, want ErrWSNotFound", writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("status update remained blocked after Close")
	}
}

func TestPresenceRateLimitResetsForNewConnection(t *testing.T) {
	old, _ := newGatewayTestConnection(t)
	replacement, peer := newGatewayTestConnection(t)
	session := &Session{LogLevel: -1, wsConn: old}
	session.gatewaySendRateLimiter.wait = func(time.Duration, <-chan struct{}) bool {
		t.Error("replacement connection inherited the old presence budget")
		return false
	}
	for i := 0; i < 5; i++ {
		if err := session.UpdateGameStatus(0, "game"); err != nil {
			t.Fatal(err)
		}
	}
	// Model Open's connection installation and limiter reset under its lock.
	session.Lock()
	session.wsConn = replacement
	session.gatewaySendRateLimiter.reset(replacement)
	session.Unlock()
	if err := session.UpdateCustomStatus("new connection"); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var write gatewayWrite
	if err := peer.ReadJSON(&write); err != nil || write.Op != 3 {
		t.Fatalf("replacement status opcode=%d, error=%v", write.Op, err)
	}
}

type delayedPresenceWriteConn struct {
	net.Conn
	now  *atomic.Int64
	once sync.Once
}

func (c *delayedPresenceWriteConn) Write(p []byte) (int, error) {
	if len(p) > 0 && p[0] == 0x81 {
		// Model a half-second delay between admission and transmission using
		// the same clock as the limiter. The WebSocket frame itself is real.
		c.once.Do(func() { c.now.Add(int64(500 * time.Millisecond)) })
	}
	return c.Conn.Write(p)
}

func TestPresenceRateLimitStartsAtCompletedWrite(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(1, 0).UnixNano())
	received := make(chan time.Time, 6)
	peerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(peerDone)
		defer close(received)
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = peer.Close() }()
		for i := range 8 {
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Error(err)
				return
			}
			var frame gatewayWrite
			if err := peer.ReadJSON(&frame); err != nil {
				t.Error(err)
				return
			}
			want := 3
			switch i {
			case 5:
				want = 8
			case 6:
				want = 1
			}
			if frame.Op != want {
				t.Errorf("frame %d opcode = %d, want %d", i, frame.Op, want)
			}
			if want == 3 {
				received <- time.Unix(0, now.Load())
			}
		}
	}))
	defer server.Close()
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &delayedPresenceWriteConn{Conn: conn, now: &now}, nil
	}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); <-peerDone }()
	session := &Session{LogLevel: -1, wsConn: conn, sequence: new(int64)}
	session.gatewaySendRateLimiter.now = func() time.Time { return time.Unix(0, now.Load()) }
	session.gatewaySendRateLimiter.wait = func(delay time.Duration, _ <-chan struct{}) bool {
		// Waiting for a presence slot must leave both ordinary gateway
		// messages and the heartbeat path able to acquire the write lock.
		if err := session.RequestGuildMembers("guild", "", 1, "nonce", false); err != nil {
			t.Error(err)
			return false
		}
		if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":1,"d":null}`)); err != nil {
			t.Error(err)
			return false
		}
		now.Add(int64(delay))
		return true
	}
	var first time.Time
	for i := range 6 {
		if err := session.UpdateGameStatus(0, "game"); err != nil {
			t.Fatal(err)
		}
		// Join each peer read before the next call can advance the clock.
		at, ok := <-received
		if !ok {
			t.Fatal("peer stopped before receiving all presence updates")
		}
		if i == 0 {
			first = at
		}
		if i == 5 && at.Sub(first) < 20*time.Second {
			t.Errorf("peer received six presence updates in %v, want at least 20s", at.Sub(first))
		}
	}
}

func TestReconnectCanceledBeforeOpenDoesNotDial(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = LogInformational
	session.gateway = "ws://discord.invalid/gateway"
	var attempts atomic.Int32
	session.Dialer = &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		attempts.Add(1)
		return nil, errors.New("test dial failure")
	}}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "trying to reconnect to gateway" {
			close(paused)
			<-release
		}
	}
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
		Logger = oldLogger
	}()
	go func() {
		defer close(done)
		session.reconnect(session.reconnectCancelSignal())
	}()
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not reach the open attempt")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled reconnect did not return")
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("canceled reconnect made %d dial attempts after Close returned", got)
	}
	session.RLock()
	canceled := reconnectCanceled(session.reconnectCancel)
	session.RUnlock()
	if !canceled {
		t.Error("canceled reconnect rearmed session cancellation")
	}
}

func TestGatewayRecoveryKeepsOriginalLifetime(t *testing.T) {
	for _, source := range []string{"read_error", "heartbeat", "reconnect_event", "invalid_session"} {
		for _, reopen := range []bool{false, true} {
			name := "current"
			if reopen {
				name = "close_then_failed_open"
			}
			t.Run(source+"/"+name, func(t *testing.T) {
				conn, peer := newGatewayTestConnection(t)
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				session.SyncEvents = true
				session.wsConn = conn
				session.listening = make(chan interface{})
				session.gateway = "ws://discord.invalid/gateway"
				originalCancel := session.reconnectCancelSignal()
				session.Client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{"url":"ws://discord.invalid/gateway"}`))}, nil
				})}
				attempted := make(chan struct{}, 4)
				var attempts atomic.Int32
				dialErr := errors.New("test dial failure")
				session.Dialer = &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
					attempts.Add(1)
					attempted <- struct{}{}
					return nil, dialErr
				}}
				callbackDone := make(chan struct{})
				var handled atomic.Bool
				session.AddHandler(func(s *Session, _ *Disconnect) {
					if !handled.CompareAndSwap(false, true) {
						return
					}
					defer close(callbackDone)
					if reopen {
						if err := s.Close(); err != nil {
							t.Errorf("Close: %v", err)
						}
						if err := s.Open(); !errors.Is(err, dialErr) {
							t.Errorf("explicit Open error = %v, want dial failure", err)
						}
					}
				})
				done := make(chan struct{})
				defer func() {
					_ = session.Close()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("gateway recovery worker did not stop during cleanup")
					}
				}()
				listening := session.listening
				// An outstanding heartbeat forces the real missed-ACK recovery path.
				session.lastHeartbeatSent = time.Now()
				go func() {
					defer close(done)
					if source == "heartbeat" {
						session.heartbeat(conn, listening, 1)
					} else {
						session.listen(conn, listening)
					}
				}()
				switch source {
				case "read_error":
					_ = peer.Close()
				case "reconnect_event", "invalid_session":
					payload := `{"op":7,"d":null}`
					if source == "invalid_session" {
						payload = `{"op":9,"d":false}`
					}
					if err := peer.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-callbackDone:
				case <-time.After(time.Second):
					t.Fatal("Disconnect callback did not finish")
				}
				select {
				case <-attempted:
				case <-time.After(time.Second):
					t.Fatal("expected Open attempt did not reach dialer")
				}
				if reopen {
					if !reconnectCanceled(originalCancel) {
						t.Error("Close did not cancel the original gateway lifetime")
					}
					select {
					case <-done:
					case <-attempted:
						t.Fatal("old gateway recovery dialed again after Close and failed explicit Open")
					case <-time.After(time.Second):
						t.Fatal("old gateway recovery did not return")
					}
					if reconnectCanceled(session.reconnectCancelSignal()) {
						t.Error("old gateway recovery canceled the explicit Open lifetime")
					}
				}
				if got := attempts.Load(); got != 1 {
					t.Errorf("dial attempts = %d, want 1", got)
				}
			})
		}
	}
}

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

// Pause after the transport has read a response, before the WebSocket reader
// can act on it. Closing the transport cannot retract bytes already read.
type gatewayReadResultBarrier struct {
	net.Conn
	active, entered, release chan struct{}
	once                     sync.Once
}

func (c *gatewayReadResultBarrier) Read(p []byte) (n int, err error) {
	n, err = c.Conn.Read(p)
	select {
	case <-c.active:
		c.once.Do(func() { close(c.entered) })
		<-c.release
	default:
	}
	return
}

func TestOpenStaleFailureDoesNotResetReplacement(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    int
		payload []byte
	}{
		{"invalid session", websocket.TextMessage, []byte(`{"op":9,"d":false}`)},
		{"malformed session", websocket.TextMessage, []byte(`{"op":9,"d":{}}`)},
		{"invalid sequence close", websocket.CloseMessage, websocket.FormatCloseMessage(4007, "invalid sequence")},
	} {
		t.Run(test.name, func(t *testing.T) {
			barrier := &gatewayReadResultBarrier{active: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
			serverDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
					return
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				close(barrier.active)
				if err := conn.WriteMessage(test.kind, test.payload); err != nil {
					return
				}
				<-serverDone
			}))
			defer server.Close()
			defer close(serverDone)
			session, err := newGatewayOpenTestSession(server, "Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.ShouldReconnectOnError = false
			session.Dialer = &websocket.Dialer{HandshakeTimeout: time.Second, NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				barrier.Conn = conn
				return barrier, err
			}}
			newConn, _ := newGatewayTestConnection(t)
			done := make(chan error, 1)
			go func() { done <- session.Open() }()
			defer func() {
				closeChannel(barrier.release)
				_ = session.Close()
			}()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				t.Fatal("Open did not read the startup failure")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			// Install the state a newer Open owns while the old reader is paused.
			session.Lock()
			session.wsConn = newConn
			session.sessionID = "replacement"
			session.resumeGatewayURL = "wss://replacement.gateway"
			atomic.StoreInt64(session.sequence, 42)
			session.Unlock()
			close(barrier.release)
			select {
			case err := <-done:
				if err == nil {
					t.Error("replaced Open reported success")
				}
			case <-time.After(time.Second):
				t.Fatal("old Open did not return")
			}
			session.RLock()
			preserved := session.wsConn == newConn && session.sessionID == "replacement" && session.resumeGatewayURL == "wss://replacement.gateway" && atomic.LoadInt64(session.sequence) == 42
			session.RUnlock()
			if !preserved {
				t.Error("old startup failure erased the replacement resume state")
			}
		})
	}
}

func TestCanceledGatewayReconnectPreservesNewVoice(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"READY","d":{"session_id":"session","user":{"id":"user"},"guilds":[]}}`)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = LogInformational
	session.ShouldReconnectVoiceOnSessionError = true
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	workersStarted := make(chan struct{}, 4)
	var scheduled atomic.Int32
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		switch format {
		case "called":
			pc, _, _, _ := runtime.Caller(3)
			name := runtime.FuncForPC(pc).Name()
			if strings.HasSuffix(name, ".(*Session).heartbeat") || strings.HasSuffix(name, ".(*Session).listen") {
				workersStarted <- struct{}{}
			}
		case "successfully reconnected to gateway":
			close(paused)
			<-release
		case "reconnecting voice connection to guild %s":
			scheduled.Add(1)
		}
	}
	defer func() { Logger = oldLogger }()
	defer func() { _ = session.Close() }()
	defer closeChannel(release)
	go func() {
		defer close(done)
		session.reconnect(session.reconnectCancelSignal())
	}()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway recovery did not reach voice scheduling")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Open(); err != nil {
		t.Fatal(err)
	}
	// Both generations' heartbeat/listener startup logs must finish before
	// cleanup restores the global logger. Manual Close then stops them silently.
	for i := 0; i < 4; i++ {
		select {
		case <-workersStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("gateway workers did not start")
		}
	}
	transportClose := make(chan struct{})
	voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session, Ready: true, close: transportClose}
	defer voice.Close()
	backendClosed := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
		// Stop fallback retries only after the unwanted teardown has happened.
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
		close(backendClosed)
	}}
	installFakeDAVE(voice, backend, 1)
	session.Lock()
	session.VoiceConnections["guild"] = voice
	session.Unlock()
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("old gateway recovery did not finish")
	}
	if got := scheduled.Load(); got != 0 {
		t.Errorf("canceled gateway recovery scheduled %d new voice reconnects", got)
		select {
		case <-backendClosed:
		case <-time.After(time.Second):
			t.Fatal("scheduled voice recovery did not reach teardown")
		}
		// Wait for recovery to clear its state before inspecting and cleaning up.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(time.Second)
		for {
			voice.RLock()
			reconnecting := voice.reconnecting
			voice.RUnlock()
			if !reconnecting {
				break
			}
			select {
			case <-ticker.C:
			case <-timeout:
				t.Fatal("voice recovery did not finish")
			}
		}
	}
	voice.RLock()
	preserved := voice.Ready && voice.close == transportClose
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("old gateway recovery changed new voice: transport preserved=%t, backend closes=%d", preserved, closed)
	}
}

func TestGatewayVoiceReconnectPreservesNewTransport(t *testing.T) {
	oldConn, _ := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	synctest.Test(t, func(t *testing.T) {
		session, err := New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		session.LogLevel = LogInformational
		voice := &VoiceConnection{
			LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
			wsConn: oldConn, close: make(chan struct{}), Ready: true,
		}
		session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
		paused, release := make(chan struct{}), make(chan struct{})
		oldLogger := Logger
		Logger = func(_, _ int, format string, _ ...interface{}) {
			if format == "reconnecting voice connection to guild %s" {
				close(paused)
				<-release
			}
		}
		defer func() { Logger = oldLogger }()
		defer voice.Close()
		defer closeChannel(release)
		done := make(chan struct{})
		go func() {
			defer close(done)
			session.reconnectVoiceConnections(session.reconnectCancelSignal())
		}()
		<-paused

		// A voice server update can replace this transport independently of
		// the gateway's recovery batch. Model its completed installation.
		voice.Close()
		replacementClose := make(chan struct{})
		backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
			// Stop fallback retries only after the unwanted teardown.
			session.Lock()
			delete(session.VoiceConnections, "guild")
			session.Unlock()
		}}
		installFakeDAVE(voice, backend, 1)
		voice.Lock()
		voice.wsConn = newConn
		voice.close = replacementClose
		voice.Ready = true
		voice.Unlock()

		close(release)
		<-done
		synctest.Wait()
		voice.RLock()
		preserved := voice.wsConn == newConn && voice.close == replacementClose && voice.Ready
		reconnecting := voice.reconnecting || voice.reconnectAgain
		voice.RUnlock()
		backend.mu.Lock()
		closed := backend.closed
		backend.mu.Unlock()
		if !preserved || reconnecting || closed != 0 {
			t.Errorf("queued gateway recovery changed replacement: transport preserved=%t, reconnecting=%t, backend closes=%d", preserved, reconnecting, closed)
		}
		select {
		case <-replacementClose:
			t.Error("queued gateway recovery canceled the replacement transport")
		default:
		}
	})
}
