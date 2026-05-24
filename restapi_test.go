package discordgo

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

//////////////////////////////////////////////////////////////////////////////
/////////////////////////////////////////////////////////////// START OF TESTS

// TestChannelMessageSend tests the ChannelMessageSend() function. This should not return an error.
func TestChannelMessageSend(t *testing.T) {

	if envChannel == "" {
		t.Skip("Skipping, DG_CHANNEL not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.ChannelMessageSend(envChannel, "Running REST API Tests!")
	if err != nil {
		t.Errorf("ChannelMessageSend returned error: %+v", err)
	}
}

/*
// removed for now, only works on BOT accounts now
func TestUserAvatar(t *testing.T) {

	if dg == nil {
		t.Skip("Cannot TestUserAvatar, dg not set.")
	}

	u, err := dg.User("@me")
	if err != nil {
		t.Error("error fetching @me user,", err)
	}

	a, err := dg.UserAvatar(u.ID)
	if err != nil {
		if err.Error() == `HTTP 404 NOT FOUND, {"code": 0, "message": "404: Not Found"}` {
			t.Skip("Skipped, @me doesn't have an Avatar")
		}
		t.Errorf(err.Error())
	}

	if a == nil {
		t.Errorf("a == nil, should be image.Image")
	}
}
*/

/* Running this causes an error due to 2/hour rate limit on username changes
func TestUserUpdate(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot test logout, dg not set.")
	}

	u, err := dg.User("@me")
	if err != nil {
		t.Errorf(err.Error())
	}

	s, err := dg.UserUpdate(envEmail, envPassword, "testname", u.Avatar, "")
	if err != nil {
		t.Error(err.Error())
	}
	if s.Username != "testname" {
		t.Error("Username != testname")
	}
	s, err = dg.UserUpdate(envEmail, envPassword, u.Username, u.Avatar, "")
	if err != nil {
		t.Error(err.Error())
	}
	if s.Username != u.Username {
		t.Error("Username != " + u.Username)
	}
}
*/

//func (s *Session) UserChannelCreate(recipientID string) (st *Channel, err error) {

func TestUserChannelCreate(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot TestUserChannelCreate, dg not set.")
	}

	if envAdmin == "" {
		t.Skip("Skipped, DG_ADMIN not set.")
	}

	_, err := dg.UserChannelCreate(envAdmin)
	if err != nil {
		t.Errorf(err.Error())
	}

	// TODO make sure the channel was added
}

func TestUserGuilds(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot TestUserGuilds, dg not set.")
	}

	_, err := dg.UserGuilds(10, "", "", false)
	if err != nil {
		t.Errorf(err.Error())
	}
}

func TestGuildMembersSearchSetsGuildID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/guild/members/search" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/members/search")
		}
		if query := r.URL.Query().Get("query"); query != "user" {
			t.Fatalf("query = %q, want %q", query, "user")
		}
		if limit := r.URL.Query().Get("limit"); limit != "2" {
			t.Fatalf("limit = %q, want %q", limit, "2")
		}
		_, _ = w.Write([]byte(`[{"user":{"id":"user","username":"user","discriminator":"0"},"avatar":"guild-avatar"}]`))
	}))
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

	members, err := session.GuildMembersSearch("guild", "user", 2)
	if err != nil {
		t.Fatalf("GuildMembersSearch returned error: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("len(members) = %d, want 1", len(members))
	}
	if members[0].GuildID != "guild" {
		t.Fatalf("GuildID = %q, want %q", members[0].GuildID, "guild")
	}
	if got, want := members[0].AvatarURL(""), EndpointGuildMemberAvatar("guild", "user", "guild-avatar"); got != want {
		t.Fatalf("AvatarURL = %q, want %q", got, want)
	}
}

func TestWebhookDeleteWithTokenAllowsNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodDelete)
		}
		if r.URL.Path != "/webhooks/webhook/token" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/webhooks/webhook/token")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	oldEndpointWebhooks := EndpointWebhooks
	EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		EndpointWebhooks = oldEndpointWebhooks
	})

	session, err := New("")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	webhook, err := session.WebhookDeleteWithToken("webhook", "token")
	if err != nil {
		t.Fatalf("WebhookDeleteWithToken returned error: %v", err)
	}
	if webhook != nil {
		t.Fatalf("WebhookDeleteWithToken returned webhook = %#v, want nil", webhook)
	}
}

