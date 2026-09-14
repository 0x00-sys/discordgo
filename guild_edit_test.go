package discordgo

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// https://docs.discord.com/developers/resources/guild#modify-guild
func TestGuildEditExplicitResets(t *testing.T) {
	verification := VerificationLevelNone
	tests := []struct {
		name   string
		params GuildParams
		want   string
	}{
		{"omitted", GuildParams{}, `{}`},
		{"zero", GuildParams{DefaultMessageNotificationsSet: true, ExplicitContentFilterSet: true, SystemChannelFlagsSet: true, VerificationLevel: &verification}, `{"default_message_notifications":0,"explicit_content_filter":0,"system_channel_flags":0,"verification_level":0}`},
		{"null", GuildParams{AfkChannelIDNull: true, IconNull: true, SplashNull: true, DiscoverySplashNull: true, BannerNull: true, SystemChannelIDNull: true, RulesChannelIDNull: true, PublicUpdatesChannelIDNull: true, DescriptionNull: true}, `{"afk_channel_id":null,"icon":null,"splash":null,"discovery_splash":null,"banner":null,"system_channel_id":null,"rules_channel_id":null,"public_updates_channel_id":null,"description":null}`},
		{"legacy values", GuildParams{Name: "guild", DefaultMessageNotifications: 1, ExplicitContentFilter: 2, SystemChannelFlags: 1, AfkChannelID: "channel", Icon: "image", Splash: "splash", DiscoverySplash: "discovery", Banner: "banner", SystemChannelID: "system", RulesChannelID: "rules", PublicUpdatesChannelID: "updates", Description: "description", AfkTimeout: 300}, `{"name":"guild","default_message_notifications":1,"explicit_content_filter":2,"system_channel_flags":1,"afk_channel_id":"channel","icon":"image","splash":"splash","discovery_splash":"discovery","banner":"banner","system_channel_id":"system","rules_channel_id":"rules","public_updates_channel_id":"updates","description":"description","afk_timeout":300}`},
		{"null precedence", GuildParams{AfkChannelID: "channel", AfkChannelIDNull: true, Icon: "image", IconNull: true}, `{"afk_channel_id":null,"icon":null}`},
		{"set nonzero", GuildParams{DefaultMessageNotifications: 1, DefaultMessageNotificationsSet: true, ExplicitContentFilter: 2, ExplicitContentFilterSet: true, SystemChannelFlags: 1, SystemChannelFlagsSet: true}, `{"default_message_notifications":1,"explicit_content_filter":2,"system_channel_flags":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if r.Method != http.MethodPatch || r.URL.Path != "/api/v"+APIVersion+"/guilds/guild" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				var got, want map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("body = %#v, want %s", got, tt.want)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"guild","name":"updated"}`)), Request: r}, nil
			})
			guild, err := session.GuildEdit("guild", &tt.params)
			if err != nil {
				t.Fatal(err)
			}
			if !called || guild == nil || guild.Name != "updated" {
				t.Fatalf("GuildEdit did not complete: %#v", guild)
			}
		})
	}
}

func TestGuildEditNilParams(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("nil guild params must not send a request")
		return nil, nil
	})
	guild, err := session.GuildEdit("guild", nil)
	if err == nil || guild != nil {
		t.Fatalf("GuildEdit(nil) = %#v, %v; want nil and error", guild, err)
	}
}
