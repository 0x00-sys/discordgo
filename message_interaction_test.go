package discordgo

import (
	"encoding/json"
	"testing"
)

func TestMessageInteractionLinksMemberUser(t *testing.T) {
	var m Message
	err := json.Unmarshal([]byte(`{
		"id":"message",
		"channel_id":"channel",
		"guild_id":"guild",
		"author":{"id":"bot","username":"Bot"},
		"interaction":{
			"id":"interaction",
			"type":2,
			"name":"command",
			"user":{"id":"user","username":"User"},
			"member":{"roles":["role"],"nick":"Nick"}
		}
	}`), &m)
	if err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if m.Interaction == nil {
		t.Fatal("Interaction is nil")
	}
	if m.Interaction.Member == nil {
		t.Fatal("Interaction.Member is nil")
	}
	if m.Interaction.Member.User != m.Interaction.User {
		t.Fatal("Interaction.Member.User was not linked to Interaction.User")
	}
	if m.Interaction.Member.GuildID != "guild" {
		t.Fatalf("Interaction.Member.GuildID = %q, want guild", m.Interaction.Member.GuildID)
	}
	if mention := m.Interaction.Member.Mention(); mention != "<@!user>" {
		t.Fatalf("Interaction.Member.Mention() = %q, want %q", mention, "<@!user>")
	}
	if displayName := m.Interaction.Member.DisplayName(); displayName != "Nick" {
		t.Fatalf("Interaction.Member.DisplayName() = %q, want Nick", displayName)
	}
}
