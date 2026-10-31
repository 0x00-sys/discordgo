package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGuildMembersChunkIdentityWithoutCache(t *testing.T) {
	for _, mode := range []string{"tracked", "state_disabled", "members_disabled", "guild_uncached"} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/gateway=%t", mode, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = mode != "state_disabled"
				session.State.TrackMembers = mode != "members_disabled"
				if mode != "guild_uncached" {
					if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
						t.Fatal(err)
					}
				}
				cache := NewState()
				if err := cache.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				payload := []byte(`{"guild_id":"100","chunk_index":0,"chunk_count":1,"nonce":"lookup","members":[{
					"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0,"pending":false}]}`)
				called := false
				session.AddHandler(func(_ *Session, event *GuildMembersChunk) {
					called = true
					if len(event.Members) != 1 || event.Members[0] == nil || event.Nonce != "lookup" {
						t.Error("callback did not receive the requested member chunk")
						return
					}
					member := event.Members[0]
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := cache.MemberAdd(member); err != nil {
						t.Errorf("caching callback member: %v", err)
					} else if cached, err := cache.Member("100", "200"); err != nil || cached.GuildID != "100" {
						t.Errorf("retrieving callback member: %v", err)
					}
				})
				if gateway {
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"GUILD_MEMBERS_CHUNK","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else {
					var event GuildMembersChunk
					if err := json.Unmarshal(payload, &event); err != nil {
						t.Fatal(err)
					}
					session.handleEvent(guildMembersChunkEventType, &event)
				}
				if !called {
					t.Fatal("member chunk callback did not run")
				}
			})
		}
	}
}

func TestGuildMembersChunkIdentityAbsentMembers(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	var received *GuildMembersChunk
	session.AddHandler(func(_ *Session, event *GuildMembersChunk) { received = event })
	for _, members := range []string{`[]`, `null`, `[null]`} {
		received = nil
		payload := fmt.Sprintf(`{"op":0,"s":1,"t":"GUILD_MEMBERS_CHUNK","d":{"guild_id":"100","chunk_index":0,"chunk_count":1,"members":%s}}`, members)
		if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if received == nil {
			t.Fatal("callback did not run")
		}
		for _, member := range received.Members {
			if member != nil {
				t.Fatal("absent member was not preserved")
			}
		}
	}
}
