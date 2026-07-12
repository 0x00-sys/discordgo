package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestApplicationWebhookConstants(t *testing.T) {
	webhookTypes := []struct {
		name string
		got  ApplicationWebhookType
		want ApplicationWebhookType
	}{
		{"ping", ApplicationWebhookTypePing, 0},
		{"event", ApplicationWebhookTypeEvent, 1},
	}
	for _, tt := range webhookTypes {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("ApplicationWebhookType = %d, want %d", tt.got, tt.want)
			}
		})
	}

	eventTypes := []struct {
		name string
		got  ApplicationWebhookEventType
		want ApplicationWebhookEventType
	}{
		{"application authorized", ApplicationWebhookEventTypeApplicationAuthorized, "APPLICATION_AUTHORIZED"},
		{"application deauthorized", ApplicationWebhookEventTypeApplicationDeauthorized, "APPLICATION_DEAUTHORIZED"},
		{"entitlement create", ApplicationWebhookEventTypeEntitlementCreate, "ENTITLEMENT_CREATE"},
		{"entitlement update", ApplicationWebhookEventTypeEntitlementUpdate, "ENTITLEMENT_UPDATE"},
		{"entitlement delete", ApplicationWebhookEventTypeEntitlementDelete, "ENTITLEMENT_DELETE"},
		{"quest user enrollment", ApplicationWebhookEventTypeQuestUserEnrollment, "QUEST_USER_ENROLLMENT"},
		{"lobby message create", ApplicationWebhookEventTypeLobbyMessageCreate, "LOBBY_MESSAGE_CREATE"},
		{"lobby message update", ApplicationWebhookEventTypeLobbyMessageUpdate, "LOBBY_MESSAGE_UPDATE"},
		{"lobby message delete", ApplicationWebhookEventTypeLobbyMessageDelete, "LOBBY_MESSAGE_DELETE"},
		{"game direct message create", ApplicationWebhookEventTypeGameDirectMessageCreate, "GAME_DIRECT_MESSAGE_CREATE"},
		{"game direct message update", ApplicationWebhookEventTypeGameDirectMessageUpdate, "GAME_DIRECT_MESSAGE_UPDATE"},
		{"game direct message delete", ApplicationWebhookEventTypeGameDirectMessageDelete, "GAME_DIRECT_MESSAGE_DELETE"},
	}
	for _, tt := range eventTypes {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("ApplicationWebhookEventType = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestWebhookCurrentFields(t *testing.T) {
	data := []byte(`{
		"id":"webhook",
		"type":2,
		"guild_id":"guild",
		"channel_id":"channel",
		"application_id":null,
		"source_guild":{"id":"source-guild","name":"Source Guild","icon":"icon"},
		"source_channel":{"id":"source-channel","name":"announcements"},
		"url":"https://discord.com/api/webhooks/webhook/token"
	}`)
	var webhook Webhook
	if err := json.Unmarshal(data, &webhook); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if webhook.Type != WebhookTypeChannelFollower || webhook.SourceGuild == nil || webhook.SourceGuild.ID != "source-guild" || webhook.SourceGuild.Name != "Source Guild" {
		t.Fatalf("SourceGuild = %#v", webhook.SourceGuild)
	}
	if webhook.SourceChannel == nil || webhook.SourceChannel.ID != "source-channel" || webhook.SourceChannel.Name != "announcements" {
		t.Fatalf("SourceChannel = %#v", webhook.SourceChannel)
	}
	if webhook.URL != "https://discord.com/api/webhooks/webhook/token" {
		t.Fatalf("URL = %q", webhook.URL)
	}
	if WebhookTypeApplication != 3 {
		t.Fatalf("WebhookTypeApplication = %d, want 3", WebhookTypeApplication)
	}
}

func TestApplicationWebhookEventTypes(t *testing.T) {
	tests := []struct {
		name      string
		eventType ApplicationWebhookEventType
		data      string
		check     func(*testing.T, *ApplicationWebhookEventBody)
	}{
		{
			name:      "application authorized",
			eventType: ApplicationWebhookEventTypeApplicationAuthorized,
			data:      `{"integration_type":0,"user":{"id":"user","username":"User"},"scopes":["applications.commands","identify"],"guild":{"id":"guild","name":"Guild"}}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventApplicationAuthorizedData)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.IntegrationType == nil || *data.IntegrationType != ApplicationIntegrationGuildInstall {
					t.Fatalf("IntegrationType = %v", data.IntegrationType)
				}
				if data.User == nil || data.User.ID != "user" {
					t.Fatalf("User = %#v", data.User)
				}
				if len(data.Scopes) != 2 || data.Scopes[1] != "identify" {
					t.Fatalf("Scopes = %#v", data.Scopes)
				}
				if data.Guild == nil || data.Guild.ID != "guild" {
					t.Fatalf("Guild = %#v", data.Guild)
				}
			},
		},
		{
			name:      "application deauthorized",
			eventType: ApplicationWebhookEventTypeApplicationDeauthorized,
			data:      `{"user":{"id":"user","username":"User"}}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventApplicationDeauthorizedData)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.User == nil || data.User.ID != "user" {
					t.Fatalf("User = %#v", data.User)
				}
			},
		},
		{
			name:      "entitlement create",
			eventType: ApplicationWebhookEventTypeEntitlementCreate,
			data:      `{"id":"entitlement-create","sku_id":"sku","application_id":"application","user_id":"user","type":4,"deleted":false,"starts_at":null,"ends_at":null,"consumed":false}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*Entitlement)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.ID != "entitlement-create" || data.Type != EntitlementTypeTestModePurchase || data.Deleted {
					t.Fatalf("Entitlement = %#v", data)
				}
				if data.Consumed == nil || *data.Consumed {
					t.Fatalf("Consumed = %v", data.Consumed)
				}
			},
		},
		{
			name:      "entitlement update",
			eventType: ApplicationWebhookEventTypeEntitlementUpdate,
			data:      `{"id":"entitlement-update","sku_id":"sku","application_id":"application","guild_id":"guild","type":8,"deleted":false,"starts_at":"2026-07-01T00:00:00Z","ends_at":"2026-08-01T00:00:00Z"}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*Entitlement)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.ID != "entitlement-update" || data.GuildID != "guild" || data.EndsAt == nil {
					t.Fatalf("Entitlement = %#v", data)
				}
			},
		},
		{
			name:      "entitlement delete",
			eventType: ApplicationWebhookEventTypeEntitlementDelete,
			data:      `{"id":"entitlement-delete","sku_id":"sku","application_id":"application","user_id":"user","type":1,"deleted":true,"starts_at":null,"ends_at":null}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*Entitlement)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.ID != "entitlement-delete" || !data.Deleted {
					t.Fatalf("Entitlement = %#v", data)
				}
			},
		},
		{
			name:      "quest user enrollment",
			eventType: ApplicationWebhookEventTypeQuestUserEnrollment,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				if event.Struct != nil || event.RawData != nil {
					t.Fatalf("quest data = %T/%s, want nil", event.Struct, event.RawData)
				}
			},
		},
		{
			name:      "lobby message create",
			eventType: ApplicationWebhookEventTypeLobbyMessageCreate,
			data:      `{"id":"lobby-message","type":0,"content":"welcome","lobby_id":"lobby","channel_id":"channel","author":{"id":"user"},"metadata":{"party":"raid"},"moderation_metadata":{"verdict":"allow"},"flags":65536,"application_id":"application"}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventMessage)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.Message == nil || data.ID != "lobby-message" || data.LobbyID != "lobby" {
					t.Fatalf("Message = %#v", data)
				}
				if data.Author == nil || data.Author.ID != "user" || data.Flags != MessageFlags(65536) {
					t.Fatalf("message author/flags = %#v/%d", data.Author, data.Flags)
				}
				if data.Metadata["party"] != "raid" || data.ModerationMetadata["verdict"] != "allow" {
					t.Fatalf("message metadata = %#v/%#v", data.Metadata, data.ModerationMetadata)
				}
			},
		},
		{
			name:      "lobby message update",
			eventType: ApplicationWebhookEventTypeLobbyMessageUpdate,
			data:      `{"id":"lobby-message","type":0,"content":"updated","lobby_id":"lobby","channel_id":"channel","author":{"id":"user"},"edited_timestamp":"2025-08-05T20:39:19.557970+00:00","flags":0,"timestamp":"2025-08-05T20:38:43.660000+00:00"}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventMessage)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.Content != "updated" || data.Timestamp.IsZero() || data.EditedTimestamp == nil {
					t.Fatalf("updated message = %#v", data.Message)
				}
			},
		},
		{
			name:      "lobby message delete",
			eventType: ApplicationWebhookEventTypeLobbyMessageDelete,
			data:      `{"id":"lobby-message","lobby_id":"lobby"}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventLobbyMessageDeleteData)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.ID != "lobby-message" || data.LobbyID != "lobby" {
					t.Fatalf("delete data = %#v", data)
				}
			},
		},
		{
			name:      "game direct message create",
			eventType: ApplicationWebhookEventTypeGameDirectMessageCreate,
			data:      `{"id":"direct-message","type":0,"content":"ready?","channel_id":"dm","author":{"id":"user"},"timestamp":"2025-08-14T18:09:37.947000+00:00","application_id":"application","attachments":[],"channel":{"id":"dm","type":1,"recipients":[{"id":"recipient"}]},"activity":{"type":1,"party_id":"party"},"application":{"id":"application","name":"Game","verify_key":"verify"}}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventMessage)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.Channel == nil || data.Channel.ID != "dm" || len(data.Channel.Recipients) != 1 {
					t.Fatalf("Channel = %#v", data.Channel)
				}
				if data.Application == nil || data.Application.VerifyKey != "verify" {
					t.Fatalf("Application = %#v", data.Application)
				}
				if data.Activity == nil || data.Activity.PartyID != "party" {
					t.Fatalf("Activity = %#v", data.Activity)
				}
			},
		},
		{
			name:      "game direct message update",
			eventType: ApplicationWebhookEventTypeGameDirectMessageUpdate,
			data:      `{"id":"direct-message","content":"almost ready?","channel_id":"dm","author":{"id":"user"},"recipient_id":"recipient"}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventMessage)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.ID != "direct-message" || data.RecipientID != "recipient" {
					t.Fatalf("updated direct message = %#v", data)
				}
			},
		},
		{
			name:      "game direct message delete",
			eventType: ApplicationWebhookEventTypeGameDirectMessageDelete,
			data:      `{"id":"direct-message","type":0,"content":"deleted","channel_id":"dm","author":{"id":"user"},"timestamp":"2025-08-20T17:01:44.725000+00:00","flags":0,"attachments":[],"components":[]}`,
			check: func(t *testing.T, event *ApplicationWebhookEventBody) {
				data, ok := event.Struct.(*ApplicationWebhookEventMessage)
				if !ok {
					t.Fatalf("Struct = %T", event.Struct)
				}
				if data.ID != "direct-message" || data.Content != "deleted" || data.Components == nil {
					t.Fatalf("deleted direct message = %#v", data.Message)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := applicationWebhookEventJSON(tt.eventType, tt.data)
			var event ApplicationWebhookEvent
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if event.Version != 1 || event.ApplicationID != "application" || event.Type != ApplicationWebhookTypeEvent {
				t.Fatalf("envelope = %#v", event)
			}
			if event.Event == nil || event.Event.Type != tt.eventType {
				t.Fatalf("Event = %#v", event.Event)
			}
			if event.Event.Timestamp != "2025-08-20T17:01:50.099204" {
				t.Fatalf("Timestamp = %q", event.Event.Timestamp)
			}
			if tt.data != "" && string(event.Event.RawData) != tt.data {
				t.Fatalf("RawData = %s, want %s", event.Event.RawData, tt.data)
			}
			tt.check(t, event.Event)
		})
	}
}

