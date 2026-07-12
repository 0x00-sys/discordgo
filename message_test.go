package discordgo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestMessageAttachmentCurrentFieldsAndFlags(t *testing.T) {
	data := []byte(`{
		"id":"attachment",
		"filename":"clip.mp4",
		"title":"clip",
		"description":"alt text",
		"content_type":"video/mp4",
		"size":42,
		"url":"https://cdn.example/clip.mp4",
		"proxy_url":"https://proxy.example/clip.mp4",
		"height":720,
		"width":1280,
		"placeholder":"thumbhash",
		"placeholder_version":1,
		"ephemeral":true,
		"duration_secs":5.2,
		"waveform":"wave",
		"flags":9,
		"clip_participants":[{"id":"user","username":"User"}],
		"clip_created_at":"2026-06-24T12:00:00Z",
		"application":{"id":"app","name":"App","description":"desc","verify_key":"key"}
	}`)

	var attachment MessageAttachment
	if err := json.Unmarshal(data, &attachment); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if attachment.Title != "clip" || attachment.Description != "alt text" {
		t.Fatalf("attachment title/description = %q/%q", attachment.Title, attachment.Description)
	}
	if attachment.Placeholder != "thumbhash" || attachment.PlaceholderVersion != 1 {
		t.Fatalf("placeholder fields = %q/%d", attachment.Placeholder, attachment.PlaceholderVersion)
	}
	wantFlags := MessageAttachmentFlagsIsClip | MessageAttachmentFlagsIsSpoiler
	if attachment.Flags != wantFlags {
		t.Fatalf("Flags = %d, want %d", attachment.Flags, wantFlags)
	}
	if len(attachment.ClipParticipants) != 1 || attachment.ClipParticipants[0].ID != "user" {
		t.Fatalf("ClipParticipants = %#v", attachment.ClipParticipants)
	}
	if attachment.ClipCreatedAt == nil || attachment.ClipCreatedAt.Year() != 2026 {
		t.Fatalf("ClipCreatedAt = %v", attachment.ClipCreatedAt)
	}
	if attachment.Application == nil || attachment.Application.ID != "app" {
		t.Fatalf("Application = %#v", attachment.Application)
	}

	request := MessageAttachment{
		ID:          "0",
		Filename:    "spoiler.png",
		Description: "updated alt",
		IsSpoiler:   true,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	encodedText := string(encoded)
	if !strings.Contains(encodedText, `"description":"updated alt"`) || !strings.Contains(encodedText, `"is_spoiler":true`) {
		t.Fatalf("attachment request JSON missing edit fields: %s", encodedText)
	}
}

func TestCurrentMessageTypeAndFlagConstants(t *testing.T) {
	tests := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"MessageTypePollResult", MessageTypePollResult, MessageType(46)},
		{"MessageTypePurchaseNotification", MessageTypePurchaseNotification, MessageType(44)},
		{"MessageTypeGuildIncidentReportRaid", MessageTypeGuildIncidentReportRaid, MessageType(38)},
		{"MessageFlagsHasSnapshot", MessageFlagsHasSnapshot, MessageFlags(1 << 14)},
		{"MessageAttachmentFlagsIsSpoiler", MessageAttachmentFlagsIsSpoiler, MessageAttachmentFlags(1 << 3)},
		{"MessageAttachmentFlagsIsAnimated", MessageAttachmentFlagsIsAnimated, MessageAttachmentFlags(1 << 5)},
		{"ReactionTypeNormal", ReactionTypeNormal, ReactionType(0)},
		{"ReactionTypeBurst", ReactionTypeBurst, ReactionType(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestMessageReactionsCurrentFields(t *testing.T) {
	tests := []struct {
		name             string
		data             string
		wantCountDetails MessageReactionCountDetails
		wantMeBurst      bool
		wantBurstColors  []string
	}{
		{
			name:             "burst reaction",
			data:             `{"count":3,"count_details":{"burst":2,"normal":1},"me":true,"me_burst":true,"emoji":{"name":"sparkle"},"burst_colors":["#ff00ff","#00ffff"]}`,
			wantCountDetails: MessageReactionCountDetails{Burst: 2, Normal: 1},
			wantMeBurst:      true,
			wantBurstColors:  []string{"#ff00ff", "#00ffff"},
		},
		{
			name: "omitted burst fields",
			data: `{"count":1,"me":false,"emoji":{"name":"wave"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reaction MessageReactions
			if err := json.Unmarshal([]byte(tt.data), &reaction); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if reaction.CountDetails != tt.wantCountDetails {
				t.Fatalf("CountDetails = %#v, want %#v", reaction.CountDetails, tt.wantCountDetails)
			}
			if reaction.MeBurst != tt.wantMeBurst {
				t.Fatalf("MeBurst = %t, want %t", reaction.MeBurst, tt.wantMeBurst)
			}
			if len(reaction.BurstColors) != len(tt.wantBurstColors) {
				t.Fatalf("BurstColors = %#v, want %#v", reaction.BurstColors, tt.wantBurstColors)
			}
			for i, color := range tt.wantBurstColors {
				if reaction.BurstColors[i] != color {
					t.Fatalf("BurstColors[%d] = %q, want %q", i, reaction.BurstColors[i], color)
				}
			}
		})
	}
}

func TestMessageInteractionMetadataCommandTargets(t *testing.T) {
	tests := []struct {
		name                string
		data                string
		wantTargetUserID    string
		wantTargetMessageID string
	}{
		{
			name:                "application command targets",
			data:                `{"id":"interaction","type":2,"user":{"id":"invoker"},"authorizing_integration_owners":{"0":"guild"},"target_user":{"id":"target"},"target_message_id":"message"}`,
			wantTargetUserID:    "target",
			wantTargetMessageID: "message",
		},
		{
			name: "targets omitted",
			data: `{"id":"interaction","type":3,"user":{"id":"invoker"},"authorizing_integration_owners":{"0":"guild"},"interacted_message_id":"source"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var metadata MessageInteractionMetadata
			if err := json.Unmarshal([]byte(tt.data), &metadata); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			gotTargetUserID := ""
			if metadata.TargetUser != nil {
				gotTargetUserID = metadata.TargetUser.ID
			}
			if gotTargetUserID != tt.wantTargetUserID || metadata.TargetMessageID != tt.wantTargetMessageID {
				t.Fatalf("targets = %q/%q, want %q/%q", gotTargetUserID, metadata.TargetMessageID, tt.wantTargetUserID, tt.wantTargetMessageID)
			}
		})
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
	if m.ChannelType != 0 {
		t.Fatalf("ChannelType = %d, want zero when omitted", m.ChannelType)
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
		"channel_type":11,
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
	if m.ChannelType != ChannelTypeGuildPublicThread {
		t.Fatalf("ChannelType = %d, want %d", m.ChannelType, ChannelTypeGuildPublicThread)
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
		"channel_type":2,
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
	if m.ChannelType != ChannelTypeGuildVoice {
		t.Fatalf("ChannelType = %d, want %d", m.ChannelType, ChannelTypeGuildVoice)
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

func TestMessageSharedClientThemeJSON(t *testing.T) {
	data := []byte(`{"id":"1","shared_client_theme":{"colors":["5865F2","7258F2","9858F2"],"gradient_angle":45,"base_mix":58,"base_theme":1}}`)

	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.SharedClientTheme == nil {
		t.Fatal("SharedClientTheme = nil")
	}
	theme := message.SharedClientTheme
	if len(theme.Colors) != 3 || theme.Colors[0] != "5865F2" || theme.GradientAngle != 45 || theme.BaseMix != 58 {
		t.Fatalf("SharedClientTheme = %#v", theme)
	}
	if theme.BaseTheme == nil || *theme.BaseTheme != BaseThemeTypeDark {
		t.Fatalf("BaseTheme = %#v, want %d", theme.BaseTheme, BaseThemeTypeDark)
	}

	payload, err := json.Marshal(&MessageSend{SharedClientTheme: theme})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{`"shared_client_theme":`, `"colors":["5865F2","7258F2","9858F2"]`, `"gradient_angle":45`, `"base_mix":58`, `"base_theme":1`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("MessageSend JSON = %s, want %s", payload, want)
		}
	}
}

func TestMessageSharedClientThemeNullableFields(t *testing.T) {
	var message Message
	if err := json.Unmarshal([]byte(`{"shared_client_theme":{"colors":[],"gradient_angle":0,"base_mix":0,"base_theme":null}}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.SharedClientTheme == nil {
		t.Fatal("SharedClientTheme = nil")
	}
	if message.SharedClientTheme.BaseTheme != nil {
		t.Fatalf("BaseTheme = %#v, want nil", message.SharedClientTheme.BaseTheme)
	}

	payload, err := json.Marshal(&MessageSend{})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(payload), `"shared_client_theme"`) {
		t.Fatalf("MessageSend JSON = %s, want shared_client_theme omitted", payload)
	}
}

func TestMessageCallJSON(t *testing.T) {
	data := []byte(`{"id":"1","call":{"participants":["111","222"],"ended_timestamp":"2026-07-10T08:30:45.123Z"}}`)

	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Call == nil {
		t.Fatal("Call = nil")
	}
	if len(message.Call.Participants) != 2 || message.Call.Participants[0] != "111" || message.Call.Participants[1] != "222" {
		t.Fatalf("Participants = %#v, want [111 222]", message.Call.Participants)
	}
	wantEndedTimestamp := time.Date(2026, time.July, 10, 8, 30, 45, 123000000, time.UTC)
	if message.Call.EndedTimestamp == nil || !message.Call.EndedTimestamp.Equal(wantEndedTimestamp) {
		t.Fatalf("EndedTimestamp = %#v, want %s", message.Call.EndedTimestamp, wantEndedTimestamp)
	}
}

func TestMessageCallNullableFields(t *testing.T) {
	var message Message
	if err := json.Unmarshal([]byte(`{"call":{"participants":[],"ended_timestamp":null}}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Call == nil {
		t.Fatal("Call = nil")
	}
	if message.Call.Participants == nil || len(message.Call.Participants) != 0 {
		t.Fatalf("Participants = %#v, want empty non-nil slice", message.Call.Participants)
	}
	if message.Call.EndedTimestamp != nil {
		t.Fatalf("EndedTimestamp = %#v, want nil", message.Call.EndedTimestamp)
	}

	if err := json.Unmarshal([]byte(`{"call":{"participants":["111"]}}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Call == nil || len(message.Call.Participants) != 1 || message.Call.Participants[0] != "111" {
		t.Fatalf("Call = %#v, want participant 111", message.Call)
	}
	if message.Call.EndedTimestamp != nil {
		t.Fatalf("missing EndedTimestamp = %#v, want nil", message.Call.EndedTimestamp)
	}

	if err := json.Unmarshal([]byte(`{"call":null}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Call != nil {
		t.Fatalf("Call = %#v, want nil", message.Call)
	}

	if err := json.Unmarshal([]byte(`{}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Call != nil {
		t.Fatalf("Call = %#v, want nil", message.Call)
	}
}

func TestMessageApplicationIDJSON(t *testing.T) {
	var message Message
	if err := json.Unmarshal([]byte(`{"application_id":"1234567890123456789"}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.ApplicationID != "1234567890123456789" {
		t.Fatalf("ApplicationID = %q, want %q", message.ApplicationID, "1234567890123456789")
	}

	if err := json.Unmarshal([]byte(`{"application_id":null}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.ApplicationID != "" {
		t.Fatalf("ApplicationID = %q after null, want empty", message.ApplicationID)
	}

	message.ApplicationID = "stale"
	if err := json.Unmarshal([]byte(`{}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.ApplicationID != "" {
		t.Fatalf("ApplicationID = %q after omission, want empty", message.ApplicationID)
	}
}

func TestMessagePositionJSON(t *testing.T) {
	var message Message
	if err := json.Unmarshal([]byte(`{"position":0}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Position == nil || *message.Position != 0 {
		t.Fatalf("Position = %#v, want pointer to 0", message.Position)
	}

	if err := json.Unmarshal([]byte(`{"position":42}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Position == nil || *message.Position != 42 {
		t.Fatalf("Position = %#v, want pointer to 42", message.Position)
	}

	if err := json.Unmarshal([]byte(`{"position":null}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Position != nil {
		t.Fatalf("Position = %#v after null, want nil", message.Position)
	}

	stale := 7
	message.Position = &stale
	if err := json.Unmarshal([]byte(`{}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Position != nil {
		t.Fatalf("Position = %#v after omission, want nil", message.Position)
	}
}

func TestMessageRoleSubscriptionDataJSON(t *testing.T) {
	data := []byte(`{"type":25,"role_subscription_data":{"role_subscription_listing_id":"1234567890123456789","tier_name":"Premium","total_months_subscribed":18,"is_renewal":true}}`)

	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.RoleSubscriptionData == nil {
		t.Fatal("RoleSubscriptionData = nil")
	}
	roleSubscription := message.RoleSubscriptionData
	if roleSubscription.RoleSubscriptionListingID != "1234567890123456789" || roleSubscription.TierName != "Premium" || roleSubscription.TotalMonthsSubscribed != 18 || !roleSubscription.IsRenewal {
		t.Fatalf("RoleSubscriptionData = %#v", roleSubscription)
	}

	if err := json.Unmarshal([]byte(`{"role_subscription_data":{"role_subscription_listing_id":"111","tier_name":"Starter","total_months_subscribed":0,"is_renewal":false}}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.RoleSubscriptionData == nil || message.RoleSubscriptionData.TotalMonthsSubscribed != 0 || message.RoleSubscriptionData.IsRenewal {
		t.Fatalf("zero-value RoleSubscriptionData = %#v", message.RoleSubscriptionData)
	}
}

func TestMessageRoleSubscriptionDataNullableFields(t *testing.T) {
	message := Message{RoleSubscriptionData: &MessageRoleSubscriptionData{RoleSubscriptionListingID: "stale"}}
	if err := json.Unmarshal([]byte(`{"role_subscription_data":null}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.RoleSubscriptionData != nil {
		t.Fatalf("RoleSubscriptionData = %#v after null, want nil", message.RoleSubscriptionData)
	}

	message.RoleSubscriptionData = &MessageRoleSubscriptionData{RoleSubscriptionListingID: "stale"}
	if err := json.Unmarshal([]byte(`{}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.RoleSubscriptionData != nil {
		t.Fatalf("RoleSubscriptionData = %#v after omission, want nil", message.RoleSubscriptionData)
	}
}

func TestMessageResolvedDataJSON(t *testing.T) {
	data := []byte(`{
		"resolved":{
			"users":{"100":{"id":"100","username":"User"}},
			"members":{"100":{"nick":"Nick","roles":["200"],"permissions":"8"}},
			"roles":{"200":{"id":"200","name":"Role","permissions":"8"}},
			"channels":{"300":{"id":"300","name":"channel","type":0,"permissions":"8"}},
			"attachments":{"400":{"id":"400","filename":"file.txt","size":1,"url":"https://example.com/file.txt","proxy_url":"https://example.com/file.txt"}}
		}
	}`)

	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Resolved == nil {
		t.Fatal("Resolved = nil")
	}
	resolved := message.Resolved
	if resolved.Users["100"] == nil || resolved.Users["100"].ID != "100" {
		t.Fatalf("resolved users = %#v", resolved.Users)
	}
	if resolved.Members["100"] == nil || resolved.Members["100"].User != resolved.Users["100"] {
		t.Fatalf("resolved member user = %#v, want linked user", resolved.Members["100"])
	}
	if resolved.Roles["200"] == nil || resolved.Roles["200"].ID != "200" {
		t.Fatalf("resolved roles = %#v", resolved.Roles)
	}
	if resolved.Channels["300"] == nil || resolved.Channels["300"].ID != "300" {
		t.Fatalf("resolved channels = %#v", resolved.Channels)
	}
	if resolved.Attachments["400"] == nil || resolved.Attachments["400"].ID != "400" {
		t.Fatalf("resolved attachments = %#v", resolved.Attachments)
	}
}

func TestMessageResolvedDataNullableFields(t *testing.T) {
	message := Message{Resolved: &ComponentInteractionDataResolved{Users: map[string]*User{"stale": {ID: "stale"}}}}
	if err := json.Unmarshal([]byte(`{"resolved":null}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Resolved != nil {
		t.Fatalf("Resolved = %#v after null, want nil", message.Resolved)
	}

	message.Resolved = &ComponentInteractionDataResolved{Users: map[string]*User{"stale": {ID: "stale"}}}
	if err := json.Unmarshal([]byte(`{}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Resolved != nil {
		t.Fatalf("Resolved = %#v after omission, want nil", message.Resolved)
	}
}

func TestMessageLegacyStickersJSON(t *testing.T) {
	data := []byte(`{
		"sticker_items":[{"id":"item","name":"Wave","format_type":1}],
		"stickers":[{"id":"legacy","name":"Wave","description":null,"tags":"wave","type":1,"format_type":1}]
	}`)

	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(message.StickerItems) != 1 || message.StickerItems[0] == nil || message.StickerItems[0].ID != "item" {
		t.Fatalf("StickerItems = %#v", message.StickerItems)
	}
	if len(message.Stickers) != 1 || message.Stickers[0] == nil || message.Stickers[0].ID != "legacy" || message.Stickers[0].Type != StickerTypeStandard {
		t.Fatalf("Stickers = %#v", message.Stickers)
	}
}

func TestMessageLegacyStickersNullableFields(t *testing.T) {
	message := Message{Stickers: []*Sticker{{ID: "stale"}}}
	if err := json.Unmarshal([]byte(`{"stickers":null}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Stickers != nil {
		t.Fatalf("Stickers = %#v after null, want nil", message.Stickers)
	}

	message.Stickers = []*Sticker{{ID: "stale"}}
	if err := json.Unmarshal([]byte(`{}`), &message); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if message.Stickers != nil {
		t.Fatalf("Stickers = %#v after omission, want nil", message.Stickers)
	}
}

func TestBaseThemeTypeValues(t *testing.T) {
	tests := []struct {
		theme BaseThemeType
		want  int
	}{
		{theme: BaseThemeTypeUnset, want: 0},
		{theme: BaseThemeTypeDark, want: 1},
		{theme: BaseThemeTypeLight, want: 2},
		{theme: BaseThemeTypeDarker, want: 3},
		{theme: BaseThemeTypeMidnight, want: 4},
	}

	for _, tt := range tests {
		if got := int(tt.theme); got != tt.want {
			t.Errorf("theme = %d, want %d", got, tt.want)
		}
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
