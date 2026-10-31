package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOAuth2CurrentAuthorization(t *testing.T) {
	session, err := New("Bearer access-token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		wantPath := "/api/v" + APIVersion + "/oauth2/@me"
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
		}
		if got := r.Header.Get("X-Test"); got != "oauth2-authorization" {
			t.Fatalf("X-Test = %q, want %q", got, "oauth2-authorization")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`{
				"application": {
					"id": "application",
					"name": "Example",
					"icon": "icon",
					"description": "description",
					"bot_public": true,
					"bot_require_code_grant": false,
					"verify_key": "verify-key"
				},
				"scopes": ["identify", "guilds"],
				"expires": "2026-07-13T01:02:03.456Z",
				"user": {
					"id": "user",
					"username": "discord",
					"avatar": "avatar",
					"discriminator": "0",
					"global_name": "Discord",
					"public_flags": 131072
				}
			}`)),
			Request: r,
		}, nil
	})

	authorization, err := session.OAuth2CurrentAuthorization(WithHeader("X-Test", "oauth2-authorization"))
	if err != nil {
		t.Fatalf("OAuth2CurrentAuthorization returned error: %v", err)
	}
	if authorization == nil || authorization.Application == nil {
		t.Fatalf("authorization = %#v", authorization)
	}
	if authorization.Application.ID != "application" || authorization.Application.Name != "Example" || authorization.Application.Icon != "icon" || authorization.Application.Description != "description" || !authorization.Application.BotPublic || authorization.Application.BotRequireCodeGrant || authorization.Application.VerifyKey != "verify-key" {
		t.Fatalf("application = %#v", authorization.Application)
	}
	if len(authorization.Scopes) != 2 || authorization.Scopes[0] != "identify" || authorization.Scopes[1] != "guilds" {
		t.Fatalf("scopes = %#v", authorization.Scopes)
	}
	wantExpires := time.Date(2026, time.July, 13, 1, 2, 3, 456000000, time.UTC)
	if !authorization.Expires.Equal(wantExpires) {
		t.Fatalf("expires = %v, want %v", authorization.Expires, wantExpires)
	}
	if authorization.User == nil || authorization.User.ID != "user" || authorization.User.Username != "discord" || authorization.User.Avatar != "avatar" || authorization.User.Discriminator != "0" || authorization.User.GlobalName != "Discord" || authorization.User.PublicFlags != 131072 {
		t.Fatalf("user = %#v", authorization.User)
	}
}

func TestOAuth2CurrentAuthorizationWithoutUser(t *testing.T) {
	session, err := New("Bearer access-token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"application":{"id":"application","name":"Example"},"scopes":[],"expires":"2026-07-13T01:02:03Z"}`)),
			Request:    r,
		}, nil
	})

	authorization, err := session.OAuth2CurrentAuthorization()
	if err != nil {
		t.Fatalf("OAuth2CurrentAuthorization returned error: %v", err)
	}
	if authorization.User != nil {
		t.Fatalf("user = %#v, want nil", authorization.User)
	}
	if authorization.Scopes == nil {
		t.Fatal("scopes = nil, want empty slice")
	}
}

func TestOAuth2CurrentAuthorizationResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantJSON bool
		wantREST bool
	}{
		{name: "malformed json", status: http.StatusOK, body: `{`, wantJSON: true},
		{name: "invalid expiry", status: http.StatusOK, body: `{"application":{},"scopes":[],"expires":"invalid"}`, wantJSON: true},
		{name: "null response", status: http.StatusOK, body: `null`, wantJSON: true},
		{name: "missing application", status: http.StatusOK, body: `{"scopes":[],"expires":"2026-07-13T01:02:03Z"}`, wantJSON: true},
		{name: "missing scopes", status: http.StatusOK, body: `{"application":{},"expires":"2026-07-13T01:02:03Z"}`, wantJSON: true},
		{name: "missing expiry", status: http.StatusOK, body: `{"application":{},"scopes":[]}`, wantJSON: true},
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

			authorization, err := session.OAuth2CurrentAuthorization(WithRestRetries(0))
			if authorization != nil {
				t.Fatalf("authorization = %#v, want nil", authorization)
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

func TestOAuth2CurrentApplication(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/oauth2/applications/@me"
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bot test" {
			t.Fatalf("Authorization = %q, want Bot test", got)
		}
		if got := r.Header.Get("X-Test"); got != "oauth2-current-application" {
			t.Fatalf("X-Test = %q, want oauth2-current-application", got)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"id":"app","name":"Application","description":"description","bot_public":true,"flags_new":"4294967296"}`)),
			Request:    r,
		}, nil
	})

	application, err := session.OAuth2CurrentApplication(WithHeader("X-Test", "oauth2-current-application"))
	if err != nil {
		t.Fatalf("OAuth2CurrentApplication returned error: %v", err)
	}
	if application.ID != "app" || application.Name != "Application" || application.Description != "description" || !application.BotPublic || application.FlagsNew != "4294967296" {
		t.Fatalf("application = %#v", application)
	}
	if _, ok := session.Ratelimiter.buckets[EndpointOAuth2CurrentApplication]; !ok {
		t.Fatalf("bucket %q was not created", EndpointOAuth2CurrentApplication)
	}
}

func TestOAuth2CurrentApplicationResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantREST bool
	}{
		{name: "malformed json", status: http.StatusOK, body: `{`},
		{name: "null response", status: http.StatusOK, body: `null`},
		{name: "rest error", status: http.StatusUnauthorized, body: `{"code":0,"message":"Unauthorized"}`, wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
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

			application, err := session.OAuth2CurrentApplication(WithRestRetries(0))
			if application != nil {
				t.Fatalf("application = %#v, want nil", application)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
				}
				return
			}
			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %T %v, want ErrJSONUnmarshal", err, err)
			}
		})
	}
}

func TestOAuth2PublicKeys(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/oauth2/keys"
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Test"); got != "oauth2-keys" {
			t.Fatalf("X-Test = %q, want oauth2-keys", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"keys":[{"kty":"RSA","use":"sig","kid":"key-id","n":"modulus","e":"AQAB","alg":"RS256"}]}`)),
			Request:    r,
		}, nil
	})

	keys, err := session.OAuth2PublicKeys(WithHeader("X-Test", "oauth2-keys"))
	if err != nil {
		t.Fatalf("OAuth2PublicKeys returned error: %v", err)
	}
	if len(keys.Keys) != 1 {
		t.Fatalf("keys = %#v", keys)
	}
	key := keys.Keys[0]
	if key.KeyType != "RSA" || key.Use != "sig" || key.KeyID != "key-id" || key.Modulus != "modulus" || key.Exponent != "AQAB" || key.Algorithm != "RS256" {
		t.Fatalf("key = %#v", key)
	}
}

func TestOAuth2PublicKeysResponseErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantREST   bool
	}{
		{name: "malformed JSON", statusCode: http.StatusOK, body: `{`},
		{name: "null response", statusCode: http.StatusOK, body: `null`},
		{name: "missing keys", statusCode: http.StatusOK, body: `{}`},
		{name: "null key", statusCode: http.StatusOK, body: `{"keys":[null]}`},
		{name: "REST error", statusCode: http.StatusBadRequest, body: `{"code":50035,"message":"Invalid Form Body"}`, wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.statusCode,
					Status:     http.StatusText(tt.statusCode),
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			keys, err := session.OAuth2PublicKeys()
			if keys != nil {
				t.Fatalf("keys = %#v, want nil", keys)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.statusCode {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.statusCode)
				}
				return
			}
			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %T %v, want ErrJSONUnmarshal", err, err)
			}
		})
	}
}

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