func TestApplicationWebhookEventNullableAndUnknownData(t *testing.T) {
	t.Run("ping has no event", func(t *testing.T) {
		var event ApplicationWebhookEvent
		if err := json.Unmarshal([]byte(`{"version":1,"application_id":"application","type":0}`), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.Type != ApplicationWebhookTypePing || event.Event != nil {
			t.Fatalf("event = %#v", event)
		}
	})

	t.Run("null event", func(t *testing.T) {
		var event ApplicationWebhookEvent
		if err := json.Unmarshal([]byte(`{"version":1,"application_id":"application","type":0,"event":null}`), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.Event != nil {
			t.Fatalf("Event = %#v, want nil", event.Event)
		}
	})

	t.Run("known event without data", func(t *testing.T) {
		var event ApplicationWebhookEvent
		if err := json.Unmarshal([]byte(applicationWebhookEventJSON(ApplicationWebhookEventTypeApplicationAuthorized, "")), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.Event.Struct != nil || event.Event.RawData != nil {
			t.Fatalf("data = %T/%s, want nil", event.Event.Struct, event.Event.RawData)
		}
	})

	t.Run("known event with null data", func(t *testing.T) {
		var event ApplicationWebhookEvent
		if err := json.Unmarshal([]byte(applicationWebhookEventJSON(ApplicationWebhookEventTypeApplicationAuthorized, "null")), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.Event.Struct != nil || string(event.Event.RawData) != "null" {
			t.Fatalf("data = %T/%s, want nil/null", event.Event.Struct, event.Event.RawData)
		}
	})

	t.Run("nullable authorization fields", func(t *testing.T) {
		var event ApplicationWebhookEvent
		payload := applicationWebhookEventJSON(ApplicationWebhookEventTypeApplicationAuthorized, `{"integration_type":null,"user":{"id":"user"},"scopes":[],"guild":null}`)
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		data := event.Event.Struct.(*ApplicationWebhookEventApplicationAuthorizedData)
		if data.IntegrationType != nil || data.Guild != nil {
			t.Fatalf("nullable fields = %v/%#v", data.IntegrationType, data.Guild)
		}
	})

	t.Run("unknown event preserves raw data", func(t *testing.T) {
		const raw = `{"future":true}`
		var event ApplicationWebhookEvent
		payload := applicationWebhookEventJSON(ApplicationWebhookEventType("FUTURE_EVENT"), raw)
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.Event.Struct != nil || string(event.Event.RawData) != raw {
			t.Fatalf("data = %T/%s", event.Event.Struct, event.Event.RawData)
		}

		encoded, err := json.Marshal(&event)
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		var roundTrip ApplicationWebhookEvent
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("round-trip json.Unmarshal returned error: %v", err)
		}
		if roundTrip.Event.Type != "FUTURE_EVENT" || string(roundTrip.Event.RawData) != raw {
			t.Fatalf("round-trip event = %#v", roundTrip.Event)
		}
	})

	t.Run("quest data remains raw", func(t *testing.T) {
		const raw = `{"future_quest_field":"value"}`
		var event ApplicationWebhookEvent
		payload := applicationWebhookEventJSON(ApplicationWebhookEventTypeQuestUserEnrollment, raw)
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.Event.Struct != nil || string(event.Event.RawData) != raw {
			t.Fatalf("data = %T/%s", event.Event.Struct, event.Event.RawData)
		}
	})

	t.Run("invalid known data", func(t *testing.T) {
		var event ApplicationWebhookEvent
		payload := applicationWebhookEventJSON(ApplicationWebhookEventTypeEntitlementCreate, `"invalid"`)
		if err := json.Unmarshal([]byte(payload), &event); err == nil {
			t.Fatal("json.Unmarshal returned nil error")
		}
	})
}

func applicationWebhookEventJSON(eventType ApplicationWebhookEventType, data string) string {
	dataField := ""
	if data != "" {
		dataField = `,"data":` + data
	}
	return fmt.Sprintf(`{"version":1,"application_id":"application","type":1,"event":{"type":%q,"timestamp":"2025-08-20T17:01:50.099204"%s}}`, eventType, dataField)
}
