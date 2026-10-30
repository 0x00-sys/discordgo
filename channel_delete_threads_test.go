package discordgo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestChannelDeleteEvictsChildThreads(t *testing.T) {
	for _, trackChannels := range []bool{true, false} {
		for _, kind := range []ChannelType{ChannelTypeGuildText, ChannelTypeGuildNews, ChannelTypeGuildForum, ChannelTypeGuildMedia} {
			t.Run(fmt.Sprintf("type=%d/trackChannels=%t", kind, trackChannels), func(t *testing.T) {
				session, err := New("")
				if err != nil {
					t.Fatal(err)
				}
				state := session.State
				state.TrackChannels = trackChannels
				parent := &Channel{ID: "parent", GuildID: "guild", Type: kind}
				child := &Channel{ID: "child", GuildID: "guild", ParentID: parent.ID, Type: ChannelTypeGuildPublicThread,
					Messages: []*Message{{ID: "message", Content: "retained message"}}}
				sibling := &Channel{ID: "sibling", GuildID: "guild", ParentID: "other", Type: ChannelTypeGuildPublicThread}
				if err := state.GuildAdd(&Guild{ID: "guild", Channels: []*Channel{parent}, Threads: []*Channel{child, sibling}}); err != nil {
					t.Fatal(err)
				}
				before, err := state.Guild("guild")
				if err != nil {
					t.Fatal(err)
				}
				payload := fmt.Sprintf(`{"op":0,"t":"CHANNEL_DELETE","s":1,"d":{"id":"parent","guild_id":"guild","type":%d}}`, kind)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{parent.ID, child.ID} {
					if _, err := state.Channel(id); !errors.Is(err, ErrStateNotFound) {
						t.Errorf("Channel(%q) error = %v, want ErrStateNotFound", id, err)
					}
				}
				after, err := state.Guild("guild")
				if err != nil {
					t.Fatal(err)
				}
				if len(after.Threads) != 1 || after.Threads[0] != sibling {
					t.Errorf("remaining threads = %v, want only sibling", after.Threads)
				}
				if len(before.Threads) != 2 || before.Threads[0] != child {
					t.Error("old guild snapshot was mutated")
				}
				if _, err := state.Message(child.ID, "message"); !errors.Is(err, ErrStateNotFound) {
					t.Errorf("deleted child's messages remain cached: %v", err)
				}
			})
		}
	}
}
