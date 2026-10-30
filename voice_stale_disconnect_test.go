package discordgo

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceDisconnectAfterTerminalClosePreservesNewJoin(t *testing.T) {
	gateway, gatewayPeer := newGatewayTestConnection(t)
	oldSocket, oldPeer := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn = gateway
	oldVoice := &VoiceConnection{
		LogLevel: -1, GuildID: "guild", ChannelID: "old-channel", sessionID: "old-session",
		session: session, wsConn: oldSocket, close: make(chan struct{}),
		OpusSend: make(chan []byte), OpusRecv: make(chan *Packet),
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": oldVoice}
	defer oldVoice.Close()
	defer func() { _ = session.Close() }()

	// A real terminal close unregisters the old handle without an application
	// Disconnect call. The application can still retain that handle.
	if err := oldPeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
		t.Fatal(err)
	}
	oldVoice.wsListen(oldSocket, oldVoice.close)
	if session.VoiceConnections["guild"] != nil {
		t.Fatal("terminal close did not unregister the old connection")
	}

	joined := make(chan struct{})
	go func() {
		defer close(joined)
		_, _ = session.ChannelVoiceJoin("guild", "new-channel", false, false)
	}()
	defer func() {
		_ = session.Close()
		<-joined
	}()
	if err := gatewayPeer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var request voiceChannelJoinOp
	if err := gatewayPeer.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "new-channel" {
		t.Fatalf("replacement join request = %+v", request)
	}
	session.RLock()
	replacement := session.VoiceConnections["guild"]
	session.RUnlock()
	if replacement == nil || replacement == oldVoice {
		t.Fatal("public join did not create a replacement")
	}
	if err := oldVoice.Disconnect(); err != nil {
		t.Fatal(err)
	}
	// A subsequent public command marks the end of any stale writes. No
	// negative timing assertion or synthetic replacement installation is needed.
	if err := session.ChannelVoiceJoinManual("other-guild", "marker", false, false); err != nil {
		t.Fatal(err)
	}
	request = voiceChannelJoinOp{}
	if err := gatewayPeer.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Op != 4 || request.Data.GuildID == nil || *request.Data.GuildID != "other-guild" || request.Data.ChannelID == nil || *request.Data.ChannelID != "marker" {
		t.Errorf("old Disconnect sent a voice command ahead of the marker: %+v", request)
	}
	session.RLock()
	registered := session.VoiceConnections["guild"]
	session.RUnlock()
	if registered != replacement {
		t.Error("old Disconnect unregistered the replacement")
	}
	oldVoice.RLock()
	defer oldVoice.RUnlock()
	if !oldVoice.disconnecting || oldVoice.sessionID != "" || oldVoice.OpusSend != nil || oldVoice.OpusRecv != nil {
		t.Error("old Disconnect did not finish its own cleanup")
	}
}
