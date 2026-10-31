package discordgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type voiceJoinChangeCleanupPool struct {
	puts    atomic.Uint32
	written chan struct{}
	release chan struct{}
}

func (*voiceJoinChangeCleanupPool) Get() interface{} { return nil }
func (p *voiceJoinChangeCleanupPool) Put(interface{}) {
	if p.puts.Add(1) == 2 {
		close(p.written)
		<-p.release
	}
}

func TestVoiceJoinTimeoutPreservesChannelChangeDuringWrite(t *testing.T) {
	peerReady := make(chan *websocket.Conn, 1)
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		peerReady <- conn
		<-serverDone
	}))
	defer server.Close()
	defer close(serverDone)
	pool := &voiceJoinChangeCleanupPool{written: make(chan struct{}), release: make(chan struct{})}
	dialer := websocket.Dialer{WriteBufferPool: pool}
	gateway, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-peerReady
	defer func() { _ = gateway.Close(); _ = peer.Close() }()
	replacement, replacementPeer := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.wsConn, session.LogLevel = gateway, LogWarning
	paused, releaseTimeout := make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error waiting for voice to connect, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-releaseTimeout
		}
	}
	defer func() { Logger = oldLogger }()
	firstDone := make(chan struct{})
	var changeDone chan struct{}
	var firstErr, changeErr error
	go func() {
		defer close(firstDone)
		_, firstErr = session.ChannelVoiceJoin("guild", "old-channel", false, false)
	}()
	defer func() {
		closeChannel(releaseTimeout)
		closeChannel(pool.release)
		_ = session.Close()
		<-firstDone
		if changeDone != nil {
			<-changeDone
		}
	}()
	readJoin := func(channelID string) {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var op voiceChannelJoinOp
		if err := peer.ReadJSON(&op); err != nil {
			t.Fatal(err)
		}
		if op.Op != 4 || op.Data.ChannelID == nil || *op.Data.ChannelID != channelID {
			t.Fatalf("voice request = %#v, want channel %s", op, channelID)
		}
	}
	readJoin("old-channel")
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("old join did not reach timeout cleanup")
	}
	session.RLock()
	voice := session.VoiceConnections["guild"]
	session.RUnlock()
	changeDone = make(chan struct{})
	go func() {
		defer close(changeDone)
		changeErr = voice.ChangeChannel("new-channel", false, false)
	}()
	readJoin("new-channel")
	select {
	case <-pool.written:
	case <-time.After(3 * time.Second):
		t.Fatal("channel change did not reach buffer recycling")
	}
	// Model a replacement transport after the new request reached the peer,
	// while buffer recycling still holds its caller before the local commit.
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	installFakeDAVE(voice, backend, 1)
	replacementClose := make(chan struct{})
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	close(releaseTimeout)
	// Give the released timeout cleanup an opportunity to run before the
	// newer write returns. A correctly serialized cleanup waits for it.
	select {
	case <-firstDone:
	case <-time.After(250 * time.Millisecond):
	}
	close(pool.release)
	for _, done := range []<-chan struct{}{changeDone, firstDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("voice request did not finish")
		}
	}
	if changeErr != nil || firstErr == nil || !strings.Contains(firstErr.Error(), "timeout waiting for voice") {
		t.Fatalf("change/old join errors = %v/%v", changeErr, firstErr)
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready && voice.ChannelID == "new-channel"
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("old join cleanup destroyed newer transport: preserved=%t, backend closes=%d", preserved, closed)
	}
	if err := voice.Speaking(true); err != nil {
		t.Fatalf("new transport is unusable after channel change: %v", err)
	}
	if err := replacementPeer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var speaking struct {
		Op int `json:"op"`
	}
	if err := replacementPeer.ReadJSON(&speaking); err != nil || speaking.Op != 5 {
		t.Errorf("replacement speaking command = %d, %v", speaking.Op, err)
	}
}
