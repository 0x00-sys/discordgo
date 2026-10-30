package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGuildMemberAddCountsOnlyNewCachedMembers(t *testing.T) {
	for _, initiallyCached := range []bool{false, true} {
		t.Run(fmt.Sprintf("initially_cached=%t", initiallyCached), func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			member := func(nick string) string {
				return fmt.Sprintf(`{"user":{"id":"200","username":"member"},"nick":%q,"roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false}`, nick)
			}
			members, initialCount := "[]", 0
			if initiallyCached {
				members, initialCount = "["+member("initial")+"]", 1
			}
			dispatch("GUILD_CREATE", fmt.Sprintf(`{"id":"100","name":"guild","member_count":%d,"members":%s}`, initialCount, members))
			original, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			callbacks := 0
			session.AddHandler(func(s *Session, event *GuildMemberAdd) {
				callbacks++
				guild, err := s.State.Guild(event.GuildID)
				if err != nil {
					t.Error(err)
					return
				}
				cached, err := s.State.Member(event.GuildID, event.User.ID)
				if err != nil {
					t.Error(err)
					return
				}
				if guild.MemberCount != 1 || len(guild.Members) != 1 {
					t.Errorf("member add callback sees count=%d, cached members=%d; want 1 each", guild.MemberCount, len(guild.Members))
				}
				if cached.Nick != event.Nick || guild.Members[0] != cached {
					t.Error("member add did not refresh both cache indexes before its callback")
				}
			})
			add := func(nick string) {
				t.Helper()
				data := member(nick)
				dispatch("GUILD_MEMBER_ADD", `{"guild_id":"100",`+data[1:])
			}
			add("first")
			beforeRepeated, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			previousCount := beforeRepeated.MemberCount
			// Discord also sends GUILD_MEMBER_ADD for existing guild members.
			add("refreshed")
			add("refreshed again")
			if original.MemberCount != initialCount || (initiallyCached && original.Members[0].Nick != "initial") || beforeRepeated.MemberCount != previousCount || beforeRepeated.Members[0].Nick != "first" {
				t.Error("later member adds changed an earlier guild snapshot")
			}
			dispatch("GUILD_MEMBER_REMOVE", `{"guild_id":"100","user":{"id":"200","username":"member"}}`)
			afterRemove, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			if afterRemove.MemberCount != 0 || len(afterRemove.Members) != 0 {
				t.Errorf("after removal: count=%d, cached members=%d; want 0 each", afterRemove.MemberCount, len(afterRemove.Members))
			}
			add("rejoined")
			if callbacks != 4 {
				t.Errorf("member add callbacks=%d, want 4", callbacks)
			}
		})
	}
}
