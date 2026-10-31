package discordgo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPresenceUpdateRefreshesCachedMemberDiscriminator(t *testing.T) {
	const userID = "175928847299117063"
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
		for _, status := range []Status{StatusOnline, StatusOffline} {
			for _, tc := range []struct {
				name          string
				field         string
				discriminator string
				tag           string
				avatarIndex   int
			}{
				{"legacy", `,"discriminator":"9876"`, "9876", "person#9876", 1},
				{"migrated", `,"discriminator":"0"`, "0", "person", 2},
				{"omitted", "", "1234", "person#1234", 4},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", tracking.name, status, tc.name), func(t *testing.T) {
					session, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					session.LogLevel, session.SyncEvents = -1, true
					session.Identify.Intents = IntentsGuildPresences
					user := &User{ID: userID, Username: "person", Discriminator: "1234"}
					if err := session.State.GuildAdd(&Guild{ID: "100",
						Members:   []*Member{{GuildID: "100", User: user, Nick: "nickname", Roles: []string{"role"}}},
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
					session.AddHandler(func(s *Session, event *PresenceUpdate) {
						called = true
						member, err := s.State.Member("100", userID)
						if err != nil {
							t.Error(err)
							return
						}
						wantDiscriminator, wantTag, wantAvatar := "1234", "person#1234", 4
						if tracking.members {
							wantDiscriminator, wantTag, wantAvatar = tc.discriminator, tc.tag, tc.avatarIndex
						}
						if member.User.Discriminator != wantDiscriminator || member.User.String() != wantTag {
							t.Errorf("member discriminator/tag = %q/%q, want %q/%q", member.User.Discriminator, member.User.String(), wantDiscriminator, wantTag)
						}
						if got, want := member.AvatarURL(""), EndpointDefaultUserAvatar(wantAvatar); got != want {
							t.Errorf("member default avatar = %q, want %q", got, want)
						}
						presence, err := s.State.Presence("100", userID)
						if err != nil {
							t.Error(err)
							return
						}
						wantPresence := "1234"
						if tracking.presences {
							wantPresence = tc.discriminator
						}
						if presence.User.Discriminator != wantPresence {
							t.Errorf("presence discriminator = %q, want %q", presence.User.Discriminator, wantPresence)
						}
						guild, err := s.State.Guild("100")
						if err != nil || len(guild.Members) != 1 || guild.Members[0] != member {
							t.Errorf("guild/member indexes disagree: %v", err)
						}
						if member.Nick != "nickname" || len(member.Roles) != 1 || member.Roles[0] != "role" || member.User.Username != "person" {
							t.Error("partial presence lost unrelated member fields")
						}
						if event.User.Username != "" || event.User.Avatar != "" {
							t.Error("cache merge changed the partial event user")
						}
					})
					payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":%q%s},"status":%q,"activities":[],"client_status":{}}}`, userID, tc.field, status)
					if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
					if !called {
						t.Fatal("presence callback did not run")
					}
					if oldMember.User.Discriminator != "1234" || oldPresence.User.Discriminator != "1234" || before.Members[0] != oldMember || before.Presences[0] != oldPresence {
						t.Error("presence update changed an earlier snapshot")
					}
				})
			}
		}
	}
}

func TestOfflinePresenceDiscriminatorDoesNotAddMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200","discriminator":"0"},"status":"offline","activities":[],"client_status":{}}}`)); err != nil {
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
