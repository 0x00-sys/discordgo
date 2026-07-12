package discordgo

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerifyInteraction(t *testing.T) {
	pubkey, privkey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Errorf("error generating signing keypair: %s", err)
	}
	timestamp := "1608597133"

	t.Run("success", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader(body))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if !VerifyInteraction(request, pubkey) {
			t.Error("expected true, got false")
		}
		restored, err := ioutil.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("ReadAll restored body returned error: %v", err)
		}
		if string(restored) != body {
			t.Fatalf("restored body = %q, want %q", restored, body)
		}
	})

	t.Run("failure/modified body", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader("WRONG"))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if VerifyInteraction(request, pubkey) {
			t.Error("expected false, got true")
		}
	})

	t.Run("failure/modified timestamp", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader("WRONG"))
		request.Header.Set("X-Signature-Timestamp", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if VerifyInteraction(request, pubkey) {
			t.Error("expected false, got true")
		}
	})

	t.Run("failure/body too large", func(t *testing.T) {
		body := strings.Repeat("x", maxInteractionVerificationBodySize+1)
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader(body))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if VerifyInteraction(request, pubkey) {
			t.Error("expected false, got true")
		}
	})

	t.Run("failure/bad public key length", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader(body))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("VerifyInteraction panicked with invalid public key: %v", r)
			}
		}()
		if VerifyInteraction(request, ed25519.PublicKey("bad")) {
			t.Error("expected false, got true")
		}
	})
}

func TestApplicationCommandResolvedMembersLinkUsers(t *testing.T) {
	data := []byte(`{
		"users": {
			"100": {
				"id": "100",
				"username": "user",
				"discriminator": "0001"
			}
		},
		"members": {
			"100": {
				"roles": ["200"]
			}
		}
	}`)

	var resolved ApplicationCommandInteractionDataResolved
	if err := json.Unmarshal(data, &resolved); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	member := resolved.Members["100"]
	if member == nil {
		t.Fatal("resolved member was not set")
	}
	if member.User != resolved.Users["100"] {
		t.Fatalf("member user was not linked to resolved user")
	}
	if mention := member.Mention(); mention != "<@!100>" {
		t.Fatalf("Mention = %q, want %q", mention, "<@!100>")
	}
}

func TestApplicationCommandOptionMaxValueJSON(t *testing.T) {
	tests := []struct {
		name    string
		option  *ApplicationCommandOption
		present bool
		value   float64
	}{
		{
			name:   "omitted zero",
			option: &ApplicationCommandOption{MaxValue: 0},
		},
		{
			name:    "nonzero",
			option:  &ApplicationCommandOption{MaxValue: -1},
			present: true,
			value:   -1,
		},
		{
			name:    "explicit zero",
			option:  &ApplicationCommandOption{MaxValueSet: true},
			present: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.option)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}

			var fields map[string]json.RawMessage
			if err = json.Unmarshal(data, &fields); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			raw, present := fields["max_value"]
			if present != test.present {
				t.Fatalf("max_value presence = %v, want %v: %s", present, test.present, data)
			}
			if present {
				var value float64
				if err = json.Unmarshal(raw, &value); err != nil {
					t.Fatalf("Unmarshal max_value returned error: %v", err)
				}
				if value != test.value {
					t.Fatalf("max_value = %v, want %v", value, test.value)
				}
			}
		})
	}

	command := ApplicationCommand{
		Name: "limit",
		Options: []*ApplicationCommandOption{{
			Type:        ApplicationCommandOptionNumber,
			Name:        "number",
			Description: "number",
			MaxValueSet: true,
		}},
	}
	data, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("Marshal nested command returned error: %v", err)
	}
	if !bytes.Contains(data, []byte(`"max_value":0`)) {
		t.Fatalf("nested command omitted max_value: %s", data)
	}
}

func TestComponentResolvedMembersLinkUsers(t *testing.T) {
	data := []byte(`{
		"users": {
			"100": {
				"id": "100",
				"username": "user",
				"discriminator": "0001"
			}
		},
		"members": {
			"100": {
				"roles": ["200"]
			}
		}
	}`)

	var resolved ComponentInteractionDataResolved
	if err := json.Unmarshal(data, &resolved); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	member := resolved.Members["100"]
	if member == nil {
		t.Fatal("resolved member was not set")
	}
	if member.User != resolved.Users["100"] {
		t.Fatalf("member user was not linked to resolved user")
	}
	if mention := member.Mention(); mention != "<@!100>" {
		t.Fatalf("Mention = %q, want %q", mention, "<@!100>")
	}
}

