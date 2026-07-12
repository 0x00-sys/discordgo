package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOAuth2CurrentUserInfo(t *testing.T) {
	session, err := New("Bearer access-token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/oauth2/userinfo"
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want Bearer access-token", got)
		}
		if got := r.Header.Get("X-Test"); got != "oauth2-userinfo" {
			t.Fatalf("X-Test = %q, want oauth2-userinfo", got)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"sub":"user",
				"preferred_username":"discord",
				"nickname":"Discord",
				"picture":"https://cdn.discordapp.com/avatar.png",
				"locale":"en-US",
				"email":"discord@example.com",
				"email_verified":true
			}`)),
			Request: r,
		}, nil
	})

	info, err := session.OAuth2CurrentUserInfo(WithHeader("X-Test", "oauth2-userinfo"))
	if err != nil {
		t.Fatalf("OAuth2CurrentUserInfo returned error: %v", err)
	}
	if info.Subject != "user" || info.PreferredUsername != "discord" || info.Picture != "https://cdn.discordapp.com/avatar.png" || info.Locale != "en-US" || !info.EmailVerified {
		t.Fatalf("userinfo = %#v", info)
	}
	if info.Nickname == nil || *info.Nickname != "Discord" {
		t.Fatalf("nickname = %#v", info.Nickname)
	}
	if info.Email == nil || *info.Email != "discord@example.com" {
		t.Fatalf("email = %#v", info.Email)
	}
}

func TestOAuth2CurrentUserInfoNullableClaims(t *testing.T) {
	session, err := New("Bearer access-token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"sub":"user","nickname":null,"email":null}`)),
			Request:    r,
		}, nil
	})

	info, err := session.OAuth2CurrentUserInfo()
	if err != nil {
		t.Fatalf("OAuth2CurrentUserInfo returned error: %v", err)
	}
	if info.Nickname != nil || info.Email != nil || info.EmailVerified {
		t.Fatalf("nullable claims = %#v", info)
	}
}

func TestOAuth2CurrentUserInfoResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantJSON bool
		wantREST bool
	}{
		{name: "malformed json", status: http.StatusOK, body: `{`, wantJSON: true},
		{name: "null response", status: http.StatusOK, body: `null`, wantJSON: true},
		{name: "missing subject", status: http.StatusOK, body: `{}`, wantJSON: true},
		{name: "rest error", status: http.StatusUnauthorized, body: `{"code":0,"message":"401: Unauthorized"}`, wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bearer access-token")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.status,
					Status:     http.StatusText(tt.status),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			info, err := session.OAuth2CurrentUserInfo(WithRestRetries(0))
			if info != nil {
				t.Fatalf("userinfo = %#v, want nil", info)
			}
			if tt.wantJSON && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %T %v, want ErrJSONUnmarshal", err, err)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
				}
			}
		})
	}
}
