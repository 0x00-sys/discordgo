package discordgo

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceTerminalCloseCancelsJoinWait(t *testing.T) {
	for _, beforeWait := range []bool{false, true} {
		name := "waiting"
		if beforeWait {
			name = "before wait initialization"
		}
		t.Run(name, func(t *testing.T) {
			gateway, gatewayPeer := newGatewayTestConnection(t)
			voiceSocket, voicePeer := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn = gateway
			voice := &VoiceConnection{
				LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
				wsConn: voiceSocket, close: make(chan struct{}),
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			paused, release := make(chan struct{}), make(chan struct{})
			oldLogger := Logger
			if beforeWait {
				voice.LogLevel = LogInformational
				var once atomic.Bool
				Logger = func(_, _ int, format string, _ ...interface{}) {
					if format == "called" && once.CompareAndSwap(false, true) {
						close(paused)
						<-release
					}
				}
			}
			defer func() { Logger = oldLogger }()
			joined := make(chan struct{})
			var joinErr error
			go func() {
				defer close(joined)
				_, joinErr = session.ChannelVoiceJoin("guild", "channel", false, false)
			}()
			defer func() {
				closeChannel(release)
				_ = voice.Disconnect()
				_ = session.Close()
				<-joined
			}()
			if err := gatewayPeer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var request voiceChannelJoinOp
			if err := gatewayPeer.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "channel" {
				t.Fatalf("unexpected join request: %+v", request)
			}
			if beforeWait {
				select {
				case <-paused:
				case <-time.After(time.Second):
					t.Fatal("join did not reach the readiness wait")
				}
			} else {
				deadline := time.Now().Add(time.Second)
				for {
					voice.RLock()
					initialized := voice.disconnectCancel != nil
					voice.RUnlock()
					if initialized {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("join did not initialize cancellation")
					}
					time.Sleep(time.Millisecond)
				}
			}
			// A terminal voice close must end the pending public join, even
			// when the wait has not yet installed its cancellation channel.
			if err := voicePeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
				t.Fatal(err)
			}
			voice.wsListen(voiceSocket, voice.close)
			close(release)
			select {
			case <-joined:
				if !errors.Is(joinErr, ErrWSNotFound) {
					t.Errorf("join error = %v, want ErrWSNotFound", joinErr)
				}
			case <-time.After(200 * time.Millisecond):
				t.Error("terminally closed voice left its public join waiting")
			}
		})
	}
}
