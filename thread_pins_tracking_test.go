package discordgo

import (
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestThreadPinsUpdateWithoutChannelTracking(t *testing.T) {
	for _, channelType := range []ChannelType{ChannelTypeGuildNewsThread, ChannelTypeGuildPublicThread, ChannelTypeGuildPrivateThread} {
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			session.State.TrackChannels = false
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			dispatch("THREAD_CREATE", fmt.Sprintf(`{"id":"200","guild_id":"100","parent_id":"150","owner_id":"300","type":%d,"name":"thread","last_pin_timestamp":"2026-10-30T10:00:00Z","thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false}}`, channelType))
			var callbackChannel *Channel
			session.AddHandler(func(s *Session, event *ChannelPinsUpdate) {
				callbackChannel, err = s.State.Channel(event.ChannelID)
				if err != nil {
					t.Error(err)
				}
			})
			for _, timestamp := range []string{"2026-10-30T11:00:00Z", ""} {
				before, err := session.State.Channel("200")
				if err != nil {
					t.Fatal(err)
				}
				if before.LastPinTimestamp == nil {
					t.Fatal("expected an existing pin timestamp before the update")
				}
				previousTimestamp := *before.LastPinTimestamp
				value := "null"
				var expected *time.Time
				if timestamp != "" {
					parsed, err := time.Parse(time.RFC3339, timestamp)
					if err != nil {
						t.Fatal(err)
					}
					expected = &parsed
					value = fmt.Sprintf("%q", timestamp)
				}
				dispatch("CHANNEL_PINS_UPDATE", fmt.Sprintf(`{"guild_id":"100","channel_id":"200","last_pin_timestamp":%s}`, value))
				after, err := session.State.Channel("200")
				if err != nil {
					t.Fatal(err)
				}
				if (expected == nil && after.LastPinTimestamp != nil) || (expected != nil && (after.LastPinTimestamp == nil || !after.LastPinTimestamp.Equal(*expected))) {
					t.Errorf("thread pin timestamp = %v, want %v", after.LastPinTimestamp, expected)
				}
				guild, err := session.State.Guild("100")
				if err != nil {
					t.Fatal(err)
				}
				if len(guild.Threads) != 1 || guild.Threads[0] != after || callbackChannel != after || after == before {
					t.Error("thread pin update did not replace both cache indexes before its callback")
				}
				if before.LastPinTimestamp == nil || !before.LastPinTimestamp.Equal(previousTimestamp) || len(guild.Channels) != 0 {
					t.Error("pin update changed the previous snapshot or cached an ordinary channel")
				}
			}
		})
	}
}

func TestThreadPinsUpdatePreservesDisabledTracking(t *testing.T) {
	for _, channelType := range []ChannelType{ChannelTypeGuildText, ChannelTypeGuildPublicThread} {
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
				t.Fatal(err)
			}
			channel := &Channel{ID: "channel", GuildID: "guild", Type: channelType}
			if err := state.ChannelAdd(channel); err != nil {
				t.Fatal(err)
			}
			state.TrackChannels = false
			state.TrackThreads = !channel.IsThread()
			if err := state.OnInterface(&Session{StateEnabled: true}, &ChannelPinsUpdate{ChannelID: "channel", LastPinTimestamp: "2026-10-30T11:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			after, err := state.Channel("channel")
			if err != nil || after != channel || after.LastPinTimestamp != nil {
				t.Errorf("pin update changed an untracked channel: %v", err)
			}
		})
	}
}
