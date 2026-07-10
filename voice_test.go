package discordgo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOpusSenderNilAEAD(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP returned error: %v", err)
	}
	defer udpConn.Close()

	opus := make(chan []byte, 1)
	opus <- []byte{0x01, 0x02}
	close(opus)

	v := &VoiceConnection{LogLevel: -1}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("opusSender panicked with nil aead: %v", r)
		}
	}()

	v.opusSender(udpConn, make(chan struct{}), opus, 48000, 960)
}

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

func TestUDPOpenReadTimeout(t *testing.T) {
	oldTimeout := voiceUDPReadTimeout
	voiceUDPReadTimeout = 25 * time.Millisecond
	defer func() {
		voiceUDPReadTimeout = oldTimeout
	}()

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("error listening on udp: %v", err)
	}
	defer server.Close()

	addr := server.LocalAddr().(*net.UDPAddr)
	v := &VoiceConnection{
		wsConn:   &websocket.Conn{},
		close:    make(chan struct{}),
		endpoint: "127.0.0.1",
		op2: voiceOP2{
			IP:   "127.0.0.1",
			Port: addr.Port,
			SSRC: 1,
		},
	}
	defer func() {
		if v.udpConn != nil {
			v.udpConn.Close()
		}
	}()

	start := time.Now()
	err = v.udpOpen()
	if err == nil {
		t.Fatal("expected udpOpen to time out waiting for discovery response")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("udpOpen took too long to return: %s", elapsed)
	}

	lockAcquired := make(chan struct{})
	go func() {
		v.RLock()
		v.RUnlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("udpOpen returned without releasing the voice lock")
	}
}

func TestVoiceCloseKeepsOpusChannels(t *testing.T) {
	// Close runs on every internal reconnect (voice server updates,
	// websocket errors, 4014 channel moves); user pipelines hold these
	// channels and must survive it.
	opusSend := make(chan []byte)
	opusRecv := make(chan *Packet)
	voice := &VoiceConnection{
		OpusSend: opusSend,
		OpusRecv: opusRecv,
	}

	voice.Close()

	if voice.OpusSend != opusSend {
		t.Fatal("OpusSend was replaced by Close")
	}
	if voice.OpusRecv != opusRecv {
		t.Fatal("OpusRecv was replaced by Close")
	}
}

func TestVoiceDisconnectClearsOpusChannels(t *testing.T) {
	session := &Session{VoiceConnections: map[string]*VoiceConnection{}}
	voice := &VoiceConnection{
		session:  session,
		GuildID:  "guild",
		OpusSend: make(chan []byte),
		OpusRecv: make(chan *Packet),
	}
	session.VoiceConnections["guild"] = voice

	close(voice.OpusSend)
	close(voice.OpusRecv)

	if err := voice.Disconnect(); err != nil {
		t.Fatalf("Disconnect returned error: %v", err)
	}

	if voice.OpusSend != nil {
		t.Fatal("OpusSend was not cleared")
	}
	if voice.OpusRecv != nil {
		t.Fatal("OpusRecv was not cleared")
	}
	if _, ok := session.VoiceConnections["guild"]; ok {
		t.Fatal("voice connection was not removed from session")
	}
}

func TestVoiceCloseWithLockedWSMutex(t *testing.T) {
	server := newCloseFrameTestServer(t)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	voice := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		wsConn:   conn,
	}

	voice.wsMutex.Lock()
	done := make(chan error, 1)
	go func() {
		voice.Close()
		done <- nil
	}()

	requireCloseDoneWhileWSMutexLocked(t, func() {
		voice.wsMutex.Unlock()
	}, done)
}

func TestVoiceReconnectStopsWhenUnregistered(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		ChannelID: "channel",
		LogLevel:  -1,
		session: &Session{
			VoiceConnections: make(map[string]*VoiceConnection),
		},
	}

	done := make(chan struct{})
	go func() {
		v.reconnect()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not stop for an unregistered voice connection")
	}

	v.RLock()
	reconnecting := v.reconnecting
	v.RUnlock()
	if reconnecting {
		t.Fatal("reconnecting was not cleared")
	}
}

