package discordgo

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestMessageUpdatePreservesInteractionMemberSnapshot(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v"+APIVersion+"/channels/channel/messages/message" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		// guild_id is a gateway extra field, absent from this REST message.
		body := `{
			"id":"message","channel_id":"channel","author":{"id":"bot","username":"Bot","bot":true},
			"content":"before","timestamp":"2026-10-30T10:00:00Z","edited_timestamp":null,
			"tts":false,"mention_everyone":false,"mentions":[],"mention_roles":[],
			"attachments":[],"embeds":[],"pinned":false,"type":20,
			"interaction":{
				"id":"interaction","type":2,"name":"command","user":{"id":"user","username":"User"},
				"member":{"nick":"Nick","roles":[],"joined_at":"2025-01-01T00:00:00Z"}
			}
		}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	message, err := session.ChannelMessage("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	session.State = newMessageUpdateState(t, message)
	before, err := session.State.Message("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	if before.Interaction == nil || before.Interaction.Member == nil || before.Interaction.Member.GuildID != "" {
		t.Fatalf("unexpected initial interaction: %#v", before.Interaction)
	}
	var event *MessageUpdate
	session.AddHandler(func(_ *Session, update *MessageUpdate) { event = update })
	start := make(chan struct{})
	readDone := make(chan int, 1)
	go func() {
		<-start
		observed := 0
		for range 1000 {
			if before.Interaction.Member.GuildID != "" {
				observed++
			}
		}
		readDone <- observed
	}()
	close(start)
	_, updateErr := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"t":"MESSAGE_UPDATE","s":1,"d":{"id":"message","channel_id":"channel","guild_id":"guild","content":"after"}}`))
	observed := <-readDone
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	after, err := session.State.Message("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	if observed != 0 || before.Interaction.Member.GuildID != "" || message.Interaction.Member.GuildID != "" {
		t.Fatal("message update mutated the earlier interaction member snapshot")
	}
	if event == nil || event.BeforeUpdate == nil || event.BeforeUpdate.Interaction == nil || event.BeforeUpdate.Interaction.Member == nil || event.BeforeUpdate.Interaction.Member.GuildID != "" {
		t.Fatal("BeforeUpdate lost the original interaction member")
	}
	if event.Interaction != nil {
		t.Fatal("gateway event was populated with omitted interaction data")
	}
	if after.Content != "after" || after.GuildID != "guild" || after.Interaction == nil || after.Interaction.Member == nil {
		t.Fatalf("unexpected updated message: %#v", after)
	}
	if after.Interaction.Member.GuildID != "guild" || after.Interaction.Member.User != after.Interaction.User || after.Interaction.Member.Nick != "Nick" {
		t.Fatalf("updated interaction member was not preserved and linked: %#v", after.Interaction.Member)
	}
}