func TestWebhookTokenEndpointsOmitAuthorization(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) error
		body string
	}{
		{
			name: "get webhook with token",
			call: func(s *Session) error {
				_, err := s.WebhookWithToken("webhook", "token")
				return err
			},
			body: `{"id":"webhook","type":1}`,
		},
		{
			name: "execute webhook",
			call: func(s *Session) error {
				_, err := s.WebhookExecute("webhook", "token", false, &WebhookParams{Content: "hello"})
				return err
			},
			body: ``,
		},
		{
			name: "get webhook message",
			call: func(s *Session) error {
				_, err := s.WebhookMessage("webhook", "token", "message")
				return err
			},
			body: `{"id":"message","channel_id":"channel"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if got := r.Header.Get("Authorization"); got != "" {
					t.Fatalf("Authorization header = %q, want empty", got)
				}

				statusCode := http.StatusOK
				status := "200 OK"
				if tt.body == "" {
					statusCode = http.StatusNoContent
					status = "204 No Content"
				}

				return &http.Response{
					StatusCode: statusCode,
					Status:     status,
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			if err := tt.call(session); err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			if !called {
				t.Fatal("HTTP transport was not called")
			}
		})
	}
}

func TestInteractionTokenEndpointsOmitAuthorization(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) error
	}{
		{
			name: "initial response",
			call: func(s *Session) error {
				return s.InteractionRespond(&Interaction{ID: "interaction", Token: "token"}, &InteractionResponse{Type: InteractionResponsePong})
			},
		},
		{
			name: "followup message",
			call: func(s *Session) error {
				_, err := s.FollowupMessageCreate(&Interaction{AppID: "application", Token: "token"}, false, &WebhookParams{Content: "hello"})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if got := r.Header.Get("Authorization"); got != "" {
					t.Fatalf("Authorization header = %q, want empty", got)
				}

				return &http.Response{
					StatusCode: http.StatusNoContent,
					Status:     "204 No Content",
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    r,
				}, nil
			})

			if err := tt.call(session); err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			if !called {
				t.Fatal("HTTP transport was not called")
			}
		})
	}
}

func TestAuthenticatedWebhookEndpointKeepsAuthorization(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	called := false
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if got := r.Header.Get("Authorization"); got != "Bot secret" {
			t.Fatalf("Authorization header = %q, want Bot secret", got)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"webhook","type":1}`)),
			Request:    r,
		}, nil
	})

	if _, err := session.Webhook("webhook"); err != nil {
		t.Fatalf("Webhook returned error: %v", err)
	}
	if !called {
		t.Fatal("HTTP transport was not called")
	}
}

