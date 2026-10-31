package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPresenceUpdateClearsExplicitNullAvatar(t *testing.T) {
	for _, tc := range []struct {
		name            string
		avatar          string
		wantAvatar      string
		offline         bool
		disableTracking bool
		disableState    bool
		programmatic    bool
	}{
		{name: "clear_online", avatar: `,"avatar":null`},
		{name: "clear_offline", avatar: `,"avatar":null`, offline: true},
		{name: "replace", avatar: `,"avatar":"new-avatar"`, wantAvatar: "new-avatar"},
		{name: "omit", wantAvatar: "old-avatar"},
		{name: "malformed_avatar", avatar: `,"avatar":123`, wantAvatar: "old-avatar"},
		{name: "tracking_disabled", avatar: `,"avatar":null`, wantAvatar: "old-avatar", disableTracking: true},
		{name: "state_disabled", avatar: `,"avatar":null`, wantAvatar: "old-avatar", disableState: true},
		{name: "programmatic_zero", wantAvatar: "old-avatar", programmatic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			if err := session.State.GuildAdd(&Guild{ID: "100", Presences: []*Presence{{
				User:   &User{ID: "200", Username: "cached-name", Discriminator: "0", Avatar: "old-avatar"},
				Status: StatusOnline,
			}}}); err != nil {
				t.Fatal(err)
			}
			before, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			old := before.Presences[0]
			session.State.TrackPresences = !tc.disableTracking
			session.StateEnabled = !tc.disableState
			called := false
			session.AddHandler(func(s *Session, update *PresenceUpdate) {
				called = true
				if update.GuildID != "100" || update.User == nil || update.User.ID != "200" || update.User.Username != "" {
					t.Error("presence decoding or cache merge changed the event's identity/user fields")
				}
				current, err := s.State.Presence("100", "200")
				if err != nil {
					t.Error(err)
					return
				}
				if current.User.Avatar != tc.wantAvatar || current.User.Username != "cached-name" {
					t.Errorf("cached avatar/name = %q/%q, want %q/cached-name", current.User.Avatar, current.User.Username, tc.wantAvatar)
				}
				wantUser := &User{ID: "200", Discriminator: "0", Avatar: tc.wantAvatar}
				if got := current.User.AvatarURL(""); got != wantUser.AvatarURL("") {
					t.Errorf("cached avatar URL = %q, want %q", got, wantUser.AvatarURL(""))
				}
				guild, err := s.State.Guild("100")
				if err != nil || len(guild.Presences) != 1 || guild.Presences[0] != current {
					t.Errorf("guild and presence cache disagree: %v", err)
				}
				if !tc.disableTracking && !tc.disableState && (current == old || current.User == old.User) {
					t.Error("presence update reused the previous snapshot")
				}
			})
			status := StatusIdle
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
			if old.User.Avatar != "old-avatar" || before.Presences[0] != old || old.Status != StatusOnline {
				t.Error("presence update mutated an earlier snapshot")
			}
		})
	}
}
