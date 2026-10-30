package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestThreadMembersUpdateClearsCurrentMember(t *testing.T) {
	for _, source := range []string{"THREAD_CREATE", "GUILD_CREATE"} {
		t.Run(source, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			dispatch("READY", `{"v":10,"user":{"id":"300","bot":true},"guilds":[],"session_id":"test","resume_gateway_url":"wss://gateway.discord.gg"}`)
			member := `{"join_timestamp":"2026-10-30T10:00:00Z","flags":1}`
			if source == "THREAD_CREATE" {
				member = `{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1}`
			}
			thread := `{"id":"200","guild_id":"100","parent_id":"150","type":11,"name":"public-thread","member_count":2,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false},"member":` + member + `}`
			if source == "GUILD_CREATE" {
				// Discord omits both membership IDs in a GUILD_CREATE thread.
				dispatch(source, `{"id":"100","threads":[`+thread+`]}`)
			} else {
				dispatch("GUILD_CREATE", `{"id":"100","threads":[]}`)
				dispatch(source, thread)
			}
			before, err := session.State.Channel("200")
			if err != nil || before.Member == nil {
				t.Fatalf("initial membership missing: %v", err)
			}
			if source == "GUILD_CREATE" && before.Member.UserID != "" {
				t.Fatal("fixture unexpectedly supplied the omitted user ID")
			}
			callbacks := 0
			session.AddHandler(func(s *Session, event *ThreadMembersUpdate) {
				callbacks++
				cached, err := s.State.Channel(event.ID)
				if err != nil {
					t.Fatal(err)
				}
				if event.RemovedMembers[0] == "300" && cached.Member != nil {
					t.Error("removal callback still sees the current user as a thread member")
				}
			})
			dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":1,"removed_member_ids":["400"]}`)
			otherRemoved, err := session.State.Channel("200")
			if err != nil || otherRemoved.Member != before.Member {
				t.Fatalf("another user's removal changed current membership: %v", err)
			}
			dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":0,"removed_member_ids":["300"]}`)
			after, err := session.State.Channel("200")
			if err != nil {
				t.Fatal(err)
			}
			if after.Member != nil || after.MemberCount != 0 {
				t.Errorf("current membership survived removal: member=%+v count=%d", after.Member, after.MemberCount)
			}
			if before.Member == nil || otherRemoved.Member != before.Member || before.MemberCount != 2 || otherRemoved.MemberCount != 1 {
				t.Error("removal modified an earlier thread snapshot")
			}
			guild, err := session.State.Guild("100")
			if err != nil || len(guild.Threads) != 1 || guild.Threads[0] != after {
				t.Fatalf("guild thread cache not updated: %v", err)
			}
			if callbacks != 2 {
				t.Fatalf("removal callbacks = %d, want 2", callbacks)
			}
		})
	}
}
