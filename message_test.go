package discordgo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContentWithMoreMentionsReplaced(t *testing.T) {
	s := &Session{StateEnabled: true, State: NewState()}

	user := &User{
		ID:       "user",
		Username: "User Name",
	}

	s.State.GuildAdd(&Guild{ID: "guild"})
	s.State.RoleAdd("guild", &Role{
		ID:          "role",
		Name:        "Role Name",
		Mentionable: true,
	})
	s.State.MemberAdd(&Member{
		User:    user,
		Nick:    "User Nick",
		GuildID: "guild",
	})
	s.State.ChannelAdd(&Channel{
		Name:    "Channel Name",
		GuildID: "guild",
		ID:      "channel",
	})
	m := &Message{
		Content:      "<@&role> <@!user> <@user> <#channel>",
		ChannelID:    "channel",
		MentionRoles: []string{"role"},
		Mentions:     []*User{user},
	}
	if result, _ := m.ContentWithMoreMentionsReplaced(s); result != "@Role Name @User Nick @User Name #Channel Name" {
		t.Error(result)
	}
}
func TestGettingEmojisFromMessage(t *testing.T) {
	msg := "test test <:kitty14:811736565172011058> <:kitty4:811736468812595260>"
	m := &Message{
		Content: msg,
	}
	emojis := m.GetCustomEmojis()
	if len(emojis) < 1 {
		t.Error("No emojis found.")
		return
	}

}

func TestMessage_Reference(t *testing.T) {
	m := &Message{
		ID:        "811736565172011001",
		GuildID:   "811736565172011002",
		ChannelID: "811736565172011003",
	}

	ref := m.Reference()

	if ref.Type != 0 {
		t.Error("Default reference type should be 0")
	}

	if ref.MessageID != m.ID {
		t.Error("Message ID should be the same")
	}

	if ref.GuildID != m.GuildID {
		t.Error("Guild ID should be the same")
	}

	if ref.ChannelID != m.ChannelID {
		t.Error("Channel ID should be the same")
	}
}

func TestMessage_Forward(t *testing.T) {
	m := &Message{
		ID:        "811736565172011001",
		GuildID:   "811736565172011002",
		ChannelID: "811736565172011003",
	}

	ref := m.Forward()

	if ref.Type != MessageReferenceTypeForward {
		t.Error("Reference type should be 1 (forward)")
	}

	if ref.MessageID != m.ID {
		t.Error("Message ID should be the same")
	}

	if ref.GuildID != m.GuildID {
		t.Error("Guild ID should be the same")
	}

	if ref.ChannelID != m.ChannelID {
		t.Error("Channel ID should be the same")
	}
}

func TestMessageReference_DefaultTypeIsDefault(t *testing.T) {
	r := MessageReference{}
	if r.Type != MessageReferenceTypeDefault {
		t.Error("Default message type should be MessageReferenceTypeDefault")
	}
}

func TestMessageCreateUnknownComponentType(t *testing.T) {
	var m MessageCreate
	err := json.Unmarshal([]byte(`{
		"id":"message",
		"channel_id":"channel",
		"content":"content",
		"components":[{"type":20,"id":1,"custom":"value"}]
	}`), &m)
	if err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if m.Message == nil {
		t.Fatal("Message is nil")
	}
	if len(m.Components) != 1 {
		t.Fatalf("len(Components) = %d, want 1", len(m.Components))
	}
	if m.Components[0].Type() != ComponentType(20) {
		t.Fatalf("component type = %d, want 20", m.Components[0].Type())
	}

	raw, err := m.Components[0].MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var component struct {
		Type   ComponentType `json:"type"`
		ID     int           `json:"id"`
		Custom string        `json:"custom"`
	}
	if err := json.Unmarshal(raw, &component); err != nil {
		t.Fatalf("json.Unmarshal component returned error: %v", err)
	}
	if component.Type != ComponentType(20) || component.ID != 1 || component.Custom != "value" {
		t.Fatalf("component = %#v, want type 20 with original fields", component)
	}
}

func TestMessageCreateLinksMemberUser(t *testing.T) {
	var m MessageCreate
	err := json.Unmarshal([]byte(`{
		"id":"message",
		"channel_id":"channel",
		"guild_id":"guild",
		"author":{"id":"user","username":"User"},
		"member":{"roles":["role"],"nick":"Nick"}
	}`), &m)
	if err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if m.Member == nil {
		t.Fatal("Member is nil")
	}
	if m.Member.User != m.Author {
		t.Fatal("Member.User was not linked to Author")
	}
	if m.Member.GuildID != "guild" {
		t.Fatalf("Member.GuildID = %q, want guild", m.Member.GuildID)
	}
	if mention := m.Member.Mention(); mention != "<@!user>" {
		t.Fatalf("Member.Mention() = %q, want %q", mention, "<@!user>")
	}
	if displayName := m.Member.DisplayName(); displayName != "Nick" {
		t.Fatalf("Member.DisplayName() = %q, want Nick", displayName)
	}
}

