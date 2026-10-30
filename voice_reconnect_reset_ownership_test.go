package discordgo

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceReconnectFailureDoesNotDisconnectNewerJoin(t *testing.T) {
	testVoiceReconnectResetOwnership(t, true)
}

func TestVoiceReconnectFailureResetsCurrentJoin(t *testing.T) {
	testVoiceReconnectResetOwnership(t, false)
}

func testVoiceReconnectResetOwnership(t *testing.T, newerJoin bool) {
	t.Helper()
	gateway, peer := newGatewayTestConnection(t)
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
	paused, release := make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error reconnecting to channel %s, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	done := make(chan struct{})
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
	readUpdate := func() voiceChannelJoinOp {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var update voiceChannelJoinOp
		if err := peer.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		return update
	}
	checkJoin := func(update voiceChannelJoinOp) {
		t.Helper()
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "channel" {
			t.Fatalf("unexpected join: %+v", update)
		}
	}
	checkJoin(readUpdate())
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("recovery did not reach its failed-join reset")
	}
	if !newerJoin {
		close(release)
		update := readUpdate()
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID != nil || !update.Data.SelfMute || !update.Data.SelfDeaf {
			t.Errorf("current recovery reset = %+v, want a leave request", update)
		}
		return
	}
	joinDone = make(chan struct{})
	var joined *VoiceConnection
	var joinErr error
	go func() {
		defer close(joinDone)
		joined, joinErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	checkJoin(readUpdate())
	// Model the newer join completing after its actual gateway request.
	replacementClose := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	installFakeDAVE(voice, backend, 1)
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	select {
	case <-joinDone:
	case <-time.After(3 * time.Second):
		t.Fatal("newer join did not finish")
	}
	if joined != voice || joinErr != nil {
		t.Fatalf("newer join = %p, %v; want original handle and success", joined, joinErr)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("obsolete recovery did not finish")
	}
	// All recovery writes precede this marker, so a reset cannot be hidden
	// by the scheduling of a reader goroutine or by fixture cleanup.
	if err := session.ChannelVoiceJoinManual("marker", "marker", false, false); err != nil {
		t.Fatal(err)
	}
	update := readUpdate()
	if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "marker" || update.Data.ChannelID == nil || *update.Data.ChannelID != "marker" {
		t.Errorf("obsolete recovery sent an unexpected voice update before the marker: %+v", update)
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready && !voice.reconnecting
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("obsolete recovery changed newer transport: preserved=%t, backend closes=%d", preserved, closed)
	}
}
