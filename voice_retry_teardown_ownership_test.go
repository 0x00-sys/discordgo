package discordgo

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceQueuedRetryTeardownPreservesNewerJoin(t *testing.T) {
	testVoiceQueuedRetryTeardown(t, true)
}

func TestVoiceQueuedRetryTeardownClosesCurrentTransport(t *testing.T) {
	testVoiceQueuedRetryTeardown(t, false)
}

func testVoiceQueuedRetryTeardown(t *testing.T, newerJoin bool) {
	t.Helper()
	gateway, peer := newGatewayTestConnection(t)
	failedConn, _ := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn, session.DataReady = gateway, true
	voice := &VoiceConnection{LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var completed, pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "successfully reconnected to channel %s" {
			completed.Store(true)
		}
		if format == "called" && completed.Load() && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	go func() {
		defer close(done)
		voice.reconnectWithResume(false, nil, nil)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
	}()
	readJoin := func() {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var update voiceChannelJoinOp
		if err := peer.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "channel" {
			t.Fatalf("unexpected join: %+v", update)
		}
	}
	readJoin()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := udp.Close(); err != nil {
		t.Fatal(err)
	}
	failedClose, failedDone := make(chan struct{}), make(chan struct{})
	installFakeDAVE(voice, &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() { close(failedDone) }}, 1)
	voice.Lock()
	voice.wsConn, voice.udpConn, voice.close = failedConn, udp, failedClose
	voice.Unlock()
	// An actual failed keepalive queues recovery while the first join is waiting.
	voice.udpKeepAlive(udp, failedClose, time.Second)
	select {
	case <-failedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("UDP failure did not finish transport teardown")
	}
	voice.lifecycleMu.Lock()
	voice.RLock()
	queued := voice.reconnecting && voice.reconnectAgain && voice.close == nil
	voice.RUnlock()
	if !queued {
		voice.lifecycleMu.Unlock()
		t.Fatal("UDP failure did not queue recovery")
	}
	// Model handshake completion before the waiting recovery join returns.
	// Installation takes the real lifecycle lock, before the later pause.
	replacementClose := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
		// Stop additional attempts only after the unwanted teardown occurs.
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
	}}
	installFakeDAVE(voice, backend, 1)
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	voice.lifecycleMu.Unlock()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("queued recovery did not reach its later teardown")
	}
	// Same-channel public joins can use this ready transport without installing
	// anything while the obsolete closer holds lifecycleMu.
	if newerJoin {
		joined, joinErr := session.ChannelVoiceJoin("guild", "channel", false, false)
		readJoin()
		if joinErr != nil || joined != voice {
			t.Fatalf("newer public join = %p, %v; want existing handle and success", joined, joinErr)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("obsolete recovery did not finish")
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready
	reconnecting := voice.reconnecting || voice.reconnectAgain
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if reconnecting {
		t.Error("recovery flags remained set after worker completion")
	}
	if newerJoin && (!preserved || closed != 0) {
		t.Errorf("queued teardown invalidated successful newer join: preserved=%t, backend closes=%d", preserved, closed)
	} else if !newerJoin && (preserved || closed != 1) {
		t.Errorf("current recovery did not tear down its transport: preserved=%t, backend closes=%d", preserved, closed)
	}
	select {
	case <-replacementClose:
		if newerJoin {
			t.Error("queued teardown closed the newer join's transport lifetime")
		}
	default:
		if !newerJoin {
			t.Error("current recovery left its transport lifetime open")
		}
	}
}
