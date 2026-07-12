package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
