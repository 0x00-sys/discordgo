package discordgo

import (
	"strings"
	"testing"
	"time"
)

func TestVoiceJoinDoesNotSucceedForNewerChannel(t *testing.T) {
	for _, tc := range []struct {
		name          string
		channelID     string
		changeChannel bool
	}{
		{name: "newer_join", channelID: "second-channel"},
		{name: "channel_change", channelID: "second-channel", changeChannel: true},
		{name: "same_channel", channelID: "first-channel"},
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
			voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			firstDone := make(chan struct{})
			var secondDone chan struct{}
			var firstVoice, secondVoice *VoiceConnection
			var firstErr, secondErr error
			go func() {
				defer close(firstDone)
				firstVoice, firstErr = session.ChannelVoiceJoin("guild", "first-channel", false, false)
			}()
			defer func() {
				_ = session.Close()
				<-firstDone
				if secondDone != nil {
					<-secondDone
				}
			}()
			readJoin := func(channelID string) {
				t.Helper()
				if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var request voiceChannelJoinOp
				if err := peer.ReadJSON(&request); err != nil {
					t.Fatal(err)
				}
				if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != channelID {
					t.Fatalf("unexpected voice request: %+v", request)
				}
			}
			readJoin("first-channel")
			// Withhold readiness until a later public request selects its channel.
			secondDone = make(chan struct{})
			go func() {
				defer close(secondDone)
				if tc.changeChannel {
					secondVoice = voice
					secondErr = voice.ChangeChannel(tc.channelID, false, false)
				} else {
					secondVoice, secondErr = session.ChannelVoiceJoin("guild", tc.channelID, false, false)
				}
			}()
			readJoin(tc.channelID)
			if tc.changeChannel {
				select {
				case <-secondDone:
				case <-time.After(3 * time.Second):
					t.Fatal("channel change did not finish")
				}
			}
			// Model only the later request's completed transport. Neither request
			// was ready before this point; no full voice handshake is simulated.
			backend := &fakeVoiceDAVESession{maxVersion: 1}
			installFakeDAVE(voice, backend, 1)
			transportClose := make(chan struct{})
			voice.Lock()
			voice.wsConn, voice.close, voice.Ready = transport, transportClose, true
			voice.Unlock()
			select {
			case <-secondDone:
			case <-time.After(3 * time.Second):
				t.Fatal("newer request did not finish")
			}
			if secondErr != nil || secondVoice != voice {
				t.Fatalf("newer request = %p, %v; want the registered voice and success", secondVoice, secondErr)
			}
			select {
			case <-firstDone:
			case <-time.After(13 * time.Second):
				t.Fatal("first join did not finish within its readiness timeout")
			}
			if firstVoice != voice {
				t.Error("first join replaced the registered voice handle")
			}
			if tc.channelID == "first-channel" {
				if firstErr != nil {
					t.Errorf("same-channel join failed: %v", firstErr)
				}
			} else if firstErr == nil || !strings.Contains(firstErr.Error(), "timeout waiting for voice") {
				t.Errorf("first join returned %v with only %s ready, want readiness timeout", firstErr, tc.channelID)
			}
			voice.RLock()
			preserved := voice.wsConn == transport && voice.close == transportClose && voice.Ready && voice.ChannelID == tc.channelID
			voice.RUnlock()
			backend.mu.Lock()
			closed := backend.closed
			backend.mu.Unlock()
			if !preserved || closed != 0 {
				t.Errorf("first join disturbed newer transport: preserved=%t, backend closes=%d", preserved, closed)
			}
		})
	}
}
