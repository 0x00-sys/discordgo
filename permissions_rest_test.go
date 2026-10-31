package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func computedPermissionsSession(t *testing.T, handler func(*http.Request) (int, string)) *Session {
	t.Helper()

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		status, body := handler(r)
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})
	return session
}

func TestComputedPermissionsEndpoints(t *testing.T) {
	implicitFalse := false
	implicitTrue := true
	const (
		permissionsBody = `{"permissions":"2251799813685247"}`
		wantPermissions = int64(2251799813685247)
	)

	tests := []struct {
		name      string
		call      func(*Session, string) (int64, error)
		wantPath  func(string) string
		wantQuery string
		bucket    string
	}{
		{
			name: "guild member",
			call: func(s *Session, id string) (int64, error) {
				return s.GuildMemberPermissions("guild", id, WithHeader("X-Test", "permissions"))
			},
			wantPath: func(id string) string { return "/guilds/guild/permissions/member/" + id },
			bucket:   EndpointGuildMemberPermissions("guild", ""),
		},
		{
			name: "guild role",
			call: func(s *Session, id string) (int64, error) {
				return s.GuildRolePermissions("guild", id, WithHeader("X-Test", "permissions"))
			},
			wantPath: func(id string) string { return "/guilds/guild/permissions/role/" + id },
			bucket:   EndpointGuildRolePermissions("guild", ""),
		},
		{
			name: "channel member",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelMemberPermissions("channel", id, WithHeader("X-Test", "permissions"))
			},
			wantPath: func(id string) string { return "/channels/channel/permissions/member/" + id },
			bucket:   EndpointChannelMemberPermissions("channel", ""),
		},
		{
			name: "channel member nil options",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelMemberPermissionsWithOptions("channel", id, nil, WithHeader("X-Test", "permissions"))
			},
			wantPath: func(id string) string { return "/channels/channel/permissions/member/" + id },
			bucket:   EndpointChannelMemberPermissions("channel", ""),
		},
		{
			name: "channel member empty options",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelMemberPermissionsWithOptions("channel", id, &ChannelPermissionsOptions{}, WithHeader("X-Test", "permissions"))
			},
			wantPath: func(id string) string { return "/channels/channel/permissions/member/" + id },
			bucket:   EndpointChannelMemberPermissions("channel", ""),
		},
		{
			name: "channel member implicit false",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelMemberPermissionsWithOptions("channel", id, &ChannelPermissionsOptions{Implicit: &implicitFalse}, WithHeader("X-Test", "permissions"))
			},
			wantPath:  func(id string) string { return "/channels/channel/permissions/member/" + id },
			wantQuery: "implicit=false",
			bucket:    EndpointChannelMemberPermissions("channel", ""),
		},
		{
			name: "channel role",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelRolePermissions("channel", id, WithHeader("X-Test", "permissions"))
			},
			wantPath: func(id string) string { return "/channels/channel/permissions/role/" + id },
			bucket:   EndpointChannelRolePermissions("channel", ""),
		},
		{
			name: "channel role implicit true",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelRolePermissionsWithOptions("channel", id, &ChannelPermissionsOptions{Implicit: &implicitTrue}, WithHeader("X-Test", "permissions"))
			},
			wantPath:  func(id string) string { return "/channels/channel/permissions/role/" + id },
			wantQuery: "implicit=true",
			bucket:    EndpointChannelRolePermissions("channel", ""),
		},
		{
			name: "channel role implicit false",
			call: func(s *Session, id string) (int64, error) {
				return s.ChannelRolePermissionsWithOptions("channel", id, &ChannelPermissionsOptions{Implicit: &implicitFalse}, WithHeader("X-Test", "permissions"))
			},
			wantPath:  func(id string) string { return "/channels/channel/permissions/role/" + id },
			wantQuery: "implicit=false",
			bucket:    EndpointChannelRolePermissions("channel", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			currentID := ""
			session := computedPermissionsSession(t, func(r *http.Request) (int, string) {
				calls++
				if want := "/api/v" + APIVersion + tt.wantPath(currentID); r.URL.Path != want {
					t.Errorf("path = %s, want %s", r.URL.Path, want)
				}
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want %s", r.Method, http.MethodGet)
				}
				if got := r.Header.Get("X-Test"); got != "permissions" {
					t.Errorf("X-Test = %q, want permissions", got)
				}
				if r.URL.RawQuery != tt.wantQuery {
					t.Errorf("query = %q, want %q", r.URL.RawQuery, tt.wantQuery)
				}
				return http.StatusOK, permissionsBody
			})

			// Two targets share one bucket keyed on the major parameter.
			for _, id := range []string{"first", "second"} {
				currentID = id
				before := calls
				permissions, err := tt.call(session, id)
				if err != nil {
					t.Fatalf("call returned error: %v", err)
				}
				if calls != before+1 {
					t.Fatalf("requests = %d, want %d", calls, before+1)
				}
				if permissions != wantPermissions {
					t.Fatalf("permissions = %d, want %d", permissions, wantPermissions)
				}
			}

			session.Ratelimiter.Lock()
			defer session.Ratelimiter.Unlock()
			if _, ok := session.Ratelimiter.buckets[tt.bucket]; !ok || len(session.Ratelimiter.buckets) != 1 {
				keys := make([]string, 0, len(session.Ratelimiter.buckets))
				for key := range session.Ratelimiter.buckets {
					keys = append(keys, key)
				}
				t.Fatalf("buckets = %q, want only %q", keys, tt.bucket)
			}
		})
	}
}

