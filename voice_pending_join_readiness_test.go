package discordgo

import (
	"strings"
	"testing"
	"time"
)

func TestVoiceJoinPreservesPendingReadiness(t *testing.T) {
	for _, changeChannel := range []bool{false, true} {
		name := "join"
		if changeChannel {
			name = "change_channel"
		}
		t.Run(name, func(t *testing.T) {
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
			firstDone := make(chan struct{})
			var firstErr error
			go func() {
				defer close(firstDone)
				if changeChannel {
					firstErr = voice.ChangeChannel("new-channel", false, false)
				} else {
					_, firstErr = session.ChannelVoiceJoin("guild", "new-channel", false, false)
				}
			}()
			var secondDone chan struct{}
			defer func() {
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
				var request voiceChannelJoinOp
				if err := peer.ReadJSON(&request); err != nil {
					t.Fatal(err)
				}
				if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "new-channel" {
					t.Fatalf("unexpected join request: %+v", request)
				}
			}
			readJoin()
			if changeChannel {
				select {
				case <-firstDone:
				case <-time.After(3 * time.Second):
					t.Fatal("ChangeChannel did not finish its send")
				}
				if firstErr != nil {
					t.Fatal(firstErr)
				}
			}
			secondDone = make(chan struct{})
			var joined *VoiceConnection
			var secondErr error
			go func() {
				defer close(secondDone)
				joined, secondErr = session.ChannelVoiceJoin("guild", "new-channel", false, false)
			}()
			readJoin()
			// Neither request receives voice events; the transport still
			// belongs to old-channel even though ChannelID now says new-channel.
			select {
			case <-secondDone:
			case <-time.After(13 * time.Second):
				t.Fatal("second join did not finish within its readiness timeout")
			}
			if joined != voice {
				t.Fatal("second join unexpectedly replaced the registered handle")
			}
			if secondErr == nil || !strings.Contains(secondErr.Error(), "timeout waiting for voice") {
				t.Fatalf("second join returned %v without a new transport, want readiness timeout", secondErr)
			}
		})
	}
}
