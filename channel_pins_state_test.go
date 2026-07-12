package discordgo

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestChannelPinsUpdateState(t *testing.T) {
	oldTimestamp := time.Date(2026, time.July, 12, 8, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{{
			ID:               "guild-channel",
			GuildID:          "guild",
			Type:             ChannelTypeGuildText,
			LastPinTimestamp: &oldTimestamp,
		}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.ChannelAdd(&Channel{
		ID:               "dm-channel",
		Type:             ChannelTypeDM,
		LastPinTimestamp: &oldTimestamp,
	}); err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	session := &Session{StateEnabled: true}
	wantTimestamp := time.Date(2026, time.July, 13, 9, 10, 11, 123000000, time.UTC)
	t.Run("guild channel timestamp", func(t *testing.T) {
		beforeGuild, err := state.Guild("guild")
		if err != nil {
			t.Fatalf("Guild returned error: %v", err)
		}
		beforeChannel, err := state.Channel("guild-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}

		var event ChannelPinsUpdate
		if err := json.Unmarshal([]byte(`{"channel_id":"guild-channel","guild_id":"guild","last_pin_timestamp":"2026-07-13T09:10:11.123Z"}`), &event); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		if err := state.OnInterface(session, &event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}

		channel, err := state.Channel("guild-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}
		guild, err := state.Guild("guild")
		if err != nil {
			t.Fatalf("Guild returned error: %v", err)
		}
		if channel.LastPinTimestamp == nil || !channel.LastPinTimestamp.Equal(wantTimestamp) {
			t.Fatalf("LastPinTimestamp = %v, want %v", channel.LastPinTimestamp, wantTimestamp)
		}
		if event.LastPinTimestamp != "2026-07-13T09:10:11.123Z" {
			t.Fatalf("event LastPinTimestamp = %q, want original value", event.LastPinTimestamp)
		}
		if guild == beforeGuild || channel == beforeChannel || guild.Channels[0] != channel {
			t.Fatal("channel pins update did not replace the guild channel snapshot")
		}
		if beforeChannel.LastPinTimestamp == nil || !beforeChannel.LastPinTimestamp.Equal(oldTimestamp) {
			t.Fatal("channel pins update mutated a held channel snapshot")
		}
	})

	t.Run("null clears guild timestamp", func(t *testing.T) {
		var event ChannelPinsUpdate
		if err := json.Unmarshal([]byte(`{"channel_id":"guild-channel","guild_id":"guild","last_pin_timestamp":null}`), &event); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		if err := state.OnInterface(session, &event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
		channel, err := state.Channel("guild-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}
		if channel.LastPinTimestamp != nil {
			t.Fatalf("LastPinTimestamp = %v, want nil", channel.LastPinTimestamp)
		}
		if event.LastPinTimestamp != "" {
			t.Fatalf("event LastPinTimestamp = %q, want empty null representation", event.LastPinTimestamp)
		}
	})

	t.Run("private channel timestamp", func(t *testing.T) {
		before := state.PrivateChannels[0]
		event := &ChannelPinsUpdate{
			ChannelID:        "dm-channel",
			LastPinTimestamp: "2026-07-13T09:10:11.123Z",
		}
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
		channel, err := state.Channel("dm-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}
		if channel.LastPinTimestamp == nil || !channel.LastPinTimestamp.Equal(wantTimestamp) {
			t.Fatalf("LastPinTimestamp = %v, want %v", channel.LastPinTimestamp, wantTimestamp)
		}
		if channel == before || state.PrivateChannels[0] != channel {
			t.Fatal("channel pins update did not replace the private channel snapshot")
		}
	})
}

func TestChannelPinsUpdateStateErrors(t *testing.T) {
	state := NewState()
	oldTimestamp := time.Date(2026, time.July, 12, 8, 0, 0, 0, time.UTC)
	if err := state.ChannelAdd(&Channel{ID: "dm", Type: ChannelTypeDM, LastPinTimestamp: &oldTimestamp}); err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	if err := state.OnInterface(session, &ChannelPinsUpdate{ChannelID: "missing"}); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("missing channel returned error %v, want %v", err, ErrStateNotFound)
	}
	for _, event := range []*ChannelPinsUpdate{nil, &ChannelPinsUpdate{}} {
		if err := state.OnInterface(session, event); !errors.Is(err, ErrStateInvalidData) {
			t.Fatalf("invalid event returned error %v, want %v", err, ErrStateInvalidData)
		}
	}

	before, err := state.Channel("dm")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	event := &ChannelPinsUpdate{ChannelID: "dm", LastPinTimestamp: "not-a-timestamp"}
	if err := state.OnInterface(session, event); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("malformed timestamp returned error %v, want %v", err, ErrStateInvalidData)
	}
	after, err := state.Channel("dm")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if after != before || after.LastPinTimestamp == nil || !after.LastPinTimestamp.Equal(oldTimestamp) {
		t.Fatal("malformed timestamp changed cached channel state")
	}
	if event.LastPinTimestamp != "not-a-timestamp" {
		t.Fatal("malformed timestamp event was mutated")
	}
}

func TestChannelPinsUpdateStateConcurrentAccess(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild", Type: ChannelTypeGuildText}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	var readers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for j := 0; j < 1000; j++ {
				channel, err := state.Channel("channel")
				if err == nil && channel.LastPinTimestamp != nil {
					_ = channel.LastPinTimestamp.UnixNano()
				}
				_, _ = state.Guild("guild")
			}
		}()
	}
	close(start)
	for i := 0; i < 1000; i++ {
		timestamp := ""
		if i%2 == 0 {
			timestamp = "2026-07-13T09:10:11.123Z"
		}
		if err := state.OnInterface(session, &ChannelPinsUpdate{
			ChannelID:        "channel",
			GuildID:          "guild",
			LastPinTimestamp: timestamp,
		}); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}
	readers.Wait()
}
