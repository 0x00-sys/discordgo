package discordgo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestThreadArchiveEvictsCachedState(t *testing.T) {
	for _, trackChannels := range []bool{true, false} {
		t.Run(fmt.Sprintf("trackChannels=%t", trackChannels), func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			state := session.State
			state.TrackChannels = trackChannels
			state.MaxMessageCount = 2
			parent := &Channel{ID: "150", GuildID: "100", Type: ChannelTypeGuildText}
			active := &Channel{ID: "199", GuildID: "100", ParentID: parent.ID, Type: ChannelTypeGuildPublicThread, Name: "active"}
			if err := state.GuildAdd(&Guild{ID: "100", Channels: []*Channel{parent}, Threads: []*Channel{active}}); err != nil {
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
			threadData := func(id, name string, archived bool) string {
				return fmt.Sprintf(`{"id":%q,"guild_id":"100","parent_id":"150","owner_id":"300","type":11,"name":%q,"thread_metadata":{"archived":%t,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false}}`, id, name, archived)
			}
			var snapshot *Channel
			archiveCallbacks, cachedDuringCallbacks := 0, 0
			session.AddHandler(func(_ *Session, event *ThreadUpdate) {
				if !event.ThreadMetadata.Archived {
					return
				}
				archiveCallbacks++
				if _, err := state.Channel(event.ID); err == nil {
					cachedDuringCallbacks++
				}
				if event.ID == "900" {
					if event.BeforeUpdate != nil {
						t.Error("unknown archived thread had a previous snapshot")
					}
					return
				}
				if event.BeforeUpdate == nil || event.BeforeUpdate.ThreadMetadata.Archived || len(event.BeforeUpdate.Messages) != 1 {
					t.Error("archive callback lost the previous active thread and its message")
				}
				if event.ID == "200" {
					snapshot = event.BeforeUpdate
				}
			})
			for i := 0; i < 16; i++ {
				id := fmt.Sprint(200 + i)
				dispatch("THREAD_CREATE", threadData(id, "created", false))
				dispatch("MESSAGE_CREATE", fmt.Sprintf(`{"id":%q,"channel_id":%q,"guild_id":"100","content":"cached message","author":{"id":"300"}}`, "message-"+id, id))
				dispatch("THREAD_UPDATE", threadData(id, "archived", true))
			}
			// An update for an uncached archived thread must not add a cache entry.
			dispatch("THREAD_UPDATE", threadData("900", "already archived", true))
			guild, err := state.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			if len(guild.Threads) != 1 || guild.Threads[0] != active {
				t.Errorf("cache retains %d threads after 16 archive cycles and an uncached archive update, want only the active thread", len(guild.Threads))
			}
			wantChannels := 1
			if trackChannels {
				wantChannels++
			}
			if len(state.channelMap) != wantChannels {
				t.Errorf("channel index retains %d entries, want %d", len(state.channelMap), wantChannels)
			}
			if archiveCallbacks != 17 || cachedDuringCallbacks != 0 {
				t.Errorf("archive callbacks=%d, callbacks with retained cache=%d", archiveCallbacks, cachedDuringCallbacks)
			}
			if _, err := state.Message("200", "message-200"); !errors.Is(err, ErrStateNotFound) {
				t.Errorf("archived message lookup error = %v, want ErrStateNotFound", err)
			}
			if snapshot == nil || snapshot.Name != "created" || snapshot.ThreadMetadata.Archived || len(snapshot.Messages) != 1 {
				t.Fatal("previous snapshot was mutated by archiving")
			}
			// Active updates still refresh the cache, and unarchiving can re-add a thread.
			dispatch("THREAD_UPDATE", threadData("199", "renamed", false))
			dispatch("THREAD_CREATE", threadData("200", "reopened", false))
			current, err := state.Channel("199")
			if err != nil || current.Name != "renamed" || active.Name != "active" {
				t.Fatalf("active update did not preserve snapshot semantics: %v", err)
			}
			reopened, err := state.Channel("200")
			if err != nil || reopened.Name != "reopened" || len(reopened.Messages) != 0 {
				t.Fatalf("reopened thread retained archived messages: %v", err)
			}
		})
	}
}
