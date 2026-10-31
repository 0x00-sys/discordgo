package discordgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type voiceChangeStateBufferPool struct {
	once    sync.Once
	written chan struct{}
	release chan struct{}
}

func (*voiceChangeStateBufferPool) Get() interface{} { return nil }
func (p *voiceChangeStateBufferPool) Put(interface{}) {
	p.once.Do(func() {
		close(p.written)
		<-p.release
	})
}

func TestVoiceChangeChannelPreservesReceivedState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		channels []string
		userID   string
		want     string
	}{
		{name: "newer_move", channels: []string{"20", "30"}, userID: "200", want: "30"},
		{name: "move_back", channels: []string{"20", "10"}, userID: "200", want: "10"},
		{name: "request_ack", channels: []string{"20"}, userID: "200", want: "20"},
		{name: "other_user", channels: []string{"30"}, userID: "201", want: "20"},
		{name: "no_event", want: "20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			pool := &voiceChangeStateBufferPool{written: make(chan struct{}), release: make(chan struct{})}
			dialer := websocket.Dialer{WriteBufferPool: pool}
			conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			peer := <-peerReady
			defer func() { _ = conn.Close(); _ = peer.Close() }()
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			session.ShouldReconnectOnError = false
			session.wsConn, session.listening = conn, make(chan interface{})
			session.State.User = &User{ID: "200"}
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			voice := &VoiceConnection{LogLevel: -1, session: session, GuildID: "100", ChannelID: "10"}
			session.VoiceConnections = map[string]*VoiceConnection{"100": voice}
			dispatched := make(chan *VoiceStateUpdate, 2)
			session.AddHandler(func(_ *Session, update *VoiceStateUpdate) { dispatched <- update })
			listenerDone := make(chan struct{})
			listening := session.listening
			go func() { defer close(listenerDone); session.listen(conn, listening) }()
			changeDone := make(chan struct{})
			var changeErr error
			go func() { defer close(changeDone); changeErr = voice.ChangeChannel("20", true, true) }()
			defer func() {
				closeChannel(pool.release)
				<-changeDone
				_ = session.Close()
				<-listenerDone
			}()
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var request voiceChannelJoinOp
			if err := peer.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "20" || !request.Data.SelfMute || !request.Data.SelfDeaf {
				t.Fatalf("channel change request = %+v", request)
			}
			// The frame is already on the wire, but buffer recycling delays the
			// caller's local commit while the real gateway reader receives events.
			select {
			case <-pool.written:
			case <-time.After(3 * time.Second):
				t.Fatal("channel change did not reach buffer recycling")
			}
			for i, channelID := range tc.channels {
				if err := peer.WriteJSON(map[string]interface{}{
					"op": 0, "s": i + 1, "t": "VOICE_STATE_UPDATE",
					"d": &VoiceState{GuildID: "100", ChannelID: channelID, UserID: tc.userID, SessionID: "voice-session"},
				}); err != nil {
					t.Fatal(err)
				}
				select {
				case update := <-dispatched:
					if update.ChannelID != channelID || update.UserID != tc.userID {
						t.Fatalf("unexpected voice-state callback: %+v", update)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("gateway reader did not dispatch the voice state")
				}
			}
			if tc.userID == "200" {
				voice.RLock()
				receivedChannel := voice.ChannelID
				voice.RUnlock()
				if receivedChannel != tc.want {
					t.Fatalf("voice-state handler selected %s before local commit, want %s", receivedChannel, tc.want)
				}
			}
			close(pool.release)
			select {
			case <-changeDone:
			case <-time.After(3 * time.Second):
				t.Fatal("channel change did not finish")
			}
			if changeErr != nil {
				t.Fatal(changeErr)
			}
			voice.RLock()
			channelID, mute, deaf := voice.ChannelID, voice.mute, voice.deaf
			voice.RUnlock()
			if channelID != tc.want || !mute || !deaf {
				t.Errorf("channel/mute/deaf = %s/%t/%t, want %s/true/true", channelID, mute, deaf, tc.want)
			}
			if tc.userID == "200" {
				cached, err := session.State.VoiceState("100", "200")
				if err != nil || cached.ChannelID != tc.want {
					t.Errorf("cached voice state does not match received channel: %+v, %v", cached, err)
				}
			}
		})
	}
}
