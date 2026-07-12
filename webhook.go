package discordgo

import (
	"bytes"
	"encoding/json"
)

// ApplicationWebhookType is the type of an application webhook event payload.
type ApplicationWebhookType int

// Valid ApplicationWebhookType values.
const (
	ApplicationWebhookTypePing  ApplicationWebhookType = 0
	ApplicationWebhookTypeEvent ApplicationWebhookType = 1
)

// ApplicationWebhookEventType is the type of event in an application webhook payload.
type ApplicationWebhookEventType string

// Valid ApplicationWebhookEventType values.
const (
	ApplicationWebhookEventTypeApplicationAuthorized   ApplicationWebhookEventType = "APPLICATION_AUTHORIZED"
	ApplicationWebhookEventTypeApplicationDeauthorized ApplicationWebhookEventType = "APPLICATION_DEAUTHORIZED"
	ApplicationWebhookEventTypeEntitlementCreate       ApplicationWebhookEventType = "ENTITLEMENT_CREATE"
	ApplicationWebhookEventTypeEntitlementUpdate       ApplicationWebhookEventType = "ENTITLEMENT_UPDATE"
	ApplicationWebhookEventTypeEntitlementDelete       ApplicationWebhookEventType = "ENTITLEMENT_DELETE"
	ApplicationWebhookEventTypeQuestUserEnrollment     ApplicationWebhookEventType = "QUEST_USER_ENROLLMENT"
	ApplicationWebhookEventTypeLobbyMessageCreate      ApplicationWebhookEventType = "LOBBY_MESSAGE_CREATE"
	ApplicationWebhookEventTypeLobbyMessageUpdate      ApplicationWebhookEventType = "LOBBY_MESSAGE_UPDATE"
	ApplicationWebhookEventTypeLobbyMessageDelete      ApplicationWebhookEventType = "LOBBY_MESSAGE_DELETE"
	ApplicationWebhookEventTypeGameDirectMessageCreate ApplicationWebhookEventType = "GAME_DIRECT_MESSAGE_CREATE"
	ApplicationWebhookEventTypeGameDirectMessageUpdate ApplicationWebhookEventType = "GAME_DIRECT_MESSAGE_UPDATE"
	ApplicationWebhookEventTypeGameDirectMessageDelete ApplicationWebhookEventType = "GAME_DIRECT_MESSAGE_DELETE"
)

// ApplicationWebhookEvent is an outgoing webhook event sent by Discord to an application.
type ApplicationWebhookEvent struct {
	Version       int                          `json:"version"`
	ApplicationID string                       `json:"application_id"`
	Type          ApplicationWebhookType       `json:"type"`
	Event         *ApplicationWebhookEventBody `json:"event,omitempty"`
}

// ApplicationWebhookEventBody contains the type, timestamp, and data for an application webhook event.
type ApplicationWebhookEventBody struct {
	Type      ApplicationWebhookEventType `json:"type"`
	Timestamp string                      `json:"timestamp"`
	RawData   json.RawMessage             `json:"data,omitempty"`
	// Struct contains the typed event data for known event types.
	Struct interface{} `json:"-"`
}