func TestPermissionAllIncludesCurrentPermissionFlags(t *testing.T) {
	tests := []struct {
		name       string
		permission int64
	}{
		{"CreateInstantInvite", PermissionCreateInstantInvite},
		{"KickMembers", PermissionKickMembers},
		{"BanMembers", PermissionBanMembers},
		{"Administrator", PermissionAdministrator},
		{"ManageChannels", PermissionManageChannels},
		{"ManageGuild", PermissionManageGuild},
		{"AddReactions", PermissionAddReactions},
		{"ViewAuditLogs", PermissionViewAuditLogs},
		{"VoicePrioritySpeaker", PermissionVoicePrioritySpeaker},
		{"VoiceStreamVideo", PermissionVoiceStreamVideo},
		{"ViewChannel", PermissionViewChannel},
		{"SendMessages", PermissionSendMessages},
		{"SendTTSMessages", PermissionSendTTSMessages},
		{"ManageMessages", PermissionManageMessages},
		{"EmbedLinks", PermissionEmbedLinks},
		{"AttachFiles", PermissionAttachFiles},
		{"ReadMessageHistory", PermissionReadMessageHistory},
		{"MentionEveryone", PermissionMentionEveryone},
		{"UseExternalEmojis", PermissionUseExternalEmojis},
		{"ViewGuildInsights", PermissionViewGuildInsights},
		{"VoiceConnect", PermissionVoiceConnect},
		{"VoiceSpeak", PermissionVoiceSpeak},
		{"VoiceMuteMembers", PermissionVoiceMuteMembers},
		{"VoiceDeafenMembers", PermissionVoiceDeafenMembers},
		{"VoiceMoveMembers", PermissionVoiceMoveMembers},
		{"VoiceUseVAD", PermissionVoiceUseVAD},
		{"ChangeNickname", PermissionChangeNickname},
		{"ManageNicknames", PermissionManageNicknames},
		{"ManageRoles", PermissionManageRoles},
		{"ManageWebhooks", PermissionManageWebhooks},
		{"ManageGuildExpressions", PermissionManageGuildExpressions},
		{"UseApplicationCommands", PermissionUseApplicationCommands},
		{"VoiceRequestToSpeak", PermissionVoiceRequestToSpeak},
		{"ManageEvents", PermissionManageEvents},
		{"ManageThreads", PermissionManageThreads},
		{"CreatePublicThreads", PermissionCreatePublicThreads},
		{"CreatePrivateThreads", PermissionCreatePrivateThreads},
		{"UseExternalStickers", PermissionUseExternalStickers},
		{"SendMessagesInThreads", PermissionSendMessagesInThreads},
		{"UseEmbeddedActivities", PermissionUseEmbeddedActivities},
		{"ModerateMembers", PermissionModerateMembers},
		{"ViewCreatorMonetizationAnalytics", PermissionViewCreatorMonetizationAnalytics},
		{"UseSoundboard", PermissionUseSoundboard},
		{"CreateGuildExpressions", PermissionCreateGuildExpressions},
		{"CreateEvents", PermissionCreateEvents},
		{"UseExternalSounds", PermissionUseExternalSounds},
		{"SendVoiceMessages", PermissionSendVoiceMessages},
		{"SetVoiceChannelStatus", PermissionSetVoiceChannelStatus},
		{"SendPolls", PermissionSendPolls},
		{"UseExternalApps", PermissionUseExternalApps},
		{"PinMessages", PermissionPinMessages},
		{"BypassSlowmode", PermissionBypassSlowmode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if PermissionAll&tt.permission != tt.permission {
				t.Fatalf("PermissionAll missing %s (%d)", tt.name, tt.permission)
			}
		})
	}
}

func TestPermissionAllChannelIncludesCurrentChannelPermissions(t *testing.T) {
	tests := []struct {
		name       string
		permission int64
	}{
		{"UseExternalEmojis", PermissionUseExternalEmojis},
		{"UseApplicationCommands", PermissionUseApplicationCommands},
		{"ManageEvents", PermissionManageEvents},
		{"ManageThreads", PermissionManageThreads},
		{"CreatePublicThreads", PermissionCreatePublicThreads},
		{"CreatePrivateThreads", PermissionCreatePrivateThreads},
		{"UseExternalStickers", PermissionUseExternalStickers},
		{"SendMessagesInThreads", PermissionSendMessagesInThreads},
		{"UseEmbeddedActivities", PermissionUseEmbeddedActivities},
		{"UseSoundboard", PermissionUseSoundboard},
		{"CreateEvents", PermissionCreateEvents},
		{"UseExternalSounds", PermissionUseExternalSounds},
		{"SendVoiceMessages", PermissionSendVoiceMessages},
		{"SetVoiceChannelStatus", PermissionSetVoiceChannelStatus},
		{"SendPolls", PermissionSendPolls},
		{"UseExternalApps", PermissionUseExternalApps},
		{"PinMessages", PermissionPinMessages},
		{"BypassSlowmode", PermissionBypassSlowmode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if PermissionAllChannel&tt.permission != tt.permission {
				t.Fatalf("PermissionAllChannel missing %s (%d)", tt.name, tt.permission)
			}
		})
	}
}

