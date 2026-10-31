package discordgo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPresenceUpdateRefreshesCachedMemberAvatar(t *testing.T) {
	for _, trackPresences := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			avatar       string
			want         string
			offline      bool
			programmatic bool
		}{
			{name: "replace", avatar: `,"avatar":"new-avatar"`, want: "new-avatar"},
			{name: "clear", avatar: `,"avatar":null`},
			{name: "omit", want: "old-avatar"},
			{name: "clear_offline", avatar: `,"avatar":null`, offline: true},
			{name: "replace_offline", avatar: `,"avatar":"new-avatar"`, want: "new-avatar", offline: true},
			{name: "omit_offline", want: "old-avatar", offline: true},
			{name: "programmatic_zero", want: "old-avatar", programmatic: true},
		} {
			t.Run(fmt.Sprintf("presences=%t/%s", trackPresences, tc.name), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel, session.SyncEvents = -1, true
				// Presence events can arrive without the GUILD_MEMBERS intent.
				session.Identify.Intents = IntentsGuildPresences
				session.State.TrackPresences = trackPresences
				if err := session.State.GuildAdd(&Guild{ID: "100", Members: []*Member{{
					GuildID: "100", Nick: "guild-name", Roles: []string{"role"},
					User: &User{ID: "200", Username: "cached-name", Discriminator: "0", Avatar: "old-avatar"},
				}}}); err != nil {
					t.Fatal(err)
				}
				before, err := session.State.Guild("100")
				if err != nil {
					t.Fatal(err)
				}
				old := before.Members[0]
				called := false
				session.AddHandler(func(s *Session, update *PresenceUpdate) {
					called = true
					member, err := s.State.Member("100", "200")
					if err != nil {
						t.Error(err)
						return
					}
					if member.User.Avatar != tc.want {
						t.Errorf("member avatar = %q, want %q", member.User.Avatar, tc.want)
					}
					wantUser := &User{ID: "200", Discriminator: "0", Avatar: tc.want}
					if got := member.AvatarURL(""); got != wantUser.AvatarURL("") {
						t.Errorf("member avatar URL = %q, want %q", got, wantUser.AvatarURL(""))
					}
					if member.User.Username != "cached-name" || member.Nick != "guild-name" || len(member.Roles) != 1 || member.Roles[0] != "role" {
						t.Error("partial presence lost unrelated member fields")
					}
					guild, err := s.State.Guild("100")
					if err != nil || len(guild.Members) != 1 || guild.Members[0] != member {
						t.Errorf("guild and member cache disagree: %v", err)
					}
					if (!tc.offline || tc.avatar != "") && (member == old || member.User == old.User) {
						t.Error("presence update reused the earlier member snapshot")
					}
					if update.User.Username != "" {
						t.Error("cache merge modified the partial event user")
					}
				})
				status := StatusOnline
				if tc.offline {
					status = StatusOffline
				}
				if tc.programmatic {
					session.handleEvent(presenceUpdateEventType, &PresenceUpdate{GuildID: "100", Presence: Presence{User: &User{ID: "200"}, Status: status}})
				} else {
					payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200"%s},"status":%q,"activities":[],"client_status":{}}}`, tc.avatar, status)
					if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				if !called {
					t.Fatal("presence callback did not run")
				}
				if old.User.Avatar != "old-avatar" || before.Members[0] != old {
					t.Error("presence update modified an earlier guild/member snapshot")
				}
			})
		}
	}
}

func TestOfflinePresenceAvatarDoesNotAddMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200","avatar":"new-avatar"},"status":"offline","activities":[],"client_status":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.State.Member("100", "200"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("offline member lookup = %v, want not found", err)
	}
	guild, err := session.State.Guild("100")
	if err != nil || len(guild.Members) != 0 {
		t.Errorf("offline presence populated guild members: %v", err)
	}
}