func TestVoiceReconnectResetSkipsReplacement(t *testing.T) {
	session := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	stale := &VoiceConnection{
		GuildID: "guild",
		session: session,
	}
	session.VoiceConnections[stale.GuildID] = stale

	session.voiceMutex.Lock()
	type resetResult struct {
		sent bool
		err  error
	}
	done := make(chan resetResult, 1)
	go func() {
		sent, err := stale.sendDisconnectIfCurrent()
		done <- resetResult{sent: sent, err: err}
	}()
	time.Sleep(50 * time.Millisecond)

	replacement := &VoiceConnection{GuildID: stale.GuildID, session: session}
	session.Lock()
	session.VoiceConnections[stale.GuildID] = replacement
	session.Unlock()
	session.voiceMutex.Unlock()

	result := <-done
	if result.sent {
		t.Fatal("stale reconnect sent a disconnect packet after replacement")
	}
	if result.err != nil {
		t.Fatalf("sendDisconnectIfCurrent() error = %v", result.err)
	}

	session.RLock()
	registered := session.VoiceConnections[stale.GuildID]
	session.RUnlock()
	if registered != replacement {
		t.Fatal("stale reconnect removed the replacement voice connection")
	}
}

func TestVoiceChangeChannelNilSessionWsConn(t *testing.T) {
	v := &VoiceConnection{
		GuildID: "guild",
		session: &Session{},
	}

	err := v.ChangeChannel("channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestVoiceChangeChannelNilSession(t *testing.T) {
	v := &VoiceConnection{GuildID: "guild"}

	err := v.ChangeChannel("channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestVoiceDisconnectNilSessionWsConn(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
		session:   &Session{},
	}

	err := v.Disconnect()
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("Disconnect() error = %v, want %v", err, ErrWSNotFound)
	}
	if v.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", v.sessionID)
	}
}

func TestVoiceDisconnectNilSession(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
	}

	err := v.Disconnect()
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("Disconnect() error = %v, want %v", err, ErrWSNotFound)
	}
	if v.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", v.sessionID)
	}
}

func TestVoiceDisconnectDoesNotAcquireGatewayWriteLockBeforeTeardown(t *testing.T) {
	conn, _ := newGatewayTestConnection(t)
	session := &Session{
		wsConn:           conn,
		VoiceConnections: make(map[string]*VoiceConnection),
	}
	voice := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
		session:   session,
	}
	session.VoiceConnections[voice.GuildID] = voice

	session.wsMutex.Lock()
	wsMutexLocked := true
	defer func() {
		if wsMutexLocked {
			session.wsMutex.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- voice.Disconnect()
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		voice.RLock()
		disconnecting := voice.disconnecting
		voice.RUnlock()
		if disconnecting {
			break
		}

		select {
		case err := <-done:
			session.wsMutex.Unlock()
			wsMutexLocked = false
			t.Fatalf("Disconnect returned before the gateway write lock was available: %v", err)
		case <-timer.C:
			session.wsMutex.Unlock()
			wsMutexLocked = false
			t.Fatal("Disconnect waited for the gateway write lock before tearing down local state")
		case <-time.After(time.Millisecond):
		}
	}

	session.RLock()
	registered := session.VoiceConnections[voice.GuildID]
	session.RUnlock()
	if registered != nil {
		t.Fatal("Disconnect did not unregister before the gateway write")
	}

	session.wsMutex.Unlock()
	wsMutexLocked = false
	if err := <-done; err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
}

func TestRedactedVoiceData(t *testing.T) {
	data := []byte(`{"op":4,"d":{"token":"voice-token","secret_key":[1,2,3],"nested":{"access_token":"access-token","refresh_token":"refresh-token","safe":"ok"}}}`)

	got := redactedVoiceData(data)
	for _, secret := range []string{"voice-token", "access-token", "refresh-token", "1,2,3"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedVoiceData() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedVoiceData() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedVoiceData() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedVoiceDataInvalidJSON(t *testing.T) {
	data := []byte("not-json")

	if got := redactedVoiceData(data); got != string(data) {
		t.Fatalf("redactedVoiceData() = %q, want %q", got, string(data))
	}
}

func TestVoiceSpeakingConcurrentClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	vc := &VoiceConnection{wsConn: conn}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = vc.Speaking(true)
			}
		}
	}()

	time.Sleep(time.Millisecond)
	vc.Close()
	close(stop)
	<-done
}

