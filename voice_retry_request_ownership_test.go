package discordgo

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceRetryPreservesNewerRequest(t *testing.T) {
	for _, changeChannel := range []bool{false, true} {
		name := "join"
		if changeChannel {
			name = "change_channel"
		}
		t.Run(name, func(t *testing.T) {
			gateway, peer := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn, session.DataReady = gateway, true
			voice := &VoiceConnection{
				LogLevel: LogInformational, GuildID: "guild", ChannelID: "old-channel", session: session,
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var pauseOnce atomic.Bool
			oldLogger := Logger
			Logger = func(_, _ int, format string, _ ...interface{}) {
				if format == "trying to reconnect to channel %s" && pauseOnce.CompareAndSwap(false, true) {
					close(paused)
					<-release
				}
			}
			defer func() { Logger = oldLogger }()
			var requestDone chan struct{}
			go func() {
				defer close(done)
				voice.reconnectWithResume(false, nil, nil)
			}()
			defer func() {
				closeChannel(release)
				_ = session.Close()
				<-done
				if requestDone != nil {
					<-requestDone
				}
			}()
			select {
			case <-paused:
			case <-time.After(3 * time.Second):
				t.Fatal("recovery did not reach its join attempt")
			}
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
			requestDone = make(chan struct{})
			var requestErr error
			var joined *VoiceConnection
			go func() {
				defer close(requestDone)
				if changeChannel {
					requestErr = voice.ChangeChannel("new-channel", true, true)
				} else {
					joined, requestErr = session.ChannelVoiceJoin("guild", "new-channel", true, true)
				}
			}()
			update := readUpdate()
			if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "new-channel" || !update.Data.SelfMute || !update.Data.SelfDeaf {
				t.Fatalf("newer request = %+v, want new channel and settings", update)
			}
			// Model readiness after the newer public request's actual gateway write.
			voice.Lock()
			voice.Ready = true
			voice.Unlock()
			select {
			case <-requestDone:
			case <-time.After(3 * time.Second):
				t.Fatal("newer request did not finish")
			}
			if requestErr != nil || (!changeChannel && joined != voice) {
				t.Fatalf("newer request = %p, %v; want same handle and success", joined, requestErr)
			}
			voice.RLock()
			version := voice.joinVersion
			voice.RUnlock()
			close(release)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("obsolete recovery did not finish")
			}
			// Every recovery command precedes this marker; no reader scheduling
			// or manual cleanup can hide an unwanted retry.
			if err := session.ChannelVoiceJoinManual("marker", "marker", false, false); err != nil {
				t.Fatal(err)
			}
			update = readUpdate()
			if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "marker" || update.Data.ChannelID == nil || *update.Data.ChannelID != "marker" {
				channelID := "<nil>"
				if update.Data.ChannelID != nil {
					channelID = *update.Data.ChannelID
				}
				t.Errorf("obsolete recovery sent OP%d channel=%q mute=%t deaf=%t before the marker", update.Op, channelID, update.Data.SelfMute, update.Data.SelfDeaf)
			}
			voice.RLock()
			preserved := voice.ChannelID == "new-channel" && voice.mute && voice.deaf && voice.joinVersion == version && voice.Ready && !voice.reconnecting
			voice.RUnlock()
			if !preserved {
				t.Error("obsolete recovery overwrote newer voice settings")
			}
		})
	}
}
