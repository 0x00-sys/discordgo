package discordgo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInteractionRespondPreservesExplicitNullAllowedMentions(t *testing.T) {
	tests := []struct {
		name         string
		responseType InteractionResponseType
		mentions     *MessageAllowedMentions
		set          bool
		want         string
	}{
		{"update_null", InteractionResponseUpdateMessage, nil, true, `null`},
		{"update_omitted", InteractionResponseUpdateMessage, nil, false, `{"parse":["everyone"],"replied_user":false}`},
		{"update_policy", InteractionResponseUpdateMessage, &MessageAllowedMentions{Parse: []AllowedMentionType{}}, true, `{"parse":[],"replied_user":false}`},
		{"create_ignores_update_flag", InteractionResponseChannelMessageWithSource, nil, true, `{"parse":["everyone"],"replied_user":false}`},
	}
	for _, tt := range tests {
		for _, withResponse := range []bool{false, true} {
			for _, withFile := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/response=%t/file=%t", tt.name, withResponse, withFile), func(t *testing.T) {
					s, err := New("")
					if err != nil {
						t.Fatal(err)
					}
					s.AllowedMentions = &MessageAllowedMentions{Parse: []AllowedMentionType{AllowedMentionTypeEveryone}}
					response := &InteractionResponse{
						Type: tt.responseType,
						Data: &InteractionResponseData{Content: "updated", AllowedMentions: tt.mentions, AllowedMentionsSet: tt.set},
					}
					if withFile {
						response.Data.Files = []*File{{Name: "note.txt", Reader: strings.NewReader("attachment")}}
					}
					var payload []byte
					requests := 0
					s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						requests++
						if withFile {
							if err := r.ParseMultipartForm(1 << 20); err != nil {
								return nil, err
							}
							defer func() {
								if err := r.MultipartForm.RemoveAll(); err != nil {
									t.Error(err)
								}
							}()
							payload = []byte(r.FormValue("payload_json"))
						} else {
							var err error
							payload, err = io.ReadAll(r.Body)
							if err != nil {
								return nil, err
							}
						}
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(`{"interaction":{"id":"interaction","type":3}}`)),
						}, nil
					})
					interaction := &Interaction{ID: "interaction", Token: "token"}
					if withResponse {
						_, err = s.InteractionRespondWithResponse(interaction, response)
					} else {
						err = s.InteractionRespond(interaction, response)
					}
					if err != nil {
						t.Fatal(err)
					}
					if requests != 1 {
						t.Fatalf("requests = %d, want 1", requests)
					}
					var got struct {
						Type InteractionResponseType `json:"type"`
						Data struct {
							Content         string          `json:"content"`
							AllowedMentions json.RawMessage `json:"allowed_mentions"`
						} `json:"data"`
					}
					if err := json.Unmarshal(payload, &got); err != nil {
						t.Fatal(err)
					}
					if got.Type != tt.responseType || got.Data.Content != "updated" {
						t.Fatalf("unexpected response payload: %s", payload)
					}
					assertJSONEqual(t, got.Data.AllowedMentions, []byte(tt.want))
					if response.Data.AllowedMentions != tt.mentions || response.Data.AllowedMentionsSet != tt.set {
						t.Fatal("request changed caller mention settings")
					}
				})
			}
		}
	}
}
