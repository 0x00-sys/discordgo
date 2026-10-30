package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestThreadUpdatePreservesCachedMembership(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
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
	dispatch("THREAD_CREATE", channelWithThreadMember)
	dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":2,"added_members":[{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1,"member":{"user":{"id":"300"},"roles":[],"joined_at":"2026-10-30T09:00:00Z","deaf":false,"mute":false},"presence":null},{"id":"200","user_id":"400","join_timestamp":"2026-10-30T10:01:00Z","flags":0,"member":{"user":{"id":"400"},"roles":[],"joined_at":"2026-10-30T09:00:00Z","deaf":false,"mute":false},"presence":null}]}`)
	before, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	if before.Member == nil || len(before.Members) != 2 {
		t.Fatal("membership setup failed")
	}
	var received *ThreadUpdate
	session.AddHandler(func(_ *Session, event *ThreadUpdate) { received = event })
	dispatch("THREAD_UPDATE", `{"id":"200","guild_id":"100","parent_id":"150","owner_id":"300","type":12,"name":"renamed","member_count":2,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false,"invitable":true}}`)
	after, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	if after.Member != before.Member {
		t.Error("thread rename discarded the current user's membership")
	}
	if len(after.Members) != 2 || after.Members[0] != before.Members[0] || after.Members[1] != before.Members[1] {
		t.Error("thread rename discarded tracked members")
	}
	if after == before || after.Name != "renamed" || before.Name != "private-thread" {
		t.Fatal("thread rename did not create an updated cache snapshot")
	}
	if received == nil || received.BeforeUpdate == nil || received.BeforeUpdate.Member != before.Member || len(received.BeforeUpdate.Members) != 2 {
		t.Fatal("thread update lost the previous membership snapshot")
	}
	if received.Member != nil || received.Members != nil {
		t.Fatal("cache preservation modified the incoming event")
	}
	guild, err := session.State.Guild("100")
	if err != nil || len(guild.Threads) != 1 || guild.Threads[0] != after {
		t.Fatalf("guild thread cache not updated: %v", err)
	}

	// Later membership changes must still apply without changing old snapshots.
	membersAfterRename := len(after.Members)
	dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":1,"removed_member_ids":["400"]}`)
	current, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Members) != 1 || current.Members[0].UserID != "300" {
		t.Error("membership removal lost the remaining tracked member")
	}
	if len(after.Members) != membersAfterRename || len(before.Members) != 2 || before.Members[1].UserID != "400" {
		t.Error("membership removal changed an earlier cache snapshot")
	}
}

func TestThreadChannelUpdateDoesNotRestoreReplacedMembership(t *testing.T) {
	for _, name := range []string{"tracking disabled", "obfuscated", "explicit replacement"} {
		t.Run(name, func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{ID: "guild", Threads: []*Channel{{
				ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread,
				Member: &ThreadMember{UserID: "self"}, Members: []*ThreadMember{{UserID: "other"}},
			}}}); err != nil {
				t.Fatal(err)
			}
			update := &Channel{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread}
			switch name {
			case "tracking disabled":
				state.TrackThreadMembers = false
			case "obfuscated":
				update.Flags = ChannelFlagObfuscated
			case "explicit replacement":
				update.Member = &ThreadMember{UserID: "self", Flags: 7}
				update.Members = []*ThreadMember{}
			}
			if err := state.ChannelAdd(update); err != nil {
				t.Fatal(err)
			}
			cached, err := state.Channel("thread")
			if err != nil {
				t.Fatal(err)
			}
			if cached.Member != update.Member || len(cached.Members) != 0 {
				t.Fatal("channel update restored obsolete membership")
			}
		})
	}
}
