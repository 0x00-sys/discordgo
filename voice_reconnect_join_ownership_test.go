package discordgo

import (
	"testing"
	"time"
)

func TestVoiceReconnectDoesNotRejoinAfterDisconnect(t *testing.T) {
	conn, peer := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.DataReady, session.wsConn = true, conn
	voice := &VoiceConnection{
		LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session,
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "trying to reconnect to channel %s" {
			close(paused)
			<-release
		}
	}
	requests := make(chan voiceChannelJoinOp, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		var request voiceChannelJoinOp
		if err := peer.ReadJSON(&request); err == nil {
			requests <- request
		}
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		_ = peer.Close()
		<-done
		<-readDone
		Logger = oldLogger
	}()
	go func() {
		defer close(done)
		voice.reconnectWithResume(true, nil, nil)
	}()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("voice recovery did not reach its join attempt")
	}
	if err := voice.Disconnect(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case request := <-requests:
		t.Errorf("disconnected recovery sent a gateway request: op=%d, channel=%v", request.Op, request.Data.ChannelID)
		// Stop the unexpected join's readiness wait only after observing its
		// request, so cleanup cannot hide the original failure.
		session.RLock()
		unexpected := session.VoiceConnections["guild"]
		session.RUnlock()
		if unexpected != nil {
			t.Error("disconnected recovery registered a new voice connection")
			_ = unexpected.Disconnect()
		}
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected recovery did not return")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("voice recovery did not finish")
	}
	session.RLock()
	registered := session.VoiceConnections["guild"]
	session.RUnlock()
	if registered != nil {
		t.Error("voice connection remains registered after Disconnect")
	}
	if voice.reconnecting {
		t.Error("voice reconnecting flag was not cleared")
	}
}
