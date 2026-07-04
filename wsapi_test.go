package discordgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestChannelVoiceJoinManualNilWsConn(t *testing.T) {
	s := &Session{}

	err := s.ChannelVoiceJoinManual("guild", "channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChannelVoiceJoinManual() error = %v, want %v", err, ErrWSNotFound)
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

func TestOpenRejectsHeartbeatIntervalOverflowingAckDeadline(t *testing.T) {
	// 3e12 msec passes a bound that only protects the msec-to-Duration
	// conversion, but interval*FailedHeartbeatAcks overflows int64 and
	// turns the missed-ACK deadline negative.
	server := newGatewayOpenTestServerWithHello(t, []byte(`{"op":10,"d":{"heartbeat_interval":3000000000000}}`))
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

func TestMaxGatewayHeartbeatIntervalCannotOverflowAckDeadline(t *testing.T) {
	if maxGatewayHeartbeatIntervalMsec > time.Duration(1<<63-1)/FailedHeartbeatAcks {
		t.Fatalf("maxGatewayHeartbeatIntervalMsec %d allows interval*FailedHeartbeatAcks to overflow", maxGatewayHeartbeatIntervalMsec)
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
		session.reconnect()
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
		LastHeartbeatAck: time.Now().Add(time.Hour).UTC(),
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
	oldJitter := gatewayHeartbeatInitialJitter
	gatewayHeartbeatInitialJitter = func(time.Duration) time.Duration {
		return 50 * time.Millisecond
	}
	defer func() {
		gatewayHeartbeatInitialJitter = oldJitter
	}()

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
		LastHeartbeatAck: time.Now().Add(time.Hour).UTC(),
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

func TestHeartbeatUsesRestartCloseOnMissedAck(t *testing.T) {
	closeCode := make(chan int, 1)

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		conn.SetCloseHandler(func(code int, text string) error {
			closeCode <- code
			return nil
		})

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
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
	session := &Session{
		ShouldReconnectOnError: false,
		sequence:               new(int64),
		wsConn:                 conn,
		listening:              listening,
	}
	session.LastHeartbeatAck = time.Now().Add(-time.Second)

	done := make(chan struct{})
	go func() {
		session.heartbeat(conn, listening, 1)
		close(done)
	}()

	select {
	case code := <-closeCode:
		if code != websocket.CloseServiceRestart {
			t.Fatalf("close code = %d, want %d", code, websocket.CloseServiceRestart)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive heartbeat close frame")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not return")
	}
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
		session.reconnect()
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
		session.reconnect()
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
		session.reconnect()
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
