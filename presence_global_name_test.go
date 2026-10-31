package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPresenceUpdateRefreshesCachedGlobalName(t *testing.T) {
	for _, tracking := range []struct {
		name      string
		presences bool
		members   bool
	}{
		{"both", true, true},
		{"presences_only", true, false},
		{"members_only", false, true},
		{"disabled", false, false},
	} {
		for _, tc := range []struct {
			name         string
			field        string
			want         string
			offline      bool
			programmatic bool
		}{
			{name: "replace", field: `,"global_name":"new-name"`, want: "new-name"},
			{name: "clear", field: `,"global_name":null`},
			{name: "omit", want: "old-name"},
			{name: "replace_offline", field: `,"global_name":"new-name"`, want: "new-name", offline: true},
			{name: "clear_offline", field: `,"global_name":null`, offline: true},
			{name: "omit_offline", want: "old-name", offline: true},
			{name: "malformed", field: `,"global_name":123`, want: "old-name"},
			{name: "programmatic_zero", want: "old-name", programmatic: true},
		} {
			t.Run(tracking.name+"/"+tc.name, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel, session.SyncEvents = -1, true
				session.Identify.Intents = IntentsGuildPresences
				user := &User{ID: "200", Username: "cached-name", GlobalName: "old-name", Avatar: "avatar"}
				if err := session.State.GuildAdd(&Guild{ID: "100",
					Members:   []*Member{{GuildID: "100", User: user, Roles: []string{"role"}}},
					Presences: []*Presence{{User: user, Status: StatusOnline}},
				}); err != nil {
					t.Fatal(err)
				}
				before, err := session.State.Guild("100")
				if err != nil {
					t.Fatal(err)
				}
				oldMember, oldPresence := before.Members[0], before.Presences[0]
				session.State.TrackPresences, session.State.TrackMembers = tracking.presences, tracking.members
				called := false
				session.AddHandler(func(s *Session, update *PresenceUpdate) {
					called = true
					member, err := s.State.Member("100", "200")
					if err != nil {
						t.Error(err)
						return
					}
					presence, err := s.State.Presence("100", "200")
					if err != nil {
						t.Error(err)
						return
					}
					for _, cache := range []struct {
						name        string
						user        *User
						displayName string
						tracked     bool
					}{
						{"member", member.User, member.DisplayName(), tracking.members},
						{"presence", presence.User, presence.User.DisplayName(), tracking.presences},
					} {
						want := "old-name"
						if cache.tracked {
							want = tc.want
						}
						wantDisplay := want
						if wantDisplay == "" {
							wantDisplay = "cached-name"
						}
						if cache.user.GlobalName != want || cache.displayName != wantDisplay {
							t.Errorf("%s global/display name = %q/%q, want %q/%q", cache.name, cache.user.GlobalName, cache.displayName, want, wantDisplay)
						}
						if cache.user.Username != "cached-name" || cache.user.Avatar != "avatar" {
							t.Errorf("%s merge lost omitted user fields", cache.name)
						}
					}
					guild, err := s.State.Guild("100")
					if err != nil || len(guild.Members) != 1 || guild.Members[0] != member || len(guild.Presences) != 1 || guild.Presences[0] != presence {
						t.Errorf("guild and member/presence caches disagree: %v", err)
					}
					if len(member.Roles) != 1 || member.Roles[0] != "role" {
						t.Error("partial presence lost member roles")
					}
					if update.User == nil || update.User.ID != "200" || update.User.Username != "" || update.User.Avatar != "" {
						t.Error("cache merge changed the partial event user")
					}
				})
				status := StatusOnline
				if tc.offline {
					status = StatusOffline
				}
				if tc.programmatic {
					session.handleEvent(presenceUpdateEventType, &PresenceUpdate{GuildID: "100", Presence: Presence{User: &User{ID: "200"}, Status: status}})
				} else {
					payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200"%s},"status":%q,"activities":[],"client_status":{}}}`, tc.field, status)
					if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				if !called {
					t.Fatal("presence callback did not run")
				}
				if oldMember.User.GlobalName != "old-name" || oldPresence.User.GlobalName != "old-name" || before.Members[0] != oldMember || before.Presences[0] != oldPresence {
					t.Error("presence update changed an earlier guild/member/presence snapshot")
				}
			})
		}
	}
}
