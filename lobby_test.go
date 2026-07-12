package discordgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLobbyRESTEndpoints(t *testing.T) {
	metadata := map[string]string{"mode": "ranked"}
	nullMetadata := map[string]string(nil)
	members := []LobbyMemberParams{
		{
			ID:       "user",
			Metadata: &nullMetadata,
		},
	}
	emptyMembers := []LobbyMemberParams{}
	timeout := 300
	canLink := LobbyMemberFlagCanLinkLobby
	noFlags := LobbyMemberFlags(0)
	messageFlags := MessageFlagsSuppressNotifications

	lobbyResponse := `{
		"id":"lobby",
		"application_id":"app",
		"metadata":null,
		"members":[{"id":"user","metadata":null,"flags":1}],
		"linked_channel":{"id":"channel","type":0}
	}`
	messageResponse := `{
		"id":"message",
		"type":0,
		"content":"hello",
		"lobby_id":"lobby",
		"channel_id":"lobby",
		"author":{"id":"user","username":"tester"},
		"metadata":null,
		"moderation_metadata":{"decision":"allow"},
		"flags":4096,
		"application_id":"app"
	}`

	tests := []struct {
		name          string
		method        string
		path          string
		query         string
		authorization string
		body          string
		status        int
		response      string
		call          func(*Session) error
	}{
		{
			name:          "create lobby",
			method:        http.MethodPost,
			path:          "/lobbies",
			authorization: "Bot token",
			body:          `{"metadata":{"mode":"ranked"},"members":[{"id":"user","metadata":null}],"idle_timeout_seconds":300}`,
			status:        http.StatusCreated,
			response:      lobbyResponse,
			call: func(s *Session) error {
				lobby, err := s.LobbyCreate(&LobbyParams{
					Metadata:           &metadata,
					Members:            &members,
					IdleTimeoutSeconds: &timeout,
				})
				return checkLobby(lobby, err)
			},
		},
		{
			name:          "create or join lobby",
			method:        http.MethodPut,
			path:          "/lobbies",
			authorization: "Bearer token",
			body:          `{"secret":"match-secret","idle_timeout_seconds":300,"lobby_metadata":null,"member_metadata":{"mode":"ranked"}}`,
			response:      lobbyResponse,
			call: func(s *Session) error {
				lobby, err := s.LobbyCreateOrJoin(&LobbyCreateOrJoinParams{
					Secret:             "match-secret",
					IdleTimeoutSeconds: &timeout,
					LobbyMetadata:      &nullMetadata,
					MemberMetadata:     &metadata,
				})
				return checkLobby(lobby, err)
			},
		},
		{
			name:          "get lobby",
			method:        http.MethodGet,
			path:          "/lobbies/lobby",
			authorization: "Bot token",
			response:      lobbyResponse,
			call: func(s *Session) error {
				lobby, err := s.Lobby("lobby")
				return checkLobby(lobby, err)
			},
		},
		{
			name:          "modify lobby and clear members",
			method:        http.MethodPatch,
			path:          "/lobbies/lobby",
			authorization: "Bot token",
			body:          `{"metadata":null,"members":[],"idle_timeout_seconds":300}`,
			response:      lobbyResponse,
			call: func(s *Session) error {
				lobby, err := s.LobbyEdit("lobby", &LobbyParams{
					Metadata:           &nullMetadata,
					Members:            &emptyMembers,
					IdleTimeoutSeconds: &timeout,
				})
				return checkLobby(lobby, err)
			},
		},
		{
			name:          "delete lobby",
			method:        http.MethodDelete,
			path:          "/lobbies/lobby",
			authorization: "Bot token",
			status:        http.StatusNoContent,
			call: func(s *Session) error {
				return s.LobbyDelete("lobby")
			},
		},
		{
			name:          "add lobby member",
			method:        http.MethodPut,
			path:          "/lobbies/lobby/members/user",
			authorization: "Bot token",
			body:          `{"metadata":null,"flags":0}`,
			response:      `{"id":"user","metadata":null,"flags":0}`,
			call: func(s *Session) error {
				member, err := s.LobbyMemberAdd("lobby", "user", &LobbyMemberParams{
					ID:       "ignored-body-id",
					Metadata: &nullMetadata,
					Flags:    &noFlags,
				})
				if err != nil {
					return err
				}
				if member == nil || member.ID != "user" || member.Metadata != nil || member.Flags != 0 {
					return fmt.Errorf("unexpected member: %#v", member)
				}
				return nil
			},
		},
		{
			name:          "bulk update lobby members",
			method:        http.MethodPost,
			path:          "/lobbies/lobby/members/bulk",
			authorization: "Bot token",
			body:          `[{"id":"user","metadata":{"mode":"ranked"},"flags":1},{"id":"removed","remove_member":true}]`,
			response:      `[{"id":"user","metadata":{"mode":"ranked"},"flags":1}]`,
			call: func(s *Session) error {
				updated, err := s.LobbyMembersBulkUpdate("lobby", []LobbyMemberUpdateParams{
					{ID: "user", Metadata: &metadata, Flags: &canLink},
					{ID: "removed", RemoveMember: true},
				})
				if err != nil {
					return err
				}
				if len(updated) != 1 || updated[0].ID != "user" || updated[0].Flags != canLink {
					return fmt.Errorf("unexpected updated members: %#v", updated)
				}
				return nil
			},
		},
		{
			name:          "remove lobby member",
			method:        http.MethodDelete,
			path:          "/lobbies/lobby/members/user",
			authorization: "Bot token",
			status:        http.StatusNoContent,
			call: func(s *Session) error {
				return s.LobbyMemberRemove("lobby", "user")
			},
		},
		{
			name:          "leave lobby",
			method:        http.MethodDelete,
			path:          "/lobbies/lobby/members/@me",
			authorization: "Bearer token",
			status:        http.StatusNoContent,
			call: func(s *Session) error {
				return s.LobbyLeave("lobby")
			},
		},
		{
			name:          "link channel",
			method:        http.MethodPatch,
			path:          "/lobbies/lobby/channel-linking",
			authorization: "Bearer token",
			body:          `{"channel_id":"channel"}`,
			response:      lobbyResponse,
			call: func(s *Session) error {
				lobby, err := s.LobbyChannelLink("lobby", "channel")
				return checkLobby(lobby, err)
			},
		},
		{
			name:          "unlink channel",
			method:        http.MethodPatch,
			path:          "/lobbies/lobby/channel-linking",
			authorization: "Bearer token",
			body:          `{}`,
			response:      `{"id":"lobby","application_id":"app","metadata":{},"members":[]}`,
			call: func(s *Session) error {
				lobby, err := s.LobbyChannelUnlink("lobby")
				if err != nil {
					return err
				}
				if lobby == nil || lobby.LinkedChannel != nil {
					return fmt.Errorf("unexpected lobby: %#v", lobby)
				}
				return nil
			},
		},
		{
			name:          "send lobby message",
			method:        http.MethodPost,
			path:          "/lobbies/lobby/messages",
			authorization: "Bearer token",
			body:          `{"content":"hello","metadata":null,"flags":4096}`,
			status:        http.StatusCreated,
			response:      messageResponse,
			call: func(s *Session) error {
				message, err := s.LobbyMessageSend("lobby", &LobbyMessageSendParams{
					Content:  "hello",
					Metadata: &nullMetadata,
					Flags:    &messageFlags,
				})
				return checkLobbyMessage(message, err)
			},
		},
		{
			name:          "get lobby messages",
			method:        http.MethodGet,
			path:          "/lobbies/lobby/messages",
			query:         "limit=200",
			authorization: "Bearer token",
			response:      "[" + messageResponse + "]",
			call: func(s *Session) error {
				messages, err := s.LobbyMessages("lobby", 200)
				if err != nil {
					return err
				}
				if len(messages) != 1 {
					return fmt.Errorf("messages length = %d, want 1", len(messages))
				}
				return checkLobbyMessage(messages[0], nil)
			},
		},
		{
			name:          "get lobby messages with default limit",
			method:        http.MethodGet,
			path:          "/lobbies/lobby/messages",
			authorization: "Bearer token",
			response:      `[]`,
			call: func(s *Session) error {
				messages, err := s.LobbyMessages("lobby", 0)
				if err != nil {
					return err
				}
				if len(messages) != 0 {
					return fmt.Errorf("messages length = %d, want 0", len(messages))
				}
				return nil
			},
		},
		{
			name:          "update moderation metadata",
			method:        http.MethodPut,
			path:          "/lobbies/lobby/messages/message/moderation-metadata",
			authorization: "Bot token",
			body:          `{"decision":"allow"}`,
			status:        http.StatusNoContent,
			call: func(s *Session) error {
				return s.LobbyMessageModerationMetadataUpdate("lobby", "message", map[string]string{"decision": "allow"})
			},
		},
		{
			name:          "create channel invite for self",
			method:        http.MethodPost,
			path:          "/lobbies/lobby/members/@me/invites",
			authorization: "Bearer token",
			response:      `{"code":"invite"}`,
			call: func(s *Session) error {
				invite, err := s.LobbyChannelInviteCreate("lobby")
				if err != nil {
					return err
				}
				if invite == nil || invite.Code != "invite" {
					return fmt.Errorf("unexpected invite: %#v", invite)
				}
				return nil
			},
		},
		{
			name:          "create channel invite for user",
			method:        http.MethodPost,
			path:          "/lobbies/lobby/members/user/invites",
			authorization: "Bot token",
			response:      `{"code":"invite"}`,
			call: func(s *Session) error {
				invite, err := s.LobbyChannelInviteCreateForUser("lobby", "user")
				if err != nil {
					return err
				}
				if invite == nil || invite.Code != "invite" {
					return fmt.Errorf("unexpected invite: %#v", invite)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method {
					t.Errorf("method = %q, want %q", r.Method, tt.method)
				}
				if r.URL.Path != tt.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tt.path)
				}
				if r.URL.RawQuery != tt.query {
					t.Errorf("query = %q, want %q", r.URL.RawQuery, tt.query)
				}
				if authorization := r.Header.Get("Authorization"); authorization != tt.authorization {
					t.Errorf("Authorization = %q, want %q", authorization, tt.authorization)
				}

				body, err := ioutil.ReadAll(r.Body)
				if err != nil {
					t.Errorf("ReadAll returned error: %v", err)
				}
				if tt.body == "" {
					if len(body) != 0 {
						t.Errorf("body = %s, want empty", body)
					}
				} else {
					assertLobbyJSONEqual(t, body, []byte(tt.body))
				}

				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				if tt.response != "" {
					_, _ = w.Write([]byte(tt.response))
				}
			}))
			defer server.Close()

			oldEndpointLobbies := EndpointLobbies
			EndpointLobbies = server.URL + "/lobbies"
			defer func() {
				EndpointLobbies = oldEndpointLobbies
			}()

			session, err := New(tt.authorization)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client = server.Client()

			if err := tt.call(session); err != nil {
				t.Fatalf("call returned error: %v", err)
			}
		})
	}
}

