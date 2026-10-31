package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestAddedThreadMemberPreservesGuildIdentity(t *testing.T) {
	for _, tracking := range []string{"enabled", "state_disabled", "members_disabled"} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/gateway=%t", tracking, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = tracking != "state_disabled"
				session.State.TrackMembers = tracking != "members_disabled"
				if err := session.State.GuildAdd(&Guild{ID: "100", Threads: []*Channel{{
					ID: "300", GuildID: "100", Type: ChannelTypeGuildPublicThread,
				}}}); err != nil {
					t.Fatal(err)
				}
				cache := NewState()
				if err := cache.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				payload := []byte(`{"id":"300","guild_id":"100","member_count":1,"added_members":[{
					"id":"300","user_id":"200","join_timestamp":"2026-10-30T10:00:00Z","flags":0,"presence":null,
					"member":{"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}]}`)
				called := false
				session.AddHandler(func(_ *Session, event *ThreadMembersUpdate) {
					called = true
					if len(event.AddedMembers) != 1 || event.AddedMembers[0].Member == nil {
						t.Error("callback did not receive the added guild member")
						return
					}
					member := event.AddedMembers[0].Member
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					// A callback can populate a separate cache regardless of session tracking.
					if err := cache.MemberAdd(member); err != nil {
						t.Errorf("caching callback member: %v", err)
					} else if cached, err := cache.Member("100", "200"); err != nil || cached.GuildID != "100" {
						t.Errorf("retrieving callback member: %v", err)
					}
				})
				if gateway {
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"THREAD_MEMBERS_UPDATE","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else {
					var event ThreadMembersUpdate
					if err := json.Unmarshal(payload, &event); err != nil {
						t.Fatal(err)
					}
					session.handleEvent(threadMembersUpdateEventType, &event)
				}
				if !called {
					t.Fatal("thread member callback did not run")
				}
			})
		}
	}
}

func TestAddedThreadMemberGuildIdentityAbsentData(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	var received *ThreadMembersUpdate
	session.AddHandler(func(_ *Session, event *ThreadMembersUpdate) { received = event })
	for _, added := range []string{`[]`, `[{"id":"300","user_id":"200"}]`, `[{"id":"300","user_id":"200","member":null}]`} {
		received = nil
		payload := fmt.Sprintf(`{"op":0,"s":1,"t":"THREAD_MEMBERS_UPDATE","d":{"id":"300","guild_id":"100","member_count":1,"added_members":%s}}`, added)
		if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if received == nil {
			t.Fatal("callback did not run")
		}
		for _, member := range received.AddedMembers {
			if member.Member != nil {
				t.Fatal("absent member was not preserved")
			}
		}
	}
}
