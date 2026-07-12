package discordgo

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func voiceStateString(value string) *string {
	return &value
}

func voiceStateBool(value bool) *bool {
	return &value
}

func voiceStateTime(value time.Time) **time.Time {
	pointer := &value
	return &pointer
}

func newVoiceStateRESTSession(t *testing.T, handler http.Handler) *Session {
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

func TestUserVoiceStateCurrentUserRoute(t *testing.T) {
	session := newVoiceStateRESTSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/voice-states/@me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/voice-states/@me")
		}
		if got := r.Header.Get("X-Voice-Test"); got != "current user" {
			t.Fatalf("X-Voice-Test = %q, want %q", got, "current user")
		}

		_, _ = w.Write([]byte(`{
			"guild_id":"guild",
			"channel_id":"stage",
			"user_id":"current",
			"session_id":"session",
			"deaf":false,
			"mute":false,
			"self_deaf":false,
			"self_mute":false,
			"self_video":false,
			"suppress":true,
			"request_to_speak_timestamp":null
		}`))
	}))

	state, err := session.UserVoiceState("guild", "@me", WithHeader("X-Voice-Test", "current user"))
	if err != nil {
		t.Fatalf("UserVoiceState returned error: %v", err)
	}
	if state == nil || state.GuildID != "guild" || state.ChannelID != "stage" || state.UserID != "current" || !state.Suppress {
		t.Fatalf("state = %#v", state)
	}
}

func TestVoiceStateEditRequests(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 34, 56, 0, time.UTC)
	var nullTime *time.Time

	tests := []struct {
		name string
		path string
		body string
		call func(*Session, ...RequestOption) error
	}{
		{
			name: "current user omits unset fields",
			path: "/guilds/guild/voice-states/@me",
			body: `{}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{}, options...)
			},
		},
		{
			name: "current user sends values including false",
			path: "/guilds/guild/voice-states/@me",
			body: `{"channel_id":"stage","suppress":false,"request_to_speak_timestamp":"2026-07-13T12:34:56Z"}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{
					ChannelID:               voiceStateString("stage"),
					Suppress:                voiceStateBool(false),
					RequestToSpeakTimestamp: voiceStateTime(timestamp),
				}, options...)
			},
		},
		{
			name: "current user clears request to speak",
			path: "/guilds/guild/voice-states/@me",
			body: `{"request_to_speak_timestamp":null}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{
					RequestToSpeakTimestamp: &nullTime,
				}, options...)
			},
		},
		{
			name: "user omits unset fields",
			path: "/guilds/guild/voice-states/user",
			body: `{}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.UserVoiceStateEdit("guild", "user", &UserVoiceStateEditParams{}, options...)
			},
		},
		{
			name: "user sends values including false",
			path: "/guilds/guild/voice-states/user",
			body: `{"channel_id":"stage","suppress":false}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.UserVoiceStateEdit("guild", "user", &UserVoiceStateEditParams{
					ChannelID: voiceStateString("stage"),
					Suppress:  voiceStateBool(false),
				}, options...)
			},
		},
	}

	session := newVoiceStateRESTSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}

		name := r.Header.Get("X-Voice-Test")
		var expectedPath, expectedBody string
		for _, tt := range tests {
			if tt.name == name {
				expectedPath = tt.path
				expectedBody = tt.body
				break
			}
		}
		if expectedPath == "" {
			t.Fatalf("unexpected X-Voice-Test %q", name)
		}
		if r.URL.Path != expectedPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, expectedPath)
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		assertJSONEqual(t, body, []byte(expectedBody))
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(session, WithHeader("X-Voice-Test", tt.name)); err != nil {
				t.Fatalf("voice state edit returned error: %v", err)
			}
		})
	}
}

func TestVoiceStateEditErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Session, ...RequestOption) error
	}{
		{
			name: "current user",
			path: "/guilds/guild/voice-states/@me",
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{}, options...)
			},
		},
		{
			name: "user",
			path: "/guilds/guild/voice-states/user",
			call: func(session *Session, options ...RequestOption) error {
				return session.UserVoiceStateEdit("guild", "user", &UserVoiceStateEditParams{}, options...)
			},
		},
	}

	session := newVoiceStateRESTSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}

		name := r.Header.Get("X-Voice-Test")
		var expectedPath string
		for _, tt := range tests {
			if tt.name == name {
				expectedPath = tt.path
				break
			}
		}
		if expectedPath == "" {
			t.Fatalf("unexpected X-Voice-Test %q", name)
		}
		if r.URL.Path != expectedPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, expectedPath)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":50035,"message":"Invalid Form Body"}`))
	}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(session, WithHeader("X-Voice-Test", tt.name))
			if err == nil {
				t.Fatal("voice state edit returned nil error")
			}

			var restErr *RESTError
			if !errors.As(err, &restErr) {
				t.Fatalf("error = %T, want *RESTError", err)
			}
			if restErr.Response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", restErr.Response.StatusCode, http.StatusBadRequest)
			}
		})
	}
}
