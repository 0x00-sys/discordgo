package discordgo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func messageCreateWithMember(memberFields string) []byte {
	return []byte(`{
		"id":"message",
		"channel_id":"channel",
		"guild_id":"guild",
		"content":"hello",
		"timestamp":"2026-10-13T12:00:00.000000+00:00",
		"author":{"id":"user","username":"User"},
		"member":{
			"roles":["role"],
			"nick":"Nick",
			"joined_at":"2025-05-16T22:51:16.692000+00:00",
			"flags":0,
			"communication_disabled_until":null` + memberFields + `
		}
	}`)
}

func TestMessageCreateMemberUnusualDMActivityUntil(t *testing.T) {
	want := time.Date(2026, time.October, 14, 9, 30, 15, 692000000, time.UTC)

	tests := []struct {
		name   string
		fields string
		want   *time.Time
	}{
		{name: "set", fields: `,"unusual_dm_activity_until":"2026-10-14T09:30:15.692000+00:00"`, want: &want},
		{name: "null", fields: `,"unusual_dm_activity_until":null`},
		{name: "absent", fields: ``},
		{name: "malformed string", fields: `,"unusual_dm_activity_until":"not-a-time"`},
		{name: "empty string", fields: `,"unusual_dm_activity_until":""`},
		{name: "number", fields: `,"unusual_dm_activity_until":1760434215`},
		{name: "object", fields: `,"unusual_dm_activity_until":{"until":"2026-10-14T09:30:15Z"}`},
		{name: "bool", fields: `,"unusual_dm_activity_until":true`},
		{name: "array", fields: `,"unusual_dm_activity_until":["2026-10-14T09:30:15Z"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m MessageCreate
			if err := json.Unmarshal(messageCreateWithMember(tt.fields), &m); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}

			// The rest of the message and member must survive whatever the field holds.
			if m.Message == nil || m.ID != "message" || m.Content != "hello" {
				t.Fatalf("message = %#v, want message with ID and content intact", m.Message)
			}
			if m.Author == nil || m.Author.ID != "user" {
				t.Fatalf("Author = %#v, want user", m.Author)
			}
			if m.Member == nil {
				t.Fatal("Member is nil")
			}
			if m.Member.Nick != "Nick" || len(m.Member.Roles) != 1 || m.Member.Roles[0] != "role" {
				t.Fatalf("Member = %#v, want nick and roles intact", m.Member)
			}
			if m.Member.User != m.Author || m.Member.GuildID != "guild" {
				t.Fatal("Member was not linked to Author and guild")
			}
			if m.Member.JoinedAt.IsZero() {
				t.Fatal("Member.JoinedAt is zero, want joined_at intact")
			}

			got := m.Member.UnusualDMActivityUntil
			if tt.want == nil {
				if got != nil {
					t.Fatalf("UnusualDMActivityUntil = %v, want nil", got)
				}
				return
			}
			if got == nil || !got.Equal(*tt.want) {
				t.Fatalf("UnusualDMActivityUntil = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemberUnmarshalStillRejectsOtherMalformedFields(t *testing.T) {
	var m Member
	err := json.Unmarshal([]byte(`{"joined_at":"not-a-time","unusual_dm_activity_until":"2026-10-14T09:30:15Z"}`), &m)
	if err == nil {
		t.Fatal("json.Unmarshal returned nil error, want joined_at to stay strict")
	}
}

func TestGuildMemberEventsUnusualDMActivityUntil(t *testing.T) {
	data := []byte(`{
		"guild_id":"guild",
		"user":{"id":"user","username":"User"},
		"roles":["role"],
		"nick":"Nick",
		"unusual_dm_activity_until":"2026-10-14T09:30:15Z"
	}`)
	want := time.Date(2026, time.October, 14, 9, 30, 15, 0, time.UTC)

	check := func(t *testing.T, member *Member) {
		t.Helper()
		if member == nil {
			t.Fatal("Member is nil")
		}
		if member.GuildID != "guild" || member.User == nil || member.User.ID != "user" || member.Nick != "Nick" {
			t.Fatalf("Member = %#v, want guild, user and nick intact", member)
		}
		if member.UnusualDMActivityUntil == nil || !member.UnusualDMActivityUntil.Equal(want) {
			t.Fatalf("UnusualDMActivityUntil = %v, want %v", member.UnusualDMActivityUntil, want)
		}
	}

	t.Run("add", func(t *testing.T) {
		var e GuildMemberAdd
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		check(t, e.Member)
	})
	t.Run("update", func(t *testing.T) {
		var e GuildMemberUpdate
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		check(t, e.Member)
		if e.BeforeUpdate != nil {
			t.Fatalf("BeforeUpdate = %#v, want nil", e.BeforeUpdate)
		}
	})
	t.Run("remove", func(t *testing.T) {
		var e GuildMemberRemove
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		check(t, e.Member)
		if e.BeforeDelete != nil {
			t.Fatalf("BeforeDelete = %#v, want nil", e.BeforeDelete)
		}
	})
	t.Run("event registry", func(t *testing.T) {
		for _, eventType := range []string{guildMemberAddEventType, guildMemberUpdateEventType, guildMemberRemoveEventType} {
			value := registeredInterfaceProviders[eventType].New()
			if err := json.Unmarshal(data, value); err != nil {
				t.Fatalf("%s: json.Unmarshal returned error: %v", eventType, err)
			}
		}
	})
}

func TestInteractionResolvedMemberUnusualDMActivityUntil(t *testing.T) {
	// Shape taken from the resolved data examples in Discord's component reference.
	var resolved ComponentInteractionDataResolved
	err := json.Unmarshal([]byte(`{
		"members":{
			"1111111111111111111":{
				"avatar":null,
				"banner":null,
				"collectibles":null,
				"communication_disabled_until":null,
				"flags":0,
				"joined_at":"2025-05-16T22:51:16.692000+00:00",
				"nick":null,
				"pending":false,
				"permissions":"2248473465835073",
				"premium_since":null,
				"roles":["2222222222222222222"],
				"unusual_dm_activity_until":null
			}
		},
		"users":{
			"1111111111111111111":{"id":"1111111111111111111","username":"User"}
		}
	}`), &resolved)
	if err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	member := resolved.Members["1111111111111111111"]
	if member == nil {
		t.Fatal("resolved member is nil")
	}
	if member.UnusualDMActivityUntil != nil {
		t.Fatalf("UnusualDMActivityUntil = %v, want nil", member.UnusualDMActivityUntil)
	}
	if member.Permissions != 2248473465835073 || len(member.Roles) != 1 {
		t.Fatalf("member = %#v, want permissions and roles intact", member)
	}
}

func TestMemberMarshalUnusualDMActivityUntil(t *testing.T) {
	plain, err := json.Marshal(&Member{Nick: "Nick"})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if strings.Contains(string(plain), "unusual_dm_activity_until") {
		t.Fatalf("member JSON = %s, want no unusual_dm_activity_until key when nil", plain)
	}

	until := time.Date(2026, time.October, 14, 9, 30, 15, 0, time.UTC)
	data, err := json.Marshal(&Member{Nick: "Nick", UnusualDMActivityUntil: &until})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var member Member
	if err := json.Unmarshal(data, &member); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if member.UnusualDMActivityUntil == nil || !member.UnusualDMActivityUntil.Equal(until) {
		t.Fatalf("UnusualDMActivityUntil = %v, want %v", member.UnusualDMActivityUntil, until)
	}
}
