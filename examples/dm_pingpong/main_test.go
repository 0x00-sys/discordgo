package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGuildPingSendsDM(t *testing.T) {
	s, err := newSession("test")
	if err != nil {
		t.Fatal(err)
	}
	// Guild MESSAGE_CREATE and its content must both be requested in Identify.
	required := discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent
	if s.Identify.Intents&required != required {
		t.Fatalf("intents = %d, missing guild messages or message content", s.Identify.Intents)
	}
	s.State.User = &discordgo.User{ID: "bot"}
	requests := 0
	s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		response := `{"id":"dm","type":1}`
		switch requests {
		case 1:
			if !strings.HasSuffix(r.URL.Path, "/users/@me/channels") || body["recipient_id"] != "user" {
				t.Fatalf("DM request: %s %#v", r.URL, body)
			}
		case 2:
			if !strings.HasSuffix(r.URL.Path, "/channels/dm/messages") || body["content"] != "Pong!" {
				t.Fatalf("message request: %s %#v", r.URL, body)
			}
			response = `{"id":"reply","channel_id":"dm","content":"Pong!"}`
		default:
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
	})
	messageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "channel", Content: "ping", Author: &discordgo.User{ID: "user"}}})
	if requests != 2 {
		t.Fatalf("requests = %d, want DM creation and reply", requests)
	}
}
