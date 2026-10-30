package discordgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSessionAllowedMentionsPreservesSharedPayloads(t *testing.T) {
	content := "@everyone <@123> shared announcement"
	message := func(mentions *MessageAllowedMentions) any {
		return &MessageSend{Content: content, AllowedMentions: mentions}
	}
	messageEdit := func(mentions *MessageAllowedMentions) any {
		return &MessageEdit{Channel: "channel", ID: "message", Content: &content, AllowedMentions: mentions}
	}
	webhook := func(mentions *MessageAllowedMentions) any {
		return &WebhookParams{Content: content, AllowedMentions: mentions}
	}
	webhookEdit := func(mentions *MessageAllowedMentions) any {
		return &WebhookEdit{Content: &content, AllowedMentions: mentions}
	}
	interaction := func(mentions *MessageAllowedMentions) any {
		return &InteractionResponse{Type: InteractionResponseChannelMessageWithSource, Data: &InteractionResponseData{Content: content, AllowedMentions: mentions}}
	}
	tests := []struct {
		name       string
		newPayload func(*MessageAllowedMentions) any
		call       func(*Session, any, string) error
	}{
		{"send", message, func(s *Session, payload any, id string) error {
			_, err := s.ChannelMessageSendComplex(id, payload.(*MessageSend))
			return err
		}},
		{"edit", messageEdit, func(s *Session, payload any, _ string) error {
			_, err := s.ChannelMessageEditComplex(payload.(*MessageEdit))
			return err
		}},
		{"forum", message, func(s *Session, payload any, id string) error {
			_, err := s.ForumThreadStartComplex(id, &ThreadStart{Name: "thread", AutoArchiveDuration: 60}, payload.(*MessageSend))
			return err
		}},
		{"webhook", webhook, func(s *Session, payload any, id string) error {
			_, err := s.WebhookExecute(id, "token", true, payload.(*WebhookParams))
			return err
		}},
		{"webhook_edit", webhookEdit, func(s *Session, payload any, id string) error {
			_, err := s.WebhookMessageEdit(id, "token", "message", payload.(*WebhookEdit))
			return err
		}},
		{"followup", webhook, func(s *Session, payload any, id string) error {
			_, err := s.FollowupMessageCreate(&Interaction{AppID: "app", Token: id}, true, payload.(*WebhookParams))
			return err
		}},
		{"followup_edit", webhookEdit, func(s *Session, payload any, id string) error {
			_, err := s.FollowupMessageEdit(&Interaction{AppID: "app", Token: id}, "message", payload.(*WebhookEdit))
			return err
		}},
		{"interaction", interaction, func(s *Session, payload any, id string) error {
			return s.InteractionRespond(&Interaction{ID: id, Token: "token"}, payload.(*InteractionResponse))
		}},
	}
	for _, tt := range tests {
		for _, explicit := range []bool{false, true} {
			t.Run(tt.name+"/explicit="+strconv.FormatBool(explicit), func(t *testing.T) {
				var mentions *MessageAllowedMentions
				if explicit {
					mentions = &MessageAllowedMentions{Parse: []AllowedMentionType{AllowedMentionTypeUsers}}
				}
				payload := tt.newPayload(mentions)
				before, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var requests atomic.Int32
				var sessions [2]*Session
				for i, parse := range [][]AllowedMentionType{{AllowedMentionTypeEveryone}, {}} {
					s, err := New("")
					if err != nil {
						t.Fatal(err)
					}
					s.AllowedMentions = &MessageAllowedMentions{Parse: parse}
					wantParse := parse
					if explicit {
						wantParse = mentions.Parse
					}
					s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						requests.Add(1)
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							return nil, err
						}
						field := ""
						switch tt.name {
						case "forum":
							field = "message"
						case "interaction":
							field = "data"
						}
						if field != "" {
							var nested map[string]json.RawMessage
							if err := json.Unmarshal(body[field], &nested); err != nil {
								return nil, err
							}
							body = nested
						}
						var got MessageAllowedMentions
						if err := json.Unmarshal(body["allowed_mentions"], &got); err != nil {
							return nil, err
						}
						if !reflect.DeepEqual(got.Parse, wantParse) {
							return nil, fmt.Errorf("allowed_mentions.parse = %v, want %v", got.Parse, wantParse)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"result"}`))}, nil
					})
					sessions[i] = s
				}

				const workers = 16
				start := make(chan struct{})
				results := make(chan error, workers)
				for i := range workers {
					go func() {
						<-start
						results <- tt.call(sessions[i%2], payload, strconv.Itoa(i+1))
					}()
				}
				close(start)
				var firstErr error
				for range workers {
					if err := <-results; err != nil && firstErr == nil {
						firstErr = err
					}
				}
				if firstErr != nil {
					t.Error(firstErr)
				}
				// Both policies must also remain effective when the same payload is reused serially.
				for i, s := range sessions {
					if err := tt.call(s, payload, strconv.Itoa(workers+i+1)); err != nil {
						t.Error(err)
					}
				}
				if requests.Load() != workers+2 {
					t.Errorf("requests = %d, want %d", requests.Load(), workers+2)
				}
				after, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Errorf("caller payload changed: before %s, after %s", before, after)
				}
			})
		}
	}
}
