package discordgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRESTLegacyEmbedPreservesSharedPayload(t *testing.T) {
	tests := []struct {
		name string
		edit bool
		call func(*Session, any) error
	}{
		{"send", false, func(s *Session, payload any) error {
			_, err := s.ChannelMessageSendComplex("channel", payload.(*MessageSend))
			return err
		}},
		{"edit", true, func(s *Session, payload any) error {
			_, err := s.ChannelMessageEditComplex(payload.(*MessageEdit))
			return err
		}},
		{"forum", false, func(s *Session, payload any) error {
			_, err := s.ForumThreadStartComplex("channel", &ThreadStart{Name: "thread", AutoArchiveDuration: 60}, payload.(*MessageSend))
			return err
		}},
	}
	for _, tt := range tests {
		for _, conflict := range []bool{false, true} {
			t.Run(tt.name+"/conflict="+strconv.FormatBool(conflict), func(t *testing.T) {
				embed := &MessageEmbed{Title: "Reusable legacy embed"}
				var payload any
				if tt.edit {
					data := &MessageEdit{Channel: "channel", ID: "message", Embed: embed}
					if conflict {
						data.Embeds = &[]*MessageEmbed{embed}
					}
					payload = data
				} else {
					data := &MessageSend{Embed: embed}
					if conflict {
						data.Embeds = []*MessageEmbed{embed}
					}
					payload = data
				}
				before, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				s, err := New("")
				if err != nil {
					t.Fatal(err)
				}
				var requests atomic.Int32
				s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					requests.Add(1)
					var body struct {
						MessageSend
						Message *MessageSend `json:"message"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					embeds := body.Embeds
					if tt.name == "forum" && body.Message != nil {
						embeds = body.Message.Embeds
					}
					if len(embeds) != 1 || embeds[0].Title != "Reusable legacy embed" {
						return nil, fmt.Errorf("unexpected request embeds: %+v", embeds)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"result"}`))}, nil
				})

				const workers = 16
				start := make(chan struct{})
				results := make(chan error, workers)
				for range workers {
					go func() {
						<-start
						results <- tt.call(s, payload)
					}()
				}
				close(start)
				check := func(err error) {
					t.Helper()
					if conflict {
						if err == nil || err.Error() != "cannot specify both Embed and Embeds" {
							t.Errorf("error = %v, want conflicting embed fields", err)
						}
					} else if err != nil {
						t.Error(err)
					}
				}
				for range workers {
					check(<-results)
				}
				// The same legacy request must remain valid for subsequent calls too.
				check(tt.call(s, payload))
				check(tt.call(s, payload))
				wantRequests := int32(workers + 2)
				if conflict {
					wantRequests = 0
				}
				if requests.Load() != wantRequests {
					t.Errorf("requests = %d, want %d", requests.Load(), wantRequests)
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
