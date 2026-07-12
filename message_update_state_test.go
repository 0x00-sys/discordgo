package discordgo

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

func newMessageUpdateState(t *testing.T, message *Message) *State {
	t.Helper()
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if message != nil {
		if err := state.MessageAdd(message); err != nil {
			t.Fatalf("MessageAdd returned error: %v", err)
		}
	}
	return state
}

func unmarshalMessageUpdate(t *testing.T, payload string) *MessageUpdate {
	t.Helper()
	var update MessageUpdate
	if err := json.Unmarshal([]byte(payload), &update); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return &update
}

func TestMessageUpdateMergePreservesOmittedFields(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	message := &Message{
		ID:              "message",
		ChannelID:       "channel",
		GuildID:         "guild",
		Content:         "before",
		Timestamp:       timestamp,
		Author:          &User{ID: "author"},
		Flags:           MessageFlagsSuppressEmbeds,
		Type:            MessageTypeReply,
		Pinned:          true,
		TTS:             true,
		MentionEveryone: true,
		MentionRoles:    []string{"role"},
		Reactions: []*MessageReactions{{
			Count: 1,
			Me:    true,
			Emoji: &Emoji{Name: "wave"},
		}},
	}
	state := newMessageUpdateState(t, message)
	update := unmarshalMessageUpdate(t, `{"id":"message","channel_id":"channel","content":"after"}`)

	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if got.Content != "after" || got.Author != message.Author || got.Timestamp != timestamp ||
		got.Flags != message.Flags || got.Type != message.Type || !got.Pinned || !got.TTS ||
		!got.MentionEveryone || !reflect.DeepEqual(got.MentionRoles, message.MentionRoles) ||
		!reflect.DeepEqual(got.Reactions, message.Reactions) {
		t.Fatalf("cached message lost omitted fields: %#v", got)
	}
	if update.Author != nil || update.Flags != 0 || update.Type != 0 || update.Pinned || update.Reactions != nil {
		t.Fatalf("event payload was replaced with merged state: %#v", update.Message)
	}
	if update.BeforeUpdate == nil || update.BeforeUpdate.Content != "before" || update.BeforeUpdate == got {
		t.Fatalf("BeforeUpdate = %#v, want isolated pre-update message", update.BeforeUpdate)
	}
}

func TestMessageUpdateMergeAppliesEveryExplicitField(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	one := 1
	message := &Message{
		ID: "message", ChannelID: "channel", ChannelType: ChannelTypeGuildText, GuildID: "guild",
		Content: "old", Timestamp: timestamp, EditedTimestamp: &timestamp, MentionRoles: []string{"role"},
		TTS: true, MentionEveryone: true, Author: &User{ID: "author"},
		Attachments: []*MessageAttachment{{ID: "attachment"}}, Components: []MessageComponent{&ActionsRow{}},
		Embeds: []*MessageEmbed{{Title: "embed"}}, Mentions: []*User{{ID: "mention"}},
		Reactions: []*MessageReactions{{Count: 1, Me: true, Emoji: &Emoji{Name: "wave"}}}, Nonce: "nonce",
		Pinned: true, Type: MessageTypeReply, WebhookID: "webhook", Member: &Member{GuildID: "guild", User: &User{ID: "member"}},
		MentionChannels: []*Channel{{ID: "mentioned-channel"}}, Activity: &MessageActivity{Type: MessageActivityTypeJoin, PartyID: "party"},
		Application: &MessageApplication{ID: "application"}, ApplicationID: "application",
		MessageReference: &MessageReference{MessageID: "reference"}, ReferencedMessage: &Message{ID: "reference"},
		MessageSnapshots: []MessageSnapshot{{Message: &Message{ID: "snapshot"}}}, Interaction: &MessageInteraction{ID: "interaction"},
		InteractionMetadata: &MessageInteractionMetadata{ID: "metadata"}, Flags: MessageFlagsSuppressEmbeds,
		Thread: &Channel{ID: "thread"}, StickerItems: []*StickerItem{{ID: "sticker"}}, Stickers: []*Sticker{{ID: "legacy-sticker"}},
		Position: &one, RoleSubscriptionData: &MessageRoleSubscriptionData{TierName: "tier"},
		Resolved: &ComponentInteractionDataResolved{Users: map[string]*User{"user": {ID: "user"}}},
		Poll:     &Poll{AllowMultiselect: true}, Call: &MessageCall{Participants: []string{"user"}},
		SharedClientTheme: &MessageSharedClientTheme{Colors: []string{"ffffff"}, GradientAngle: 1, BaseMix: 1},
	}
	state := newMessageUpdateState(t, message)
	update := unmarshalMessageUpdate(t, `{
		"id":"message","channel_id":"channel","channel_type":0,"guild_id":"guild",
		"content":"","timestamp":"0001-01-01T00:00:00Z","edited_timestamp":null,"mention_roles":[],
		"tts":false,"mention_everyone":false,"author":null,"attachments":[],"components":[],"embeds":[],
		"mentions":[],"reactions":[{"count":0,"count_details":{"burst":0,"normal":0},"me":false,"me_burst":false,"emoji":{"id":null,"name":""},"burst_colors":[]}],
		"nonce":"","pinned":false,"type":0,"webhook_id":"","member":null,"mention_channels":[],
		"activity":null,"application":null,"application_id":"","message_reference":null,"referenced_message":null,
		"message_snapshots":[],"interaction":null,"interaction_metadata":null,"flags":0,"thread":null,
		"sticker_items":[],"stickers":[],"position":null,"role_subscription_data":null,"resolved":null,
		"poll":null,"call":null,"shared_client_theme":null
	}`)

	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if !reflect.DeepEqual(got, update.Message) {
		t.Fatalf("cached message = %#v, want explicit update %#v", got, update.Message)
	}
	if got.Reactions[0].Count != 0 || got.Reactions[0].Me || got.Flags != 0 || got.Type != 0 || got.Pinned {
		t.Fatalf("explicit zero values were not applied: %#v", got)
	}
	if got.MentionRoles == nil || got.Attachments == nil || got.Components == nil || got.Embeds == nil || got.Mentions == nil {
		t.Fatalf("explicit empty arrays became nil: %#v", got)
	}
	if got.EditedTimestamp != nil || got.Poll != nil || got.ReferencedMessage != nil || got.Position != nil {
		t.Fatalf("explicit null values were not applied: %#v", got)
	}
	update.Reactions[0].Count = 99
	if got.Reactions[0].Count != 0 {
		t.Fatalf("cached update aliases event payload: %#v", got.Reactions)
	}
}