func TestCurrentApplicationCommandEntryPointFields(t *testing.T) {
	encoded, err := json.Marshal(ApplicationCommand{
		Type:    PrimaryEntryPointApplicationCommand,
		Name:    "launch",
		Handler: ApplicationCommandHandlerDiscordLaunchActivity,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	if !strings.Contains(string(encoded), `"type":4`) {
		t.Fatalf("encoded command missing primary entry point type: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"handler":2`) {
		t.Fatalf("encoded command missing launch activity handler: %s", encoded)
	}

	var decoded ApplicationCommand
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Type != PrimaryEntryPointApplicationCommand {
		t.Fatalf("Type = %d, want %d", decoded.Type, PrimaryEntryPointApplicationCommand)
	}
	if decoded.Handler != ApplicationCommandHandlerDiscordLaunchActivity {
		t.Fatalf("Handler = %d, want %d", decoded.Handler, ApplicationCommandHandlerDiscordLaunchActivity)
	}
}

func TestApplicationCommandResolvedLocalizations(t *testing.T) {
	var command ApplicationCommand
	if err := json.Unmarshal([]byte(`{
		"name":"birthday",
		"name_localized":"生日",
		"description":"Wish a friend a happy birthday",
		"description_localized":"祝你朋友生日快乐",
		"options":[{
			"type":1,
			"name":"friend",
			"name_localized":"朋友",
			"description":"The friend to celebrate",
			"description_localized":"要庆祝的朋友",
			"options":[{
				"type":3,
				"name":"age",
				"name_localized":"岁数",
				"description":"Your friend's age",
				"description_localized":"你朋友的岁数",
				"choices":[{
					"name":"Adult",
					"name_localized":"成年人",
					"value":"adult"
				}]
			}]
		}]
	}`), &command); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if command.NameLocalized != "生日" || command.DescriptionLocalized != "祝你朋友生日快乐" {
		t.Fatalf("command localizations = %q, %q", command.NameLocalized, command.DescriptionLocalized)
	}
	if len(command.Options) != 1 || command.Options[0] == nil {
		t.Fatalf("Options = %#v", command.Options)
	}
	option := command.Options[0]
	if option.NameLocalized != "朋友" || option.DescriptionLocalized != "要庆祝的朋友" {
		t.Fatalf("option localizations = %q, %q", option.NameLocalized, option.DescriptionLocalized)
	}
	if len(option.Options) != 1 || option.Options[0] == nil {
		t.Fatalf("nested Options = %#v", option.Options)
	}
	nestedOption := option.Options[0]
	if nestedOption.NameLocalized != "岁数" || nestedOption.DescriptionLocalized != "你朋友的岁数" {
		t.Fatalf("nested option localizations = %q, %q", nestedOption.NameLocalized, nestedOption.DescriptionLocalized)
	}
	if len(nestedOption.Choices) != 1 || nestedOption.Choices[0] == nil {
		t.Fatalf("Choices = %#v", nestedOption.Choices)
	}
	if got := nestedOption.Choices[0].NameLocalized; got != "成年人" {
		t.Fatalf("choice NameLocalized = %q, want %q", got, "成年人")
	}

	encoded, err := json.Marshal(ApplicationCommand{Name: "birthday"})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if strings.Contains(string(encoded), "_localized") {
		t.Fatalf("empty resolved localizations were encoded: %s", encoded)
	}
}

func TestCurrentInteractionFieldsAndResponseTypes(t *testing.T) {
	var interaction Interaction
	if err := json.Unmarshal([]byte(`{
		"id":"interaction",
		"application_id":"app",
		"type":2,
		"data":{"id":"command","name":"launch","type":4},
		"guild":{"id":"guild","features":["COMMUNITY"],"locale":"en-US"},
		"channel":{"id":"channel","type":0,"guild_id":"guild","name":"general"},
		"attachment_size_limit":10485760,
		"token":"token",
		"version":1
	}`), &interaction); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if interaction.AttachmentSizeLimit != 10485760 {
		t.Fatalf("AttachmentSizeLimit = %d, want 10485760", interaction.AttachmentSizeLimit)
	}
	if interaction.Channel == nil || interaction.Channel.ID != "channel" || interaction.Channel.GuildID != "guild" || interaction.Channel.Name != "general" {
		t.Fatalf("Channel = %#v, want decoded channel metadata", interaction.Channel)
	}
	if interaction.Guild == nil || interaction.Guild.ID != "guild" || interaction.Guild.Locale != EnglishUS || len(interaction.Guild.Features) != 1 || interaction.Guild.Features[0] != GuildFeatureCommunity {
		t.Fatalf("Guild = %#v, want decoded guild metadata", interaction.Guild)
	}
	data, ok := interaction.Data.(ApplicationCommandInteractionData)
	if !ok {
		t.Fatalf("Data type = %T, want ApplicationCommandInteractionData", interaction.Data)
	}
	if data.CommandType != PrimaryEntryPointApplicationCommand {
		t.Fatalf("CommandType = %d, want %d", data.CommandType, PrimaryEntryPointApplicationCommand)
	}

	if InteractionResponsePremiumRequired != 10 {
		t.Fatalf("InteractionResponsePremiumRequired = %d, want 10", InteractionResponsePremiumRequired)
	}
	if InteractionResponseLaunchActivity != 12 {
		t.Fatalf("InteractionResponseLaunchActivity = %d, want 12", InteractionResponseLaunchActivity)
	}
}

func TestInteractionResponseUpdateMessageMarshalJSON(t *testing.T) {
	nilAttachments := []*MessageAttachment(nil)
	emptyAttachments := []*MessageAttachment{}

	tests := []struct {
		name string
		data *InteractionResponseData
		want string
	}{
		{
			name: "omits nil data",
			data: nil,
			want: `{"type":7}`,
		},
		{
			name: "omits unspecified fields",
			data: &InteractionResponseData{},
			want: `{"type":7,"data":{}}`,
		},
		{
			name: "component only",
			data: &InteractionResponseData{
				TTS:        true,
				Components: []MessageComponent{TextDisplay{Content: "updated"}},
				Poll:       &Poll{},
				Choices:    []*ApplicationCommandOptionChoice{},
				CustomID:   "ignored",
				Title:      "ignored",
			},
			want: `{"type":7,"data":{"components":[{"content":"updated","type":10}]}}`,
		},
		{
			name: "includes values and empty arrays",
			data: &InteractionResponseData{
				Content:         "updated",
				Embeds:          []*MessageEmbed{},
				AllowedMentions: &MessageAllowedMentions{Parse: []AllowedMentionType{}},
				Components:      []MessageComponent{},
				Attachments:     &emptyAttachments,
				Flags:           MessageFlagsSuppressEmbeds,
			},
			want: `{"type":7,"data":{"content":"updated","embeds":[],"allowed_mentions":{"parse":[],"replied_user":false},"components":[],"attachments":[],"flags":4}}`,
		},
		{
			name: "explicit empty and zero values",
			data: &InteractionResponseData{
				ContentSet:  true,
				Embeds:      []*MessageEmbed{},
				Components:  []MessageComponent{},
				Attachments: &emptyAttachments,
				FlagsSet:    true,
			},
			want: `{"type":7,"data":{"content":"","embeds":[],"components":[],"attachments":[],"flags":0}}`,
		},
		{
			name: "explicit null values",
			data: &InteractionResponseData{
				Content:            "ignored",
				ContentSet:         true,
				ContentNull:        true,
				EmbedsSet:          true,
				AllowedMentionsSet: true,
				ComponentsSet:      true,
				Attachments:        &nilAttachments,
				Flags:              MessageFlagsSuppressEmbeds,
				FlagsSet:           true,
				FlagsNull:          true,
			},
			want: `{"type":7,"data":{"content":null,"embeds":null,"allowed_mentions":null,"components":null,"attachments":null,"flags":null}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(InteractionResponse{
				Type: InteractionResponseUpdateMessage,
				Data: tt.data,
			})
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func TestInteractionResponseUpdateMessageMarshalJSONError(t *testing.T) {
	_, err := json.Marshal(InteractionResponse{
		Type: InteractionResponseUpdateMessage,
		Data: &InteractionResponseData{
			Components: []MessageComponent{unknownMessageComponent{
				ComponentType: TextDisplayComponent,
				RawData:       json.RawMessage(`{`),
			}},
		},
	})
	if err == nil {
		t.Fatal("json.Marshal returned nil error for invalid component JSON")
	}
}

func TestInteractionResponseMarshalJSONPreservesOtherTypes(t *testing.T) {
	data := &InteractionResponseData{
		TTS:                true,
		Content:            "content",
		ContentSet:         true,
		ContentNull:        true,
		Components:         []MessageComponent{},
		ComponentsSet:      true,
		Embeds:             []*MessageEmbed{},
		EmbedsSet:          true,
		AllowedMentionsSet: true,
		Flags:              MessageFlagsSuppressEmbeds,
		FlagsSet:           true,
		FlagsNull:          true,
	}
	types := []InteractionResponseType{
		InteractionResponsePong,
		InteractionResponseChannelMessageWithSource,
		InteractionResponseDeferredChannelMessageWithSource,
		InteractionResponseDeferredMessageUpdate,
		InteractionApplicationCommandAutocompleteResult,
		InteractionResponseModal,
		InteractionResponsePremiumRequired,
		InteractionResponseLaunchActivity,
	}

	for _, responseType := range types {
		t.Run(strconv.Itoa(int(responseType)), func(t *testing.T) {
			got, err := json.Marshal(InteractionResponse{
				Type: responseType,
				Data: data,
			})
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}

			want := `{"type":` + strconv.Itoa(int(responseType)) + `,"data":{"tts":true,"content":"content","components":[],"embeds":[],"flags":4}}`
			if string(got) != want {
				t.Fatalf("json.Marshal = %s, want %s", got, want)
			}
		})
	}
}
