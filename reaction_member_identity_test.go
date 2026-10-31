package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestReactionMemberPreservesGuildIdentity(t *testing.T) {
	for _, stateEnabled := range []bool{false, true} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("state=%t/gateway=%t", stateEnabled, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = stateEnabled
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				payload := []byte(`{"guild_id":"100","channel_id":"300","message_id":"400","user_id":"200","emoji":{"id":null,"name":"👍"},"burst":false,"type":0,
					"member":{"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}`)
				called := false
				session.AddHandler(func(_ *Session, event *MessageReactionAdd) {
					called = true
					if event == nil || event.Member == nil || event.Member.User == nil {
						t.Error("callback did not receive the reaction member")
						return
					}
					member := event.Member
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := session.State.MemberAdd(member); err != nil {
						t.Errorf("caching reaction member failed: %v", err)
					} else if cached, err := session.State.Member("100", "200"); err != nil || cached.User.ID != "200" {
						t.Errorf("member not retrievable from its guild: %v", err)
					}
				})
				if gateway {
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"MESSAGE_REACTION_ADD","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else {
					var event MessageReactionAdd
					if err := json.Unmarshal(payload, &event); err != nil {
						t.Fatal(err)
					}
					session.handleEvent(messageReactionAddEventType, &event)
				}
				if !called {
					t.Fatal("reaction callback did not run")
				}
			})
		}
	}
}

func TestReactionMemberGuildIdentityOptionalData(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	var received *MessageReactionAdd
	session.AddHandler(func(_ *Session, event *MessageReactionAdd) { received = event })
	for _, payload := range []string{
		`{"channel_id":"300","message_id":"400","user_id":"200","emoji":{"id":null,"name":"👍"},"burst":false,"type":0}`,
		`{"guild_id":"100","channel_id":"300","message_id":"400","user_id":"200","emoji":{"id":null,"name":"👍"},"burst":false,"type":0,"member":null}`,
	} {
		received = nil
		if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"MESSAGE_REACTION_ADD","d":%s}`, payload))); err != nil {
			t.Fatal(err)
		}
		if received == nil || received.Member != nil {
			t.Fatal("omitted/null reaction member was not preserved")
		}
	}
}
