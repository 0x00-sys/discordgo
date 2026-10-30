package discordgo

import (
	"strings"
	"testing"
	"time"
)

func TestVoiceJoinDoesNotReuseOtherChannelReadiness(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelID   string
		replacement bool
	}{
		{name: "same channel", channelID: "old-channel"},
		{name: "other channel without handshake", channelID: "new-channel"},
		{name: "other channel with new transport", channelID: "new-channel", replacement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, peer := newGatewayTestConnection(t)
			transport, _ := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn = gateway
			voice := &VoiceConnection{
				LogLevel: -1, GuildID: "guild", ChannelID: "old-channel", session: session,
				Ready: true, wsConn: transport, close: make(chan struct{}),
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			done := make(chan struct{})
			var joined *VoiceConnection
			var joinErr error
			go func() {
				defer close(done)
				joined, joinErr = session.ChannelVoiceJoin("guild", tc.channelID, false, false)
			}()
			defer func() {
				_ = session.Close()
				<-done
			}()
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var request voiceChannelJoinOp
			if err := peer.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != tc.channelID {
				t.Fatalf("unexpected join request: %+v", request)
			}
			if tc.replacement {
				// Model the later voice handshake replacing the old transport.
				voice.Close()
				replacement, _ := newGatewayTestConnection(t)
				voice.Lock()
				voice.wsConn, voice.close, voice.Ready = replacement, make(chan struct{}), true
				voice.Unlock()
			}
			// Discord sends no voice events when the requested channel is full.
			// Withhold them here: only the old channel's transport is ready.
			select {
			case <-done:
			case <-time.After(13 * time.Second):
				t.Fatal("join did not complete within its readiness timeout")
			}
			if joined != voice {
				t.Fatal("join unexpectedly replaced the registered voice handle")
			}
			if tc.channelID == "old-channel" || tc.replacement {
				if joinErr != nil {
					t.Fatalf("join to the ready channel failed: %v", joinErr)
				}
			} else if joinErr == nil || !strings.Contains(joinErr.Error(), "timeout waiting for voice") {
				t.Fatalf("join to another channel returned %v without any new voice events, want readiness timeout", joinErr)
			}
		})
	}
}