func TestVoiceSpeakingHandlerPanicDoesNotAbortDispatch(t *testing.T) {
	vc := &VoiceConnection{LogLevel: -1}
	vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {
		panic("boom")
	})

	called := make(chan struct{})
	vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {
		close(called)
	})

	vc.onEvent([]byte(`{"op":5,"d":{"user_id":"user","ssrc":1,"speaking":true}}`))

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("second speaking handler was not called")
	}
}

func TestVoiceSpeakingHandlersConcurrentDispatch(t *testing.T) {
	vc := &VoiceConnection{LogLevel: -1}
	vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {})

	payload := []byte(`{"op":5,"d":{"user_id":"user","ssrc":1,"speaking":true}}`)
	done := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					vc.onEvent(payload)
				}
			}
		}()
	}

	for i := 0; i < 1000; i++ {
		vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {})
	}
	close(done)
	wg.Wait()
}

func TestVoiceHeartbeatIgnoresInvalidInterval(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		vc := &VoiceConnection{LogLevel: -1}
		vc.wsHeartbeat(&websocket.Conn{}, make(chan struct{}), 0)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wsHeartbeat did not return for invalid interval")
	}
}

func TestVoiceHelloStartsHeartbeat(t *testing.T) {
	messages := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		_, message, err := c.ReadMessage()
		if err == nil {
			messages <- message
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	closeChan := make(chan struct{})
	defer close(closeChan)

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    closeChan,
		wsConn:   conn,
	}

	vc.onEvent([]byte(`{"op":8,"d":{"heartbeat_interval":1}}`))

	select {
	case message := <-messages:
		if !strings.Contains(string(message), `"op":3`) {
			t.Fatalf("heartbeat message = %s, want op 3", message)
		}
	case <-time.After(time.Second):
		t.Fatal("voice Hello did not start heartbeat")
	}
}

func TestVoiceReadyDoesNotStartHeartbeat(t *testing.T) {
	messages := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		_, message, err := c.ReadMessage()
		if err == nil {
			messages <- message
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		wsConn:   conn,
	}
	defer close(vc.close)

	vc.onEvent([]byte(`{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":1,"modes":["aead_aes256_gcm_rtpsize"],"heartbeat_interval":1}}`))

	select {
	case message := <-messages:
		t.Fatalf("voice Ready started heartbeat %s", message)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestVoiceWsListenManualCloseDoesNotReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	vc := &VoiceConnection{
		close:   make(chan struct{}),
		session: &Session{},
		wsConn:  conn,
	}

	closeChan := vc.close
	done := make(chan struct{})
	go func() {
		defer close(done)
		vc.wsListen(conn, closeChan)
	}()

	vc.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wsListen did not exit after Close")
	}

	vc.RLock()
	reconnecting := vc.reconnecting
	vc.RUnlock()
	if reconnecting {
		t.Fatal("wsListen started reconnect after manual Close")
	}
}

func TestVoiceReadyConcurrentClose(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer udpConn.Close()

	udpDone := make(chan struct{})
	go func() {
		defer close(udpDone)
		buf := make([]byte, 74)
		for {
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n != 74 {
				continue
			}
			resp := make([]byte, 74)
			copy(resp[8:], []byte("127.0.0.1"))
			binary.BigEndian.PutUint16(resp[72:], uint16(udpConn.LocalAddr().(*net.UDPAddr).Port))
			if _, err = udpConn.WriteToUDP(resp, addr); err != nil {
				return
			}
		}
	}()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	udpAddr := udpConn.LocalAddr().(*net.UDPAddr)
	ready := []byte(fmt.Sprintf(`{"op":2,"d":{"ssrc":1,"ip":%q,"port":%d,"modes":["aead_aes256_gcm_rtpsize"],"heartbeat_interval":1}}`, udpAddr.IP.String(), udpAddr.Port))

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		endpoint: "voice.example.com",
		wsConn:   conn,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		vc.onEvent(ready)
	}()

	time.Sleep(time.Millisecond)
	vc.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("voice READY handling did not finish after Close")
	}
}

