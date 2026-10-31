package discordgo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGatewayCreatesUpdateChannelLastMessage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		channelType   ChannelType
		threadEvent   bool
		newThread     bool
		trackChannels bool
		trackThreads  bool
		stateEnabled  bool
		wantUpdate    bool
	}{
		{"text message", ChannelTypeGuildText, false, false, true, true, true, true},
		{"thread message", ChannelTypeGuildPublicThread, false, false, false, true, true, true},
		{"DM message", ChannelTypeDM, false, false, true, false, true, true},
		{"forum post without thread cache", ChannelTypeGuildForum, true, true, true, false, true, true},
		{"media post", ChannelTypeGuildMedia, true, true, true, true, true, true},
		{"existing forum thread", ChannelTypeGuildForum, true, false, true, true, true, false},
		{"ordinary parent thread", ChannelTypeGuildText, true, true, true, true, true, false},
		{"untracked channel", ChannelTypeGuildText, false, false, false, true, true, false},
		{"untracked thread", ChannelTypeGuildPublicThread, false, false, true, false, true, false},
		{"disabled state", ChannelTypeGuildText, false, false, true, true, false, false},
	} {
		for _, maxMessages := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/messages=%d", tc.name, maxMessages), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = tc.stateEnabled
				session.State.MaxMessageCount = maxMessages
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				channel := &Channel{ID: "300", GuildID: "100", Type: tc.channelType, LastMessageID: "400"}
				if tc.channelType == ChannelTypeDM {
					channel.GuildID = ""
				}
				if err := session.State.ChannelAdd(channel); err != nil {
					t.Fatal(err)
				}
				session.State.TrackChannels = tc.trackChannels
				session.State.TrackThreads = tc.trackThreads
				before, err := session.State.Channel("300")
				if err != nil {
					t.Fatal(err)
				}
				var callbackChannel *Channel
				capture := func() {
					callbackChannel, err = session.State.Channel("300")
					if err != nil {
						t.Error(err)
					}
				}
				session.AddHandler(func(_ *Session, _ *MessageCreate) { capture() })
				session.AddHandler(func(_ *Session, _ *ThreadCreate) { capture() })
				eventType := "MESSAGE_CREATE"
				payload := `{"id":"500","channel_id":"300","author":{"id":"200"},"content":"new message","timestamp":"2026-10-31T00:00:00Z","type":0}`
				if channel.GuildID != "" {
					payload = fmt.Sprintf(`{"guild_id":%q,`, channel.GuildID) + payload[1:]
				}
				if tc.threadEvent {
					eventType = "THREAD_CREATE"
					payload = fmt.Sprintf(`{"id":"500","guild_id":"100","parent_id":"300","type":11,"name":"post","newly_created":%t,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-31T00:00:00Z","locked":false}}`, tc.newThread)
				}
				if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":%q,"d":%s}`, eventType, payload))); err != nil {
					t.Fatal(err)
				}
				after, err := session.State.Channel("300")
				if err != nil {
					t.Fatal(err)
				}
				want := "400"
				if tc.wantUpdate {
					want = "500"
				}
				if after.LastMessageID != want || callbackChannel == nil || callbackChannel.LastMessageID != want {
					t.Errorf("last message = %q, want %q before callback", after.LastMessageID, want)
				}
				if before.LastMessageID != "400" {
					t.Error("create event mutated a retained channel snapshot")
				}
				var indexed *Channel
				if tc.channelType == ChannelTypeDM {
					indexed = session.State.PrivateChannels[0]
				} else {
					guild, err := session.State.Guild("100")
					if err != nil {
						t.Fatal(err)
					}
					if channel.IsThread() {
						indexed = guild.Threads[0]
					} else {
						indexed = guild.Channels[0]
					}
				}
				if indexed != after || callbackChannel != after {
					t.Error("channel cache indexes and callback disagree")
				}
				if maxMessages == 0 && len(after.Messages) != 0 {
					t.Error("channel metadata update retained message bodies with caching disabled")
				}
			})
		}
	}
}

func TestChannelLastMessagePreservesNonCreationAndErrorBehavior(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 2
	if err := state.ChannelAdd(&Channel{ID: "300", Type: ChannelTypeDM, LastMessageID: "500"}); err != nil {
		t.Fatal(err)
	}
	// Explicit cache insertion can contain historical messages.
	if err := state.MessageAdd(&Message{ID: "400", ChannelID: "300"}); err != nil {
		t.Fatal(err)
	}
	session := &Session{StateEnabled: true}
	for _, event := range []interface{}{
		&MessageUpdate{Message: &Message{ID: "400", ChannelID: "300", Content: "edited"}},
		&MessageDelete{Message: &Message{ID: "500", ChannelID: "300"}},
		&MessageCreate{Message: &Message{ChannelID: "300"}},
	} {
		_ = state.OnInterface(session, event)
		channel, err := state.Channel("300")
		if err != nil || channel.LastMessageID != "500" {
			t.Fatalf("%T changed last message: %v", event, err)
		}
	}
	state.MaxMessageCount = -1
	before, _ := state.Channel("300")
	err := state.OnInterface(session, &MessageCreate{Message: &Message{ID: "600", ChannelID: "300"}})
	if !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("negative message limit error = %v", err)
	}
	after, _ := state.Channel("300")
	if after != before {
		t.Fatal("rejected message create changed the channel snapshot")
	}
}