func TestMemberPermissionsAdministratorIncludesCurrentChannelPermissions(t *testing.T) {
	var currentChannelPermissions int64 = PermissionUseExternalEmojis |
		PermissionUseApplicationCommands |
		PermissionManageEvents |
		PermissionManageThreads |
		PermissionCreatePublicThreads |
		PermissionCreatePrivateThreads |
		PermissionUseExternalStickers |
		PermissionSendMessagesInThreads |
		PermissionUseEmbeddedActivities |
		PermissionUseSoundboard |
		PermissionCreateEvents |
		PermissionUseExternalSounds |
		PermissionSendVoiceMessages |
		PermissionSetVoiceChannelStatus |
		PermissionSendPolls |
		PermissionUseExternalApps |
		PermissionPinMessages |
		PermissionBypassSlowmode

	guild := &Guild{
		ID:      "guild",
		OwnerID: "owner",
		Roles: []*Role{
			{ID: "guild"},
			{ID: "admin", Permissions: PermissionAdministrator},
		},
	}
	channel := &Channel{
		GuildID: "guild",
		PermissionOverwrites: []*PermissionOverwrite{
			{
				ID:   "guild",
				Type: PermissionOverwriteTypeRole,
				Deny: currentChannelPermissions,
			},
		},
	}

	permissions := memberPermissions(guild, channel, "member", []string{"admin"})
	if permissions&currentChannelPermissions != currentChannelPermissions {
		t.Fatalf("administrator permissions missing current channel flags: got %d, want %d", permissions&currentChannelPermissions, currentChannelPermissions)
	}
}

func TestGateway(t *testing.T) {

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}
	_, err := dg.Gateway()
	if err != nil {
		t.Errorf("Gateway() returned error: %+v", err)
	}
}

func TestGatewayBot(t *testing.T) {

	if dgBot == nil {
		t.Skip("Skipping, dgBot not set.")
	}
	_, err := dgBot.GatewayBot()
	if err != nil {
		t.Errorf("GatewayBot() returned error: %+v", err)
	}
}

func TestVoiceRegions(t *testing.T) {

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.VoiceRegions()
	if err != nil {
		t.Errorf("VoiceRegions() returned error: %+v", err)
	}
}
func TestGuildRoles(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.GuildRoles(envGuild)
	if err != nil {
		t.Errorf("GuildRoles(envGuild) returned error: %+v", err)
	}

}

func TestGuildMemberNickname(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	err := dg.GuildMemberNickname(envGuild, "@me/nick", "B1nzyRocks")
	if err != nil {
		t.Errorf("GuildNickname returned error: %+v", err)
	}
}

// TestChannelMessageSend2 tests the ChannelMessageSend() function. This should not return an error.
func TestChannelMessageSend2(t *testing.T) {

	if envChannel == "" {
		t.Skip("Skipping, DG_CHANNEL not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.ChannelMessageSend(envChannel, "All done running REST API Tests!")
	if err != nil {
		t.Errorf("ChannelMessageSend returned error: %+v", err)
	}
}

// TestGuildPruneCount tests GuildPruneCount() function. This should not return an error.
func TestGuildPruneCount(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.GuildPruneCount(envGuild, 1)
	if err != nil {
		t.Errorf("GuildPruneCount returned error: %+v", err)
	}
}

/*
// TestGuildPrune tests GuildPrune() function. This should not return an error.
func TestGuildPrune(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.GuildPrune(envGuild, 1)
	if err != nil {
		t.Errorf("GuildPrune returned error: %+v", err)
	}
}
*/

func Test_unmarshal(t *testing.T) {
	err := unmarshal([]byte{}, &struct{}{})
	if !errors.Is(err, ErrJSONUnmarshal) {
		t.Errorf("Unexpected error type: %T", err)
	}
}

func TestWithContext(t *testing.T) {
	// Set up a test context.
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "value")

	// Set up a test client.
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	testErr := errors.New("test")

	// Intercept the request to assert the context.
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		val, _ := r.Context().Value(key{}).(string)
		if val != "value" {
			t.Errorf("missing value in context (got %q, wanted %q)", val, "value")
		}
		return nil, testErr
	})

	// Run any client method using WithContext.
	_, err = session.User("", WithContext(ctx))

	// Verify that the assertion code was actually run.
	if !errors.Is(err, testErr) {
		t.Errorf("unexpected error %v returned from client", err)
	}
}

