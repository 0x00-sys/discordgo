package discordgo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func guildWidgetSession(t *testing.T, handler http.HandlerFunc) *Session {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	oldEndpointGuilds := EndpointGuilds
	EndpointGuilds = server.URL + "/guilds/"
	t.Cleanup(func() {
		EndpointGuilds = oldEndpointGuilds
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()
	return session
}

func TestGuildWidget(t *testing.T) {
	session := guildWidgetSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/widget.json" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/widget.json")
		}
		if got := r.Header.Get("X-Test"); got != "guild-widget" {
			t.Fatalf("X-Test = %q, want guild-widget", got)
		}

		_, _ = w.Write([]byte(`{
			"id":"guild",
			"name":"Guild",
			"instant_invite":"https://discord.gg/widget",
			"channels":[{"id":"channel","name":"Voice","position":2}],
			"members":[{
				"id":"0",
				"username":"Member",
				"discriminator":"0000",
				"avatar":null,
				"status":"online",
				"avatar_url":"https://cdn.discordapp.com/widget-avatar.png",
				"activity":{"name":"Game"},
				"deaf":true,
				"mute":false,
				"self_deaf":false,
				"self_mute":true,
				"suppress":false,
				"channel_id":"channel"
			}],
			"presence_count":42
		}`))
	})

	widget, err := session.GuildWidget("guild", WithHeader("X-Test", "guild-widget"))
	if err != nil {
		t.Fatalf("GuildWidget returned error: %v", err)
	}
	if widget.ID != "guild" || widget.Name != "Guild" || widget.PresenceCount != 42 {
		t.Fatalf("widget = %#v", widget)
	}
	if widget.InstantInvite == nil || *widget.InstantInvite != "https://discord.gg/widget" {
		t.Fatalf("instant invite = %v", widget.InstantInvite)
	}
	if len(widget.Channels) != 1 || widget.Channels[0].ID != "channel" || widget.Channels[0].Name != "Voice" || widget.Channels[0].Position != 2 {
		t.Fatalf("channels = %#v", widget.Channels)
	}
	if len(widget.Members) != 1 {
		t.Fatalf("members = %#v", widget.Members)
	}
	member := widget.Members[0]
	if member.ID != "0" || member.Username != "Member" || member.Discriminator != "0000" || member.Avatar != "" {
		t.Fatalf("member user = %#v", member.User)
	}
	if member.Status != StatusOnline || member.AvatarURL != "https://cdn.discordapp.com/widget-avatar.png" || member.ChannelID != "channel" {
		t.Fatalf("member presence = %#v", member)
	}
	if member.Activity == nil || member.Activity.Name != "Game" {
		t.Fatalf("member activity = %#v", member.Activity)
	}
	if member.Deaf == nil || !*member.Deaf || member.Mute == nil || *member.Mute || member.SelfDeaf == nil || *member.SelfDeaf || member.SelfMute == nil || !*member.SelfMute || member.Suppress == nil || *member.Suppress {
		t.Fatalf("member voice state = %#v", member)
	}
}

func TestGuildWidgetFailures(t *testing.T) {
	session := guildWidgetSession(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/guilds/malformed/widget.json":
			_, _ = w.Write([]byte(`{`))
		case "/guilds/null/widget.json":
			_, _ = w.Write([]byte(`null`))
		case "/guilds/rest-error/widget.json":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":50004,"message":"Guild widget disabled"}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	})

	tests := []struct {
		name          string
		guildID       string
		wantREST      bool
		wantUnmarshal bool
	}{
		{name: "malformed response", guildID: "malformed", wantUnmarshal: true},
		{name: "null response", guildID: "null", wantUnmarshal: true},
		{name: "REST error", guildID: "rest-error", wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			widget, err := session.GuildWidget(tt.guildID)
			if err == nil {
				t.Fatal("GuildWidget returned nil error")
			}
			if widget != nil {
				t.Fatalf("widget = %#v, want nil", widget)
			}
			if tt.wantUnmarshal && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
			var restErr *RESTError
			if tt.wantREST && !errors.As(err, &restErr) {
				t.Fatalf("error = %v, want RESTError", err)
			}
		})
	}
}

func TestGuildWidgetImageURL(t *testing.T) {
	oldEndpointGuilds := EndpointGuilds
	EndpointGuilds = "https://example.com/api/v10/guilds/"
	t.Cleanup(func() {
		EndpointGuilds = oldEndpointGuilds
	})

	tests := []struct {
		name  string
		style GuildWidgetStyle
		want  string
	}{
		{name: "default", want: "https://example.com/api/v10/guilds/guild/widget.png"},
		{name: "shield", style: GuildWidgetStyleShield, want: "https://example.com/api/v10/guilds/guild/widget.png?style=shield"},
		{name: "banner 1", style: GuildWidgetStyleBanner1, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner1"},
		{name: "banner 2", style: GuildWidgetStyleBanner2, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner2"},
		{name: "banner 3", style: GuildWidgetStyleBanner3, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner3"},
		{name: "banner 4", style: GuildWidgetStyleBanner4, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner4"},
		{name: "query encoding", style: GuildWidgetStyle("banner&custom"), want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner%26custom"},
	}

	widget := &GuildWidget{ID: "guild"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := widget.ImageURL(tt.style); got != tt.want {
				t.Fatalf("ImageURL(%q) = %q, want %q", tt.style, got, tt.want)
			}
		})
	}
}