func TestComputedPermissionsPaths(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) (int64, error)
		want string
	}{
		{"guild member", func(s *Session) (int64, error) { return s.GuildMemberPermissions("guild", "user") }, "/api/v" + APIVersion + "/guilds/guild/permissions/member/user"},
		{"guild role", func(s *Session) (int64, error) { return s.GuildRolePermissions("guild", "role") }, "/api/v" + APIVersion + "/guilds/guild/permissions/role/role"},
		{"channel member", func(s *Session) (int64, error) { return s.ChannelMemberPermissions("channel", "user") }, "/api/v" + APIVersion + "/channels/channel/permissions/member/user"},
		{"channel role", func(s *Session) (int64, error) { return s.ChannelRolePermissions("channel", "role") }, "/api/v" + APIVersion + "/channels/channel/permissions/role/role"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := computedPermissionsSession(t, func(r *http.Request) (int, string) {
				if r.URL.Path != tt.want {
					t.Errorf("path = %s, want %s", r.URL.Path, tt.want)
				}
				return http.StatusOK, `{"permissions":"0"}`
			})
			permissions, err := tt.call(session)
			if err != nil || permissions != 0 {
				t.Fatalf("permissions = %d, err = %v; want 0, nil", permissions, err)
			}
		})
	}
}

func TestComputedPermissionsRejectsMalformedResponses(t *testing.T) {
	for _, body := range []string{
		`null`,
		`{}`,
		`{"permissions":null}`,
		`{"permissions":"not-a-number"}`,
		`{"permissions":""}`,
		`{"permissions":8}`,
	} {
		t.Run(body, func(t *testing.T) {
			session := computedPermissionsSession(t, func(*http.Request) (int, string) {
				return http.StatusOK, body
			})
			for name, call := range map[string]func() (int64, error){
				"guild member":   func() (int64, error) { return session.GuildMemberPermissions("guild", "user") },
				"guild role":     func() (int64, error) { return session.GuildRolePermissions("guild", "role") },
				"channel member": func() (int64, error) { return session.ChannelMemberPermissions("channel", "user") },
				"channel role":   func() (int64, error) { return session.ChannelRolePermissions("channel", "role") },
			} {
				permissions, err := call()
				if !errors.Is(err, ErrJSONUnmarshal) {
					t.Fatalf("%s error = %v, want ErrJSONUnmarshal", name, err)
				}
				if permissions != 0 {
					t.Fatalf("%s permissions = %d, want 0", name, permissions)
				}
			}
		})
	}
}

func TestComputedPermissionsReturnsRESTErrors(t *testing.T) {
	session := computedPermissionsSession(t, func(*http.Request) (int, string) {
		return http.StatusForbidden, `{"code":50013,"message":"Missing Permissions"}`
	})

	permissions, err := session.ChannelMemberPermissions("channel", "user")
	var restErr *RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil || restErr.Message.Code != ErrCodeMissingPermissions {
		t.Fatalf("error = %v, want RESTError with code %d", err, ErrCodeMissingPermissions)
	}
	if permissions != 0 {
		t.Fatalf("permissions = %d, want 0", permissions)
	}
}
