package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestInteractionMembersPreserveGuildIdentity(t *testing.T) {
	for _, kind := range []InteractionType{InteractionApplicationCommand, InteractionMessageComponent, InteractionApplicationCommandAutocomplete, InteractionModalSubmit} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("type_%d/gateway_%t", kind, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				data := `"id":"300","name":"inspect","type":1,"options":[{"name":"user","type":6,"value":"201"}]`
				switch kind {
				case InteractionMessageComponent:
					data = `"custom_id":"select","component_type":5,"values":["201"]`
				case InteractionApplicationCommandAutocomplete:
					data = `"id":"300","name":"inspect","type":1,"options":[{"name":"user","type":6,"value":"201"},{"name":"query","type":3,"value":"abc","focused":true}]`
				case InteractionModalSubmit:
					data = `"custom_id":"modal","components":[{"type":18,"label":"User","component":{"type":5,"custom_id":"select","values":["201"]}}]`
				}
				payload := []byte(fmt.Sprintf(`{"id":"400","application_id":"500","type":%d,"guild_id":"100","channel_id":"600","token":"test","version":1,
					"member":{"user":{"id":"200","username":"invoker","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0},
					"data":{%s,"resolved":{"users":{"201":{"id":"201","username":"selected","discriminator":"0"}},"members":{"201":{"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","flags":0}}}}}`, kind, data))
				var interaction *Interaction
				if gateway {
					session.AddHandler(func(_ *Session, event *InteractionCreate) { interaction = event.Interaction })
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"INTERACTION_CREATE","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else if err := json.Unmarshal(payload, &interaction); err != nil {
					t.Fatal(err)
				}
				if interaction == nil || interaction.Member == nil {
					t.Fatal("interaction or invoking member missing")
				}
				members := []*Member{interaction.Member}
				switch data := interaction.Data.(type) {
				case ApplicationCommandInteractionData:
					members = append(members, data.Resolved.Members["201"])
				case MessageComponentInteractionData:
					members = append(members, data.Resolved.Members["201"])
				case ModalSubmitInteractionData:
					members = append(members, data.Resolved.Members["201"])
				default:
					t.Fatalf("unexpected interaction data %T", interaction.Data)
				}
				for _, member := range members {
					if member == nil || member.User == nil {
						t.Fatal("member or linked user missing")
					}
					if member.GuildID != "100" {
						t.Errorf("member %s guild = %q, want 100", member.User.ID, member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", member.User.ID, "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", member.User.ID, "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := session.State.MemberAdd(member); err != nil {
						t.Errorf("caching interaction member failed: %v", err)
					} else if cached, err := session.State.Member("100", member.User.ID); err != nil || cached.User.ID != member.User.ID {
						t.Errorf("member not retrievable from its guild: %v", err)
					}
				}
			})
		}
	}
}

func TestInteractionMemberGuildIdentityOptionalData(t *testing.T) {
	for _, payload := range []string{
		`{"type":1}`,
		`{"type":2,"user":{"id":"200"},"data":{"id":"300","name":"inspect","type":1}}`,
		`{"type":2,"guild_id":"100","data":{"resolved":{"members":{"200":null}}}}`,
	} {
		var interaction Interaction
		if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
			t.Fatal(err)
		}
		if interaction.Member != nil {
			t.Fatal("decoder invented an invoking member")
		}
	}
}
