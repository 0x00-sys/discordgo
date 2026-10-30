package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGuildMemberUpdatePreservesOmittedNickAndPending(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fields       string
		eventNick    string
		wantNick     string
		wantPending  bool
		programmatic bool
	}{
		{name: "omitted", wantNick: "nickname", wantPending: true},
		{name: "clear_nick", fields: `,"nick":null`, wantPending: true},
		{name: "complete_screening", fields: `,"pending":false`, wantNick: "nickname"},
		{name: "replace_both", fields: `,"nick":"new nickname","pending":false`, eventNick: "new nickname", wantNick: "new nickname"},
		{name: "programmatic_zero_values", programmatic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			dispatch := func(sequence int, event, data string) {
				t.Helper()
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, event, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			dispatch(1, "GUILD_CREATE", `{"id":"100","member_count":1,"members":[{"user":{"id":"200","username":"member"},"nick":"nickname","pending":true,"roles":["300"],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false}]}`)
			before, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			original := before.Members[0]
			var callback *GuildMemberUpdate
			session.AddHandler(func(s *Session, update *GuildMemberUpdate) {
				callback = update
				cached, err := s.State.Member("100", "200")
				if err != nil {
					t.Error(err)
					return
				}
				if cached.Nick != tc.wantNick || cached.Pending != tc.wantPending {
					t.Errorf("callback cache nick=%q pending=%t, want %q %t", cached.Nick, cached.Pending, tc.wantNick, tc.wantPending)
				}
				guild, err := s.State.Guild("100")
				if err != nil {
					t.Error(err)
					return
				}
				if guild.Members[0] != cached || cached == original || len(cached.Roles) != 0 || cached.User.Username != "updated" {
					t.Error("member update did not replace both cache indexes and apply supplied fields")
				}
			})
			if tc.programmatic {
				session.handleEvent(guildMemberUpdateEventType, &GuildMemberUpdate{Member: &Member{
					GuildID: "100", User: &User{ID: "200", Username: "updated"}, Roles: []string{},
				}})
			} else {
				dispatch(2, "GUILD_MEMBER_UPDATE", `{"guild_id":"100","user":{"id":"200","username":"updated"},"roles":[],"avatar":null,"banner":null,"joined_at":"2026-10-30T10:00:00Z"`+tc.fields+`}`)
			}
			if callback == nil || callback.BeforeUpdate == nil {
				t.Fatal("member update callback or BeforeUpdate missing")
			}
			if callback.Nick != tc.eventNick || callback.Pending {
				t.Error("cache merging modified the incoming event")
			}
			if original.Nick != "nickname" || !original.Pending || original.User.Username != "member" || len(original.Roles) != 1 ||
				callback.BeforeUpdate.Nick != "nickname" || !callback.BeforeUpdate.Pending {
				t.Error("member update changed an earlier snapshot")
			}
		})
	}
}