func TestMessageUpdateLinksMemberUser(t *testing.T) {
	var m MessageUpdate
	err := json.Unmarshal([]byte(`{
		"id":"message",
		"channel_id":"channel",
		"guild_id":"guild",
		"author":{"id":"user","username":"User"},
		"member":{"roles":["role"]}
	}`), &m)
	if err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if m.Member == nil {
		t.Fatal("Member is nil")
	}
	if m.Member.User != m.Author {
		t.Fatal("Member.User was not linked to Author")
	}
	if displayName := m.Member.DisplayName(); displayName != "User" {
		t.Fatalf("Member.DisplayName() = %q, want User", displayName)
	}
}

func TestMessageSendNonce(t *testing.T) {
	payload, err := Marshal(&MessageSend{
		Content:      "hello",
		Nonce:        "ticket-123",
		EnforceNonce: true,
	})
	if err != nil {
		t.Fatalf("Marshal() returned error: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal() returned error: %v", err)
	}
	if got["nonce"] != "ticket-123" {
		t.Fatalf("nonce = %v, want %q", got["nonce"], "ticket-123")
	}
	if got["enforce_nonce"] != true {
		t.Fatalf("enforce_nonce = %v, want true", got["enforce_nonce"])
	}
}

func TestMessageMarshalJSONIncludesComponents(t *testing.T) {
	m := &Message{
		ID:        "811736565172011001",
		ChannelID: "811736565172011003",
		Components: []MessageComponent{
			ActionsRow{
				Components: []MessageComponent{
					Button{
						Label:    "Open",
						Style:    PrimaryButton,
						CustomID: "open-ticket",
					},
				},
			},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var unmarshaled Message
	if err = json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(unmarshaled.Components) != 1 {
		t.Fatalf("Components len = %d, want 1", len(unmarshaled.Components))
	}

	row, ok := unmarshaled.Components[0].(*ActionsRow)
	if !ok {
		t.Fatalf("Component type = %T, want *ActionsRow", unmarshaled.Components[0])
	}
	if len(row.Components) != 1 {
		t.Fatalf("row.Components len = %d, want 1", len(row.Components))
	}

	button, ok := row.Components[0].(*Button)
	if !ok {
		t.Fatalf("row component type = %T, want *Button", row.Components[0])
	}
	if button.CustomID != "open-ticket" {
		t.Fatalf("button.CustomID = %q, want open-ticket", button.CustomID)
	}
}

func TestMessageNonceUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "large integer nonce", data: `{"id":"1","nonce":1234567890123456789}`, want: "1234567890123456789"},
		{name: "string nonce", data: `{"id":"1","nonce":"ticket-123"}`, want: "ticket-123"},
		{name: "missing nonce", data: `{"id":"1"}`, want: ""},
		{name: "null nonce", data: `{"id":"1","nonce":null}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Message
			if err := json.Unmarshal([]byte(tt.data), &m); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if m.Nonce != tt.want {
				t.Fatalf("Nonce = %q, want %q", m.Nonce, tt.want)
			}
		})
	}
}

func TestMessageNonceMarshalRoundTrip(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"id":"1","nonce":1234567890123456789}`), &m); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	data, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"nonce":"1234567890123456789"`) {
		t.Fatalf("Marshal() = %s, want lossless nonce", data)
	}
}

func TestMessageValueMarshalJSONIncludesComponents(t *testing.T) {
	m := Message{
		ID: "811736565172011001",
		Components: []MessageComponent{
			ActionsRow{
				Components: []MessageComponent{
					Button{
						Label:    "Open",
						Style:    PrimaryButton,
						CustomID: "open-ticket",
					},
				},
			},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"custom_id":"open-ticket"`) {
		t.Fatalf("Marshal() = %s, want components included", data)
	}
}

func TestEventStructsMarshalWithNilMessage(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{name: "message create", event: &MessageCreate{}},
		{name: "message update", event: &MessageUpdate{}},
		{name: "message delete", event: &MessageDelete{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Marshal panicked: %v", r)
				}
			}()

			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(data) != "{}" {
				t.Fatalf("Marshal() = %s, want {}", data)
			}
		})
	}
}