// textFrameBlockingConn blocks websocket text-frame writes until released,
// while letting the HTTP handshake and control frames (e.g. close) pass
// through, to simulate a stalled TCP send.
type textFrameBlockingConn struct {
	net.Conn
	block       bool
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func (c *textFrameBlockingConn) Write(p []byte) (int, error) {
	if c.block && len(p) > 0 && int(p[0]&0x0f) == websocket.TextMessage {
		c.enteredOnce.Do(func() { close(c.entered) })
		<-c.release
	}
	return c.Conn.Write(p)
}

func (c *textFrameBlockingConn) releaseWrites() {
	c.releaseOnce.Do(func() { close(c.release) })
}

func newTextFrameBlockingWebsocket(t *testing.T) (*websocket.Conn, *textFrameBlockingConn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	blocking := &textFrameBlockingConn{
		block:   true,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	dialer := websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			blocking.Conn = conn
			return blocking, nil
		},
	}

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	t.Cleanup(blocking.releaseWrites)

	return conn, blocking
}

func TestVoiceSpeakingStalledWriteDoesNotBlockClose(t *testing.T) {
	conn, blocking := newTextFrameBlockingWebsocket(t)

	vc := &VoiceConnection{LogLevel: -1, wsConn: conn}

	speakingDone := make(chan struct{})
	go func() {
		defer close(speakingDone)
		_ = vc.Speaking(true)
	}()

	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Speaking never reached the websocket write")
	}

	closeDone := make(chan struct{})
	go func() {
		vc.Close()
		close(closeDone)
	}()

	// Close sleeps one second after the close frame by design; anything
	// well beyond that means it is wedged behind the stalled write.
	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked behind a stalled Speaking write")
	}

	blocking.releaseWrites()
	<-speakingDone
}

func TestVoiceDisconnectStalledGatewayWriteDoesNotBlockCloseOrReorderUpdates(t *testing.T) {
	conn, blocking := newTextFrameBlockingWebsocket(t)

	session := &Session{
		LogLevel:         -1,
		wsConn:           conn,
		VoiceConnections: make(map[string]*VoiceConnection),
	}
	voice := &VoiceConnection{
		LogLevel:  -1,
		GuildID:   "guild",
		sessionID: "session",
		session:   session,
	}
	session.VoiceConnections[voice.GuildID] = voice

	disconnectDone := make(chan error, 1)
	go func() {
		disconnectDone <- voice.Disconnect()
	}()

	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect never reached the gateway write")
	}

	closeDone := make(chan struct{})
	go func() {
		voice.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(time.Second):
		blocking.releaseWrites()
		t.Fatal("Close blocked behind a stalled Disconnect gateway write")
	}

	changeDone := make(chan error, 1)
	go func() {
		changeDone <- voice.ChangeChannel("stale", false, false)
	}()

	type joinResult struct {
		voice *VoiceConnection
		err   error
	}
	joinDone := make(chan joinResult, 1)
	go func() {
		joined, joinErr := session.ChannelVoiceJoin(voice.GuildID, "replacement", false, false)
		joinDone <- joinResult{voice: joined, err: joinErr}
	}()

	blocking.releaseWrites()

	var replacement *VoiceConnection
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for replacement == nil || replacement == voice {
		session.RLock()
		replacement = session.VoiceConnections[voice.GuildID]
		session.RUnlock()
		if replacement != nil && replacement != voice {
			break
		}

		select {
		case <-timer.C:
			t.Fatal("ChannelVoiceJoin reused the disconnecting voice connection")
		case <-time.After(time.Millisecond):
		}
	}

	replacement.Lock()
	replacement.Ready = true
	replacement.Unlock()

	if err := <-disconnectDone; err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if err := <-changeDone; !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
	joined := <-joinDone
	if joined.err != nil {
		t.Fatalf("ChannelVoiceJoin() error = %v", joined.err)
	}
	if joined.voice != replacement {
		t.Fatal("ChannelVoiceJoin did not return the replacement voice connection")
	}

	session.RLock()
	got := session.VoiceConnections[voice.GuildID]
	session.RUnlock()
	if got != replacement {
		t.Fatal("Disconnect removed a replacement voice connection")
	}
}
