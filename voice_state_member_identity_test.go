package discordgo

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestVoiceStateMembersPreserveGuildIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		event        string
		userID       string
		stateEnabled bool
	}{
		{name: "REST user", userID: "200", stateEnabled: true},
		{name: "REST current user", userID: "@me", stateEnabled: true},
		{name: "voice update", event: "VOICE_STATE_UPDATE", stateEnabled: true},
		{name: "voice update without state", event: "VOICE_STATE_UPDATE"},
		{name: "guild create", event: "GUILD_CREATE", stateEnabled: true},
		{name: "guild create without state", event: "GUILD_CREATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			session.StateEnabled = tc.stateEnabled
			session.State.User = &User{ID: "bot"}
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			payload := `{"channel_id":"300","user_id":"200","session_id":"voice-session","deaf":false,"mute":false,"self_deaf":false,"self_mute":false,"self_video":false,"suppress":false,"request_to_speak_timestamp":null,
				"member":{"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}`
			var voice *VoiceState
			if tc.event == "" {
				session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodGet || req.URL.String() != EndpointGuildMemberVoiceState("100", tc.userID) {
						t.Errorf("request = %s %s, want voice-state GET", req.Method, req.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				voice, err = session.UserVoiceState("100", tc.userID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				capture := func(state *VoiceState) {
					voice = state
					if state == nil || state.Member == nil || state.Member.GuildID != "100" {
						t.Error("callback received a member without guild identity")
					}
					if tc.stateEnabled {
						cached, err := session.State.VoiceState("100", "200")
						if err != nil || cached != state {
							t.Errorf("voice state not cached before callback: %v", err)
						}
					}
				}
				switch tc.event {
				case "VOICE_STATE_UPDATE":
					payload = `{"guild_id":"100",` + payload[1:]
					session.AddHandler(func(_ *Session, event *VoiceStateUpdate) { capture(event.VoiceState) })
				case "GUILD_CREATE":
					payload = `{"id":"100","voice_states":[` + payload + `]}`
					session.AddHandler(func(_ *Session, event *GuildCreate) { capture(event.VoiceStates[0]) })
				}
				if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":%q,"d":%s}`, tc.event, payload))); err != nil {
					t.Fatal(err)
				}
			}
			if voice == nil || voice.Member == nil || voice.Member.User == nil {
				t.Fatal("voice state or member missing")
			}
			member := voice.Member
			if voice.GuildID != "100" || member.GuildID != "100" {
				t.Errorf("guild identity = voice %q, member %q, want 100", voice.GuildID, member.GuildID)
			}
			if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
				t.Errorf("AvatarURL = %q, want %q", got, want)
			}
			if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
				t.Errorf("BannerURL = %q, want %q", got, want)
			}
			if err := session.State.MemberAdd(member); err != nil {
				t.Errorf("caching voice member failed: %v", err)
			} else if cached, err := session.State.Member("100", "200"); err != nil || cached.User.ID != "200" {
				t.Errorf("voice member not retrievable from its guild: %v", err)
			}
		})
	}
}

func TestUserVoiceStateOptionalMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"user_id":"200","channel_id":null}`, `null`} {
		session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
		})
		voice, err := session.UserVoiceState("100", "200")
		if err != nil {
			t.Fatal(err)
		}
		if payload == "null" {
			if voice != nil {
				t.Fatal("null response produced a voice state")
			}
		} else if voice == nil || voice.Member != nil {
			t.Fatal("omitted member was not preserved")
		}
	}
}