func TestLobbyOptionalJSONFields(t *testing.T) {
	nullMetadata := map[string]string(nil)
	emptyMetadata := map[string]string{}
	emptyMembers := []LobbyMemberParams{}
	noFlags := LobbyMemberFlags(0)

	tests := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{
			name:     "omit optional lobby fields",
			value:    LobbyParams{},
			expected: `{}`,
		},
		{
			name: "send null metadata and empty members",
			value: LobbyParams{
				Metadata: &nullMetadata,
				Members:  &emptyMembers,
			},
			expected: `{"metadata":null,"members":[]}`,
		},
		{
			name: "send empty metadata object",
			value: LobbyCreateOrJoinParams{
				Secret:         "secret",
				LobbyMetadata:  &emptyMetadata,
				MemberMetadata: &nullMetadata,
			},
			expected: `{"secret":"secret","lobby_metadata":{},"member_metadata":null}`,
		},
		{
			name: "send explicit zero member flags",
			value: LobbyMemberParams{
				Flags: &noFlags,
			},
			expected: `{"flags":0}`,
		},
		{
			name: "send null message metadata",
			value: LobbyMessageSendParams{
				Content:  "hello",
				Metadata: &nullMetadata,
			},
			expected: `{"content":"hello","metadata":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			assertLobbyJSONEqual(t, body, []byte(tt.expected))
		})
	}
}

func TestLobbyNilOptionalBodiesUseObjects(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) error
	}{
		{
			name: "create lobby",
			call: func(s *Session) error {
				_, err := s.LobbyCreate(nil)
				return err
			},
		},
		{
			name: "edit lobby",
			call: func(s *Session) error {
				_, err := s.LobbyEdit("lobby", nil)
				return err
			},
		},
		{
			name: "add lobby member",
			call: func(s *Session) error {
				_, err := s.LobbyMemberAdd("lobby", "user", nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := ioutil.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("ReadAll returned error: %v", err)
				}
				assertLobbyJSONEqual(t, body, []byte(`{}`))
				_, _ = w.Write([]byte(`{"id":"lobby","application_id":"app","metadata":{},"members":[]}`))
			}))
			defer server.Close()

			oldEndpointLobbies := EndpointLobbies
			EndpointLobbies = server.URL + "/lobbies"
			defer func() {
				EndpointLobbies = oldEndpointLobbies
			}()

			session, err := New("Bot token")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client = server.Client()
			if err := tt.call(session); err != nil {
				t.Fatalf("call returned error: %v", err)
			}
		})
	}
}

func TestLobbyRESTError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":50035,"message":"Invalid Form Body"}`))
	}))
	defer server.Close()

	oldEndpointLobbies := EndpointLobbies
	EndpointLobbies = server.URL + "/lobbies"
	defer func() {
		EndpointLobbies = oldEndpointLobbies
	}()

	session, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	lobby, err := session.Lobby("lobby")
	if lobby != nil {
		t.Fatalf("lobby = %#v, want nil", lobby)
	}
	if err == nil {
		t.Fatal("Lobby returned nil error")
	}
	var restErr *RESTError
	if !errors.As(err, &restErr) {
		t.Fatalf("error = %T, want *RESTError", err)
	}
	if restErr.Response.StatusCode != http.StatusBadRequest || restErr.Message == nil || restErr.Message.Code != 50035 {
		t.Fatalf("unexpected REST error: %#v", restErr)
	}
}

func TestLobbyInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":`))
	}))
	defer server.Close()

	oldEndpointLobbies := EndpointLobbies
	EndpointLobbies = server.URL + "/lobbies"
	defer func() {
		EndpointLobbies = oldEndpointLobbies
	}()

	session, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	lobby, err := session.Lobby("lobby")
	if lobby != nil {
		t.Fatalf("lobby = %#v, want nil", lobby)
	}
	if !errors.Is(err, ErrJSONUnmarshal) {
		t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
	}
}

func checkLobby(lobby *Lobby, err error) error {
	if err != nil {
		return err
	}
	if lobby == nil || lobby.ID != "lobby" || lobby.ApplicationID != "app" {
		return fmt.Errorf("unexpected lobby: %#v", lobby)
	}
	if lobby.Metadata != nil || len(lobby.Members) != 1 || lobby.Members[0].Metadata != nil {
		return fmt.Errorf("unexpected nullable lobby fields: %#v", lobby)
	}
	if lobby.Members[0].Flags != LobbyMemberFlagCanLinkLobby {
		return fmt.Errorf("member flags = %d, want %d", lobby.Members[0].Flags, LobbyMemberFlagCanLinkLobby)
	}
	if lobby.LinkedChannel == nil || lobby.LinkedChannel.ID != "channel" {
		return fmt.Errorf("unexpected linked channel: %#v", lobby.LinkedChannel)
	}
	return nil
}

