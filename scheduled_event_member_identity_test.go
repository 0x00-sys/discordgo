package discordgo

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestScheduledEventUserMembersPreserveIdentity(t *testing.T) {
	for _, exception := range []bool{false, true} {
		for _, embeddedUser := range []bool{false, true} {
			t.Run(fmt.Sprintf("exception_%t/embedded_user_%t", exception, embeddedUser), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				userField := ""
				if embeddedUser {
					userField = `"user":{"id":"200","username":"embedded","discriminator":"0"},`
				}
				body := fmt.Sprintf(`[{"guild_scheduled_event_id":"300","user":{"id":"200","username":"subscriber","discriminator":"0"},"member":{%s"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}]`, userField)
				endpoint := EndpointGuildScheduledEventUsers("100", "300")
				if exception {
					endpoint = EndpointGuildScheduledEventExceptionUsers("100", "300", "400")
				}
				session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodGet || req.URL.String() != endpoint+"?with_member=true" {
						t.Errorf("request = %s %s, want subscriber GET with members", req.Method, req.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})
				fetch := func() ([]*GuildScheduledEventUser, error) {
					if exception {
						return session.GuildScheduledEventExceptionUsers("100", "300", "400", 0, true, "", "")
					}
					return session.GuildScheduledEventUsers("100", "300", 0, true, "", "")
				}
				users, err := fetch()
				if err != nil || len(users) != 1 || users[0] == nil || users[0].Member == nil {
					t.Fatalf("subscriber response = %#v, %v", users, err)
				}
				member := users[0].Member
				if member.GuildID != "100" {
					t.Errorf("member guild = %q, want 100", member.GuildID)
				}
				if !embeddedUser && member.User != users[0].User {
					t.Error("member did not link the subscriber user")
				} else if embeddedUser && (member.User == nil || member.User.Username != "embedded") {
					t.Error("member's existing user was replaced")
				}
				if got := member.Mention(); got != "<@!200>" {
					t.Errorf("Mention = %q, want <@!200>", got)
				}
				if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
					t.Errorf("AvatarURL = %q, want %q", got, want)
				}
				if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
					t.Errorf("BannerURL = %q, want %q", got, want)
				}
				if err := session.State.MemberAdd(member); err != nil {
					t.Errorf("caching subscriber member failed: %v", err)
				} else if cached, err := session.State.Member("100", "200"); err != nil || cached.User.ID != "200" {
					t.Errorf("member not retrievable from its guild: %v", err)
				}

				// Membership is optional even when requested; do not invent it.
				body = `[{"guild_scheduled_event_id":"300","user":{"id":"200"}}]`
				if users, err = fetch(); err != nil || len(users) != 1 || users[0].Member != nil {
					t.Fatalf("omitted member response = %#v, %v", users, err)
				}
				// Preserve previously accepted null responses and entries safely.
				body = `null`
				if users, err = fetch(); err != nil || users != nil {
					t.Fatalf("null response = %#v, %v", users, err)
				}
				body = `[null]`
				if users, err = fetch(); err != nil || len(users) != 1 || users[0] != nil {
					t.Fatalf("null entry response = %#v, %v", users, err)
				}
			})
		}
	}
}