func TestMessageUpdateMergeCacheMiss(t *testing.T) {
	state := newMessageUpdateState(t, nil)
	update := unmarshalMessageUpdate(t, `{"id":"message","channel_id":"channel","content":"partial"}`)
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if got.Content != "partial" || got.Author != nil || update.BeforeUpdate != nil {
		t.Fatalf("cache miss message = %#v, BeforeUpdate = %#v", got, update.BeforeUpdate)
	}
	if got == update.Message {
		t.Fatal("cached message aliases event payload")
	}
	update.Content = "mutated"
	if got.Content != "partial" {
		t.Fatalf("cached content = %q after event mutation, want partial", got.Content)
	}
}

func TestMessageUpdateMergeProgrammaticCompatibility(t *testing.T) {
	state := newMessageUpdateState(t, &Message{
		ID:        "message",
		ChannelID: "channel",
		Content:   "before",
		Flags:     MessageFlagsSuppressEmbeds,
	})
	update := &MessageUpdate{Message: &Message{ID: "message", ChannelID: "channel", Content: "after"}}
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if got.Content != "after" || got.Flags != MessageFlagsSuppressEmbeds {
		t.Fatalf("programmatic update = %#v", got)
	}
}

func TestMessageUpdateMergeConcurrent(t *testing.T) {
	state := newMessageUpdateState(t, &Message{ID: "message", ChannelID: "channel", Content: "initial"})
	session := &Session{StateEnabled: true}
	var wg sync.WaitGroup
	updates := make([]*MessageUpdate, 25)
	for i := range updates {
		updates[i] = unmarshalMessageUpdate(t, `{"id":"message","channel_id":"channel","content":"updated"}`)
	}
	for _, update := range updates {
		wg.Add(2)
		go func(update *MessageUpdate) {
			defer wg.Done()
			if err := state.OnInterface(session, update); err != nil {
				t.Errorf("OnInterface returned error: %v", err)
			}
		}(update)
		go func() {
			defer wg.Done()
			if _, err := state.Message("channel", "message"); err != nil {
				t.Errorf("Message returned error: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestMessageUpdateUnmarshalMalformed(t *testing.T) {
	for _, payload := range []string{`{"id":`, `[]`, `{"id":false}`} {
		var update MessageUpdate
		if err := json.Unmarshal([]byte(payload), &update); err == nil {
			t.Fatalf("json.Unmarshal(%q) returned nil error", payload)
		}
		if update.Message != nil {
			t.Fatalf("json.Unmarshal(%q) left Message = %#v", payload, update.Message)
		}
	}
}
