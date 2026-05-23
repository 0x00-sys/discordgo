package discordgo

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
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

// roundTripperFunc implements http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
