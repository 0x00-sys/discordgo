package discordgo

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceJoinTimeoutPreservesNewerJoin(t *testing.T) {
	gateway, peer := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.wsConn = gateway
	session.LogLevel = LogWarning
	paused, release := make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error waiting for voice to connect, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	firstDone := make(chan struct{})
	var secondDone chan struct{}
	var firstErr, secondErr error
	var secondVoice *VoiceConnection
	go func() {
		defer close(firstDone)
		_, firstErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-firstDone
		if secondDone != nil {
			<-secondDone
		}
	}()
	readJoin := func() {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var op voiceChannelJoinOp
		if err := peer.ReadJSON(&op); err != nil {
			t.Fatal(err)
		}
		if op.Op != 4 || op.Data.ChannelID == nil || *op.Data.ChannelID != "channel" {
			t.Fatalf("unexpected voice join: %#v", op)
		}
	}
	readJoin()
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("first voice join did not reach timeout cleanup")
	}
	session.RLock()
	voice := session.VoiceConnections["guild"]
	session.RUnlock()
	secondDone = make(chan struct{})
	go func() {
		defer close(secondDone)
		secondVoice, secondErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	readJoin()
	// Model completion of the later join after its real gateway request.
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	installFakeDAVE(voice, backend, 1)
	replacementClose := make(chan struct{})
	voice.Lock()
	voice.wsConn = replacement
	voice.close = replacementClose
	voice.Ready = true
	voice.Unlock()
	select {
	case <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("second voice join did not finish")
	}
	if secondErr != nil || secondVoice != voice {
		t.Fatalf("second join = %p, %v; want the existing voice and success", secondVoice, secondErr)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first voice join did not finish cleanup")
	}
	if firstErr == nil || !strings.Contains(firstErr.Error(), "timeout waiting for voice") {
		t.Fatalf("first join error = %v, want readiness timeout", firstErr)
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("old join cleanup changed successful newer join: transport preserved=%t, backend closes=%d", preserved, closed)
	}
}
