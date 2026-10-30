package discordgo

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceQueuedRetryPreservesNewerJoin(t *testing.T) {
	gateway, peer := newGatewayTestConnection(t)
	failedConn, _ := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn, session.DataReady = gateway, true
	voice := &VoiceConnection{
		LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session,
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error reconnecting to channel %s, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	var joinDone chan struct{}
	go func() {
		defer close(done)
		voice.reconnectWithResume(false, nil, nil)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
		if joinDone != nil {
			<-joinDone
		}
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
	// Model a transport installed during the first recovery's join wait.
	// A real failed UDP keepalive queues another recovery and tears it down.
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
	voice.udpKeepAlive(udp, failedClose, time.Second)
	select {
	case <-failedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("UDP failure did not tear down the attempted transport")
	}
	voice.lifecycleMu.Lock()
	voice.RLock()
	queued := voice.reconnecting && voice.reconnectAgain && voice.close == nil
	voice.RUnlock()
	voice.lifecycleMu.Unlock()
	if !queued {
		t.Fatal("UDP failure did not queue recovery")
	}
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("original join did not reach its failure reset")
	}
	joinDone = make(chan struct{})
	var joined *VoiceConnection
	var joinErr error
	go func() {
		defer close(joinDone)
		joined, joinErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	readJoin()
	// Model completion of the newer public join after its actual OP4 write.
	replacementClose := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
		// Stop further retries only after the unwanted destructive teardown.
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
	}}
	installFakeDAVE(voice, backend, 1)
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	select {
	case <-joinDone:
	case <-time.After(3 * time.Second):
		t.Fatal("newer public join did not complete")
	}
	if joined != voice || joinErr != nil {
		t.Fatalf("newer join = %p, %v; want same handle and success", joined, joinErr)
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
	if !preserved || reconnecting || closed != 0 {
		t.Errorf("obsolete queued recovery changed newer join: preserved=%t, reconnecting=%t, backend closes=%d", preserved, reconnecting, closed)
	}
	select {
	case <-replacementClose:
		t.Error("obsolete queued recovery canceled the replacement lifetime")
	default:
	}
}
