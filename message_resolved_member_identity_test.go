package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestMessageResolvedMembersPreserveGuildIdentity(t *testing.T) {
	for _, eventType := range []string{messageCreateEventType, messageUpdateEventType} {
		for _, stateEnabled := range []bool{false, true} {
			for _, gateway := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/state=%t/gateway=%t", eventType, stateEnabled, gateway), func(t *testing.T) {
					session, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					session.SyncEvents = true
					session.StateEnabled = stateEnabled
					if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
						t.Fatal(err)
					}
					payload := []byte(`{"id":"400","channel_id":"300","guild_id":"100",
						"resolved":{"users":{"200":{"id":"200","username":"selected","discriminator":"0"}},
						"members":{"200":{"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","flags":0}}}}`)
					var received *Message
					session.AddHandler(func(_ *Session, event *MessageCreate) { received = event.Message })
					session.AddHandler(func(_ *Session, event *MessageUpdate) { received = event.Message })
					if gateway {
						if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":%q,"d":%s}`, eventType, payload))); err != nil {
							t.Fatal(err)
						}
					} else {
						var event interface{}
						if eventType == messageCreateEventType {
							event = &MessageCreate{}
						} else {
							event = &MessageUpdate{}
						}
						if err := json.Unmarshal(payload, event); err != nil {
							t.Fatal(err)
						}
						session.handleEvent(eventType, event)
					}
					if received == nil || received.Resolved == nil {
						t.Fatal("callback did not receive message resolved data")
					}
					member := received.Resolved.Members["200"]
					if member == nil || member.User == nil || member.User != received.Resolved.Users["200"] {
						t.Fatal("resolved member or linked user missing")
					}
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
						t.Errorf("caching resolved member failed: %v", err)
					} else if cached, err := session.State.Member("100", "200"); err != nil || cached.GuildID != "100" || cached.User.ID != "200" {
						t.Errorf("member not retrievable from its guild: %v", err)
					}
				})
			}
		}
	}
}

func TestMessageResolvedMemberGuildIdentityOptionalData(t *testing.T) {
	for _, payload := range []string{
		`{"guild_id":"100"}`,
		`{"guild_id":"100","resolved":null}`,
		`{"guild_id":"100","resolved":{"members":null}}`,
		`{"guild_id":"100","resolved":{"members":{"200":null}}}`,
	} {
		var message Message
		if err := json.Unmarshal([]byte(payload), &message); err != nil {
			t.Fatal(err)
		}
		if message.Resolved != nil && message.Resolved.Members["200"] != nil {
			t.Fatal("decoder invented a resolved member")
		}
	}
}