func TestRequestWithLockedBucketNonJSONRateLimitUsesRetryAfter(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header: http.Header{
				"Retry-After": []string{"2"},
			},
			Body:    io.NopCloser(strings.NewReader("error code: 1015")),
			Request: r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if rateLimitErr.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %v, want %v", rateLimitErr.RetryAfter, 2*time.Second)
	}
	if rateLimitErr.Message != "error code: 1015" {
		t.Fatalf("Message = %q, want %q", rateLimitErr.Message, "error code: 1015")
	}
}

func TestRateLimitErrorRedactsWebhookToken(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":1}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointWebhookToken("webhook", "secret-token"), nil, webhookTokenBucketID("webhook"), WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if strings.Contains(rateLimitErr.URL, "secret-token") {
		t.Fatalf("RateLimit URL leaked webhook token: %q", rateLimitErr.URL)
	}
	if !strings.Contains(rateLimitErr.URL, redactedURLValue) {
		t.Fatalf("RateLimit URL = %q, want redacted token", rateLimitErr.URL)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("RateLimitError leaked webhook token: %q", err.Error())
	}
}

func TestRequestTransportErrorRedactsTokenURL(t *testing.T) {
	testErr := errors.New("network down")

	tests := []struct {
		name  string
		token string
		call  func(*Session) error
	}{
		{
			name:  "webhook token",
			token: "secret-token",
			call: func(session *Session) error {
				_, err := session.WebhookExecute("webhook", "secret-token", false, &WebhookParams{Content: "hello"})
				return err
			},
		},
		{
			name:  "interaction token",
			token: "interaction-token",
			call: func(session *Session) error {
				return session.InteractionRespond(&Interaction{ID: "interaction", Token: "interaction-token"}, &InteractionResponse{Type: InteractionResponsePong})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, testErr
			})

			err = tt.call(session)
			if !errors.Is(err, testErr) {
				t.Fatalf("error = %v, want wrapped network error", err)
			}
			var urlErr *url.Error
			if !errors.As(err, &urlErr) {
				t.Fatalf("error = %T %[1]v, want *url.Error", err)
			}
			if strings.Contains(err.Error(), tt.token) {
				t.Fatalf("transport error leaked token: %q", err.Error())
			}
			if strings.Contains(urlErr.URL, tt.token) {
				t.Fatalf("transport error URL leaked token: %q", urlErr.URL)
			}
			if !strings.Contains(urlErr.URL, redactedURLValue) {
				t.Fatalf("transport error URL = %q, want redacted token", urlErr.URL)
			}
		})
	}
}

func TestRateLimitEventRedactsWebhookToken(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true

	var gotURL string
	session.AddHandler(func(_ *Session, event *RateLimit) {
		gotURL = event.URL
	})

	requests := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":0}`)),
				Request:    r,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointWebhookToken("webhook", "secret-token"), nil, webhookTokenBucketID("webhook"))
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if strings.Contains(gotURL, "secret-token") {
		t.Fatalf("RateLimit event URL leaked webhook token: %q", gotURL)
	}
	if !strings.Contains(gotURL, redactedURLValue) {
		t.Fatalf("RateLimit event URL = %q, want redacted token", gotURL)
	}
}

func TestRequestRawBucketWaitRespectsContext(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		t.Fatal("HTTP transport should not be called after context deadline")
		return nil, nil
	})

	bucket := session.Ratelimiter.GetBucket(EndpointGateway)
	bucket.Lock()
	bucket.Remaining = 0
	bucket.reset = time.Now().Add(10 * time.Second)
	bucket.setReset(bucket.reset)
	bucket.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RequestWithBucketID() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RequestWithBucketID() slept for %v after context deadline", elapsed)
	}
	if called {
		t.Fatal("HTTP transport was called after context deadline")
	}
	if active := atomic.LoadInt32(&bucket.activeRequests); active != 0 {
		t.Fatalf("activeRequests = %d, want 0", active)
	}
}