func checkLobbyMessage(message *LobbyMessage, err error) error {
	if err != nil {
		return err
	}
	if message == nil || message.Message == nil {
		return fmt.Errorf("unexpected message: %#v", message)
	}
	if message.ID != "message" || message.LobbyID != "lobby" || message.ChannelID != "lobby" {
		return fmt.Errorf("unexpected message IDs: %#v", message)
	}
	if message.Author == nil || message.Author.ID != "user" || message.ApplicationID != "app" {
		return fmt.Errorf("unexpected message author or application: %#v", message)
	}
	if message.Metadata != nil || message.ModerationMetadata["decision"] != "allow" {
		return fmt.Errorf("unexpected message metadata: %#v", message)
	}
	if message.Flags != MessageFlagsSuppressNotifications {
		return fmt.Errorf("message flags = %d, want %d", message.Flags, MessageFlagsSuppressNotifications)
	}
	return nil
}

func assertLobbyJSONEqual(t *testing.T, actual, expected []byte) {
	t.Helper()

	var actualJSON interface{}
	if err := json.Unmarshal(actual, &actualJSON); err != nil {
		t.Fatalf("actual body is not JSON: %q: %v", actual, err)
	}
	var expectedJSON interface{}
	if err := json.Unmarshal(expected, &expectedJSON); err != nil {
		t.Fatalf("expected body is not JSON: %q: %v", expected, err)
	}
	if !reflect.DeepEqual(actualJSON, expectedJSON) {
		t.Errorf("body = %s, want %s", actual, expected)
	}
}
