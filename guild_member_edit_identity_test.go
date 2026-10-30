package discordgo

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGuildMemberEditPreservesGuildIdentity(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "GuildMemberEdit"
		if legacy {
			name = "GuildMemberEditComplex"
		}
		t.Run(name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			body := `{"user":{"id":"200","username":"member","discriminator":"0"},"nick":"renamed","avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}`
			session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPatch || req.URL.String() != EndpointGuildMember("100", "200") {
					t.Errorf("request = %s %s, want member PATCH", req.Method, req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			nick := "renamed"
			edit := func() (*Member, error) {
				params := &GuildMemberParams{Nick: nick}
				if legacy {
					return session.GuildMemberEditComplex("100", "200", params)
				}
				return session.GuildMemberEdit("100", "200", params)
			}
			member, err := edit()
			if err != nil || member == nil {
				t.Fatalf("edit returned %v, %v", member, err)
			}
			if member.GuildID != "100" || member.Nick != nick {
				t.Errorf("edited member identity = guild %q, nick %q", member.GuildID, member.Nick)
			}
			if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
				t.Errorf("AvatarURL = %q, want %q", got, want)
			}
			if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
				t.Errorf("BannerURL = %q, want %q", got, want)
			}
			if err := session.State.MemberAdd(member); err != nil {
				t.Errorf("caching edited member failed: %v", err)
			} else if cached, err := session.State.Member("100", "200"); err != nil || cached.Nick != nick {
				t.Errorf("edited member was not retrievable from its guild: %v", err)
			}

			// Preserve the existing nil result for a null response without a panic.
			body = `null`
			if member, err := edit(); err != nil || member != nil {
				t.Fatalf("null response returned %v, %v", member, err)
			}
		})
	}
}
