package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGuildHomeSettings(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/guilds/guild/new-member-welcome"
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Test"); got != "guild-home-settings" {
			t.Fatalf("X-Test = %q, want guild-home-settings", got)
		}

		body := `{"guild_id":"guild","enabled":true,"welcome_message":{"author_ids":["author"],"message":"Welcome"},"new_member_actions":[{"channel_id":"rules","action_type":0,"title":"Read the rules","description":"Start here","emoji":{"id":null,"name":"👋","animated":false},"icon":"book"},{"channel_id":"chat","action_type":1,"title":"Say hello","description":"Meet everyone"}],"resource_channels":[{"channel_id":"faq","title":"FAQ","description":"Common questions","emoji":{"id":"emoji","name":null,"animated":true},"icon":"help"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	settings, err := session.GuildHomeSettings("guild", WithHeader("X-Test", "guild-home-settings"))
	if err != nil {
		t.Fatalf("GuildHomeSettings returned error: %v", err)
	}
	if settings == nil || settings.GuildID != "guild" || !settings.Enabled {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.WelcomeMessage == nil || settings.WelcomeMessage.Message != "Welcome" || len(settings.WelcomeMessage.AuthorIDs) != 1 || settings.WelcomeMessage.AuthorIDs[0] != "author" {
		t.Fatalf("welcome message = %#v", settings.WelcomeMessage)
	}
	if len(settings.NewMemberActions) != 2 || settings.NewMemberActions[0].ActionType != GuildHomeNewMemberActionTypeView || settings.NewMemberActions[1].ActionType != GuildHomeNewMemberActionTypeTalk {
		t.Fatalf("new member actions = %#v", settings.NewMemberActions)
	}
	if settings.NewMemberActions[0].Emoji == nil || settings.NewMemberActions[0].Emoji.Name != "👋" || settings.NewMemberActions[1].Emoji != nil {
		t.Fatalf("new member action emojis = %#v", settings.NewMemberActions)
	}
	if len(settings.ResourceChannels) != 1 || settings.ResourceChannels[0].ChannelID != "faq" || settings.ResourceChannels[0].Emoji == nil || settings.ResourceChannels[0].Emoji.ID != "emoji" || !settings.ResourceChannels[0].Emoji.Animated {
		t.Fatalf("resource channels = %#v", settings.ResourceChannels)
	}
}

func TestGuildHomeSettingsResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantNil    bool
		wantJSON   bool
		wantREST   bool
	}{
		{name: "no content", statusCode: http.StatusNoContent, wantNil: true},
		{name: "malformed JSON", statusCode: http.StatusOK, body: `{`, wantNil: true, wantJSON: true},
		{name: "null JSON", statusCode: http.StatusOK, body: `null`, wantNil: true, wantJSON: true},
		{name: "REST error", statusCode: http.StatusForbidden, body: `{"code":50001,"message":"Missing Access"}`, wantNil: true, wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.statusCode,
					Status:     http.StatusText(tt.statusCode),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			settings, err := session.GuildHomeSettings("guild")
			if tt.wantJSON && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.statusCode {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.statusCode)
				}
			}
			if !tt.wantJSON && !tt.wantREST && err != nil {
				t.Fatalf("GuildHomeSettings returned error: %v", err)
			}
			if tt.wantNil && settings != nil {
				t.Fatalf("settings = %#v, want nil", settings)
			}
		})
	}
}