// UnmarshalJSON unmarshals known application webhook event data into Struct while preserving RawData.
func (e *ApplicationWebhookEventBody) UnmarshalJSON(data []byte) error {
	var v struct {
		Type      ApplicationWebhookEventType `json:"type"`
		Timestamp string                      `json:"timestamp"`
		RawData   json.RawMessage             `json:"data"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	e.Type = v.Type
	e.Timestamp = v.Timestamp
	e.RawData = v.RawData
	e.Struct = nil

	if len(v.RawData) == 0 || bytes.Equal(bytes.TrimSpace(v.RawData), []byte("null")) {
		return nil
	}

	switch e.Type {
	case ApplicationWebhookEventTypeApplicationAuthorized:
		e.Struct = &ApplicationWebhookEventApplicationAuthorizedData{}
	case ApplicationWebhookEventTypeApplicationDeauthorized:
		e.Struct = &ApplicationWebhookEventApplicationDeauthorizedData{}
	case ApplicationWebhookEventTypeEntitlementCreate,
		ApplicationWebhookEventTypeEntitlementUpdate,
		ApplicationWebhookEventTypeEntitlementDelete:
		e.Struct = &Entitlement{}
	case ApplicationWebhookEventTypeLobbyMessageCreate,
		ApplicationWebhookEventTypeLobbyMessageUpdate,
		ApplicationWebhookEventTypeGameDirectMessageCreate,
		ApplicationWebhookEventTypeGameDirectMessageUpdate,
		ApplicationWebhookEventTypeGameDirectMessageDelete:
		e.Struct = &ApplicationWebhookEventMessage{}
	case ApplicationWebhookEventTypeLobbyMessageDelete:
		e.Struct = &ApplicationWebhookEventLobbyMessageDeleteData{}
	default:
		return nil
	}

	return json.Unmarshal(v.RawData, e.Struct)
}

// ApplicationWebhookEventApplicationAuthorizedData is sent when a user authorizes an application.
type ApplicationWebhookEventApplicationAuthorizedData struct {
	IntegrationType *ApplicationIntegrationType `json:"integration_type,omitempty"`
	User            *User                       `json:"user"`
	Scopes          []string                    `json:"scopes"`
	Guild           *Guild                      `json:"guild,omitempty"`
}

// ApplicationWebhookEventApplicationDeauthorizedData is sent when a user deauthorizes an application.
type ApplicationWebhookEventApplicationDeauthorizedData struct {
	User *User `json:"user"`
}

// ApplicationWebhookEventMessage contains the standard and Social SDK fields used by message webhook events.
type ApplicationWebhookEventMessage struct {
	*Message
	LobbyID            string            `json:"lobby_id,omitempty"`
	Channel            *Channel          `json:"channel,omitempty"`
	RecipientID        string            `json:"recipient_id,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	ModerationMetadata map[string]string `json:"moderation_metadata,omitempty"`
	Application        *Application      `json:"application,omitempty"`
}

// UnmarshalJSON is a helper function to unmarshal an ApplicationWebhookEventMessage.
func (m *ApplicationWebhookEventMessage) UnmarshalJSON(data []byte) error {
	var message *Message
	if err := json.Unmarshal(data, &message); err != nil {
		return err
	}

	var v struct {
		LobbyID            string            `json:"lobby_id"`
		Channel            *Channel          `json:"channel"`
		RecipientID        string            `json:"recipient_id"`
		Metadata           map[string]string `json:"metadata"`
		ModerationMetadata map[string]string `json:"moderation_metadata"`
		Application        *Application      `json:"application"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	m.Message = message
	m.LobbyID = v.LobbyID
	m.Channel = v.Channel
	m.RecipientID = v.RecipientID
	m.Metadata = v.Metadata
	m.ModerationMetadata = v.ModerationMetadata
	m.Application = v.Application
	return nil
}

// ApplicationWebhookEventLobbyMessageDeleteData identifies a deleted lobby message.
type ApplicationWebhookEventLobbyMessageDeleteData struct {
	ID      string `json:"id"`
	LobbyID string `json:"lobby_id"`
}

// Webhook stores the data for a webhook.
type Webhook struct {
	ID        string      `json:"id"`
	Type      WebhookType `json:"type"`
	GuildID   string      `json:"guild_id"`
	ChannelID string      `json:"channel_id"`
	User      *User       `json:"user"`
	Name      string      `json:"name"`
	Avatar    string      `json:"avatar"`
	Token     string      `json:"token"`

	// ApplicationID is the bot/OAuth2 application that created this webhook
	ApplicationID string `json:"application_id,omitempty"`
	// SourceGuild is the guild of the followed channel for channel follower webhooks.
	SourceGuild *Guild `json:"source_guild,omitempty"`
	// SourceChannel is the followed channel for channel follower webhooks.
	SourceChannel *Channel `json:"source_channel,omitempty"`
	// URL is the webhook execution URL returned by the webhooks OAuth2 flow.
	URL string `json:"url,omitempty"`
}

// WebhookType is the type of Webhook (see WebhookType* consts) in the Webhook struct
// https://discord.com/developers/docs/resources/webhook#webhook-object-webhook-types
type WebhookType int

// Valid WebhookType values
const (
	WebhookTypeIncoming        WebhookType = 1
	WebhookTypeChannelFollower WebhookType = 2
	WebhookTypeApplication     WebhookType = 3
)

// WebhookParams is a struct for webhook params, used in the WebhookExecute command.
type WebhookParams struct {
	Content         string                  `json:"content,omitempty"`
	Username        string                  `json:"username,omitempty"`
	AvatarURL       string                  `json:"avatar_url,omitempty"`
	TTS             bool                    `json:"tts,omitempty"`
	Files           []*File                 `json:"-"`
	Components      []MessageComponent      `json:"components"`
	Embeds          []*MessageEmbed         `json:"embeds,omitempty"`
	Attachments     []*MessageAttachment    `json:"attachments,omitempty"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
	// Only flags supported by webhook messages can be set.
	// MessageFlagsEphemeral can only be set when using Followup Message Create endpoint.
	// Use MessageFlagsIsComponentsV2 when sending components v2 messages.
	Flags MessageFlags `json:"flags,omitempty"`
	// Name of the thread to create.
	// NOTE: can only be set if the webhook channel is a forum or media channel.
	ThreadName string `json:"thread_name,omitempty"`
	// IDs of tags to apply to the created thread in a forum or media channel.
	AppliedTags []string `json:"applied_tags,omitempty"`
	Poll        *Poll    `json:"poll,omitempty"`
}

// WebhookExecuteOptions stores query parameters for executing a webhook.
type WebhookExecuteOptions struct {
	// Wait for server confirmation and return the created message.
	Wait bool
	// Send the message to this thread. The thread will automatically be unarchived.
	ThreadID string
	// Respect the components field. Non-application-owned webhooks may only send non-interactive components.
	WithComponents bool
}

// WebhookMessageOptions stores query parameters for getting or deleting a webhook message.
type WebhookMessageOptions struct {
	// ID of the thread containing the message.
	ThreadID string
}

// WebhookMessageEditOptions stores query parameters for editing a webhook message.
type WebhookMessageEditOptions struct {
	// ID of the thread containing the message.
	ThreadID string
	// Respect the components field. Non-application-owned webhooks may only send non-interactive components.
	WithComponents bool
}

// WebhookEdit stores data for editing of a webhook message.
type WebhookEdit struct {
	Content         *string                 `json:"content,omitempty"`
	Components      *[]MessageComponent     `json:"components,omitempty"`
	Embeds          *[]*MessageEmbed        `json:"embeds,omitempty"`
	Files           []*File                 `json:"-"`
	Attachments     *[]*MessageAttachment   `json:"attachments,omitempty"`
	AllowedMentions *MessageAllowedMentions `json:"allowed_mentions,omitempty"`
	Flags           MessageFlags            `json:"flags,omitempty"`
	// Poll can only be added when editing a deferred interaction response.
	Poll *Poll `json:"poll,omitempty"`
}
