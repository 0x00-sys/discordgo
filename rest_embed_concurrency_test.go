package discordgo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRESTHelpersPreserveSharedEmbed(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session, *MessageEmbed) error
	}{
		{"send", func(s *Session, embed *MessageEmbed) error {
			_, err := s.ChannelMessageSendEmbed("channel", embed)
			return err
		}},
		{"edit", func(s *Session, embed *MessageEmbed) error {
			_, err := s.ChannelMessageEditEmbed("channel", "message", embed)
			return err
		}},
		{"forum", func(s *Session, embed *MessageEmbed) error {
			_, err := s.ForumThreadStartEmbed("channel", "thread", 60, embed)
			return err
		}},
	}
	for _, tt := range tests {
		for _, embedType := range []EmbedType{"", EmbedTypeRich} {
			t.Run(tt.name+"/type="+string(embedType), func(t *testing.T) {
				s, err := New("")
				if err != nil {
					t.Fatal(err)
				}
				embed := &MessageEmbed{Type: embedType, Title: "Shared announcement", Description: "Read-only content"}
				var requests atomic.Int32
				s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					var payload struct {
						Embeds  []MessageEmbed `json:"embeds"`
						Message *MessageSend   `json:"message"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						return nil, err
					}
					embeds := payload.Embeds
					if tt.name == "forum" && payload.Message != nil {
						for _, item := range payload.Message.Embeds {
							embeds = append(embeds, *item)
						}
					}
					if len(embeds) != 1 || embeds[0].Title != "Shared announcement" || embeds[0].Description != "Read-only content" {
						return nil, fmt.Errorf("unexpected request embeds: %+v", embeds)
					}
					// Discord defaults an omitted type to rich. Explicit types must survive encoding.
					if embeds[0].Type != EmbedTypeRich && (embedType != "" || embeds[0].Type != "") {
						return nil, fmt.Errorf("unexpected request embed type: %q", embeds[0].Type)
					}
					requests.Add(1)
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"result"}`))}, nil
				})

				const workers = 32
				start := make(chan struct{})
				results := make(chan error, workers)
				for range workers {
					go func() {
						<-start
						results <- tt.call(s, embed)
					}()
				}
				close(start)
				for range workers {
					if err := <-results; err != nil {
						t.Error(err)
					}
				}
				if requests.Load() != workers {
					t.Errorf("requests = %d, want %d", requests.Load(), workers)
				}
				if embed.Type != embedType {
					t.Errorf("shared embed type changed to %q, want %q", embed.Type, embedType)
				}
			})
		}
	}
}