func TestRequestWithLockedBucketClosesRateLimitBodyBeforeRetry(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	firstBody := &closeNotifyReadCloser{
		reader: strings.NewReader(`{"message":"rate limited","retry_after":0.001,"global":false}`),
		closed: make(chan struct{}),
	}
	attempts := 0

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       firstBody,
				Request:    r,
			}, nil
		}

		select {
		case <-firstBody.closed:
		case <-time.After(time.Second):
			t.Fatal("rate-limit response body was not closed before retry")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway)
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRequestWithLockedBucketRetriesGetServerError(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`server error`)),
				Request:    r,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway)
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRequestWithLockedBucketDoesNotRetryPostServerError(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`server error`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("POST", EndpointChannelMessages("channel"), map[string]string{
		"content": "hello",
	}, EndpointChannelMessages("channel"))
	if err == nil {
		t.Fatal("RequestWithBucketID() returned nil error, want RESTError")
	}
	var restErr *RESTError
	if !errors.As(err, &restErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RESTError", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRequestWithLockedBucketGlobalRateLimitSetsGlobalReset(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header: http.Header{
				"Retry-After":       []string{"1"},
				"X-RateLimit-Scope": []string{"global"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"message":"You are being rate limited.","retry_after":1,"global":true}`)),
			Request: r,
		}, nil
	})

	before := time.Now()
	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if !rateLimitErr.Global {
		t.Fatal("RateLimitError.Global = false, want true")
	}

	reset := time.Unix(0, atomic.LoadInt64(session.Ratelimiter.global))
	if !reset.After(before) {
		t.Fatalf("global reset = %v, want after %v", reset, before)
	}
}

func TestChannelMessagesPinnedUsesGlobalBucket(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header: http.Header{
				"X-RateLimit-Remaining":   []string{"1"},
				"X-RateLimit-Reset-After": []string{"0.1"},
			},
			Body:    ioutil.NopCloser(strings.NewReader(`{"items":[],"has_more":false}`)),
			Request: r,
		}, nil
	})

	before := time.Now()
	if _, err = session.ChannelMessagesPinned("channel-a", &before, 10); err != nil {
		t.Fatalf("ChannelMessagesPinned returned error: %v", err)
	}
	if _, err = session.ChannelMessagesPinned("channel-b", nil, 0); err != nil {
		t.Fatalf("ChannelMessagesPinned returned error: %v", err)
	}

	session.Ratelimiter.Lock()
	defer session.Ratelimiter.Unlock()

	want := EndpointChannelMessagesPins("")
	if _, ok := session.Ratelimiter.buckets[want]; !ok {
		t.Fatalf("bucket %q was not created", want)
	}
	if len(session.Ratelimiter.buckets) != 1 {
		t.Fatalf("bucket count = %d, want 1", len(session.Ratelimiter.buckets))
	}
	for key := range session.Ratelimiter.buckets {
		if strings.Contains(key, "?") {
			t.Fatalf("bucket %q includes query parameters", key)
		}
		if strings.Contains(key, "channel-a") || strings.Contains(key, "channel-b") {
			t.Fatalf("bucket %q includes channel ID", key)
		}
	}
}

func TestRedactedHeaderValues(t *testing.T) {
	values := redactedHeaderValues("Authorization", []string{"Bot secret"})
	if len(values) != 1 || values[0] != redactedValue {
		t.Fatalf("redactedHeaderValues() = %#v, want %q", values, redactedValue)
	}

	values = redactedHeaderValues("User-Agent", []string{"discordgo"})
	if len(values) != 1 || values[0] != "discordgo" {
		t.Fatalf("redactedHeaderValues() changed non-secret header: %#v", values)
	}
}

func TestNewRestErrorRedactsRequestSecrets(t *testing.T) {
	req, err := http.NewRequest("GET", EndpointWebhookToken("webhook", "secret-token"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bot secret")
	req.Header.Set("User-Agent", "discordgo")
	resp := &http.Response{
		Status:     "401 Unauthorized",
		StatusCode: http.StatusUnauthorized,
		Request:    req,
	}

	restErr := newRestError(req, resp, []byte(`{"code":0,"message":"unauthorized"}`))

	if got := restErr.Request.Header.Get("Authorization"); got != redactedValue {
		t.Fatalf("RESTError request authorization = %q, want %q", got, redactedValue)
	}
	if got := restErr.Response.Request.Header.Get("Authorization"); got != redactedValue {
		t.Fatalf("RESTError response request authorization = %q, want %q", got, redactedValue)
	}
	if got := restErr.Request.URL.String(); strings.Contains(got, "secret-token") {
		t.Fatalf("RESTError request URL leaked webhook token: %q", got)
	}
	if got := restErr.Response.Request.URL.String(); strings.Contains(got, "secret-token") {
		t.Fatalf("RESTError response request URL leaked webhook token: %q", got)
	}
	if got := restErr.Request.URL.String(); !strings.Contains(got, redactedURLValue) {
		t.Fatalf("RESTError request URL = %q, want redacted token", got)
	}
	if got := restErr.Request.Header.Get("User-Agent"); got != "discordgo" {
		t.Fatalf("RESTError request user agent = %q, want discordgo", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bot secret" {
		t.Fatalf("original request authorization = %q, want Bot secret", got)
	}
	if got := req.URL.String(); !strings.Contains(got, "secret-token") {
		t.Fatalf("original request URL = %q, want original token", got)
	}
}

func TestRedactedURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "webhook token",
			url:  "https://discord.com/api/v10/webhooks/123456789/token-value/messages/@original?wait=true",
			want: "https://discord.com/api/v10/webhooks/123456789/REDACTED/messages/@original?wait=true",
		},
		{
			name: "interaction token",
			url:  "https://discord.com/api/v10/interactions/123456789/token-value/callback",
			want: "https://discord.com/api/v10/interactions/123456789/REDACTED/callback",
		},
		{
			name: "ordinary endpoint",
			url:  "https://discord.com/api/v10/channels/123/messages",
			want: "https://discord.com/api/v10/channels/123/messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactedURL(tt.url); got != tt.want {
				t.Fatalf("redactedURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactedIdentify(t *testing.T) {
	op := identifyOp{2, Identify{Token: "Bot secret"}}

	redacted := redactedIdentify(op)
	if redacted.Data.Token != redactedValue {
		t.Fatalf("redacted token = %q, want %q", redacted.Data.Token, redactedValue)
	}
	if op.Data.Token != "Bot secret" {
		t.Fatalf("redactedIdentify mutated original token: %q", op.Data.Token)
	}
}

func TestRedactedRESTBody(t *testing.T) {
	body := []byte(`{"access_token":"access-value","token":"webhook-value","password":"password-value","nested":{"refresh_token":"refresh-value","client_secret":"client-secret-value","safe":"ok"},"items":[{"token":"item-token-value"}]}`)

	got := redactedRESTBody(body)
	for _, secret := range []string{"access-value", "webhook-value", "password-value", "refresh-value", "client-secret-value", "item-token-value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedRESTBody() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedRESTBody() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedRESTBody() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedRESTBodyInvalidJSON(t *testing.T) {
	body := []byte("not-json")

	if got := redactedRESTBody(body); got != string(body) {
		t.Fatalf("redactedRESTBody() = %q, want %q", got, string(body))
	}
}

func TestRedactedRESTBodyMultipart(t *testing.T) {
	body := []byte("--boundary\r\nContent-Disposition: form-data; name=\"files[0]\"; filename=\"upload.txt\"\r\n\r\nprivate upload contents\r\n--boundary--")

	got := redactedRESTBody(body, "multipart/form-data; boundary=boundary")
	if got != redactedMultipartBody {
		t.Fatalf("redactedRESTBody() = %q, want %q", got, redactedMultipartBody)
	}
	for _, secret := range []string{"upload.txt", "private upload contents"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedRESTBody() leaked %q in %s", secret, got)
		}
	}
}

// roundTripperFunc implements http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type closeNotifyReadCloser struct {
	reader *strings.Reader
	closed chan struct{}
}

func (r *closeNotifyReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *closeNotifyReadCloser) Close() error {
	close(r.closed)
	return nil
}
