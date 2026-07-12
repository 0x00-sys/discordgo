package discordgo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestApplicationResourceEndpoints(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	requests := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		wantPath := "/api/v" + APIVersion + "/applications/app"
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Test"); got != "application-resource" {
			t.Fatalf("X-Test = %q, want application-resource", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bot test" {
			t.Fatalf("Authorization = %q, want Bot test", got)
		}

		body := `{"id":"app","name":"Application","description":"original","flags_new":"4294967296"}`
		switch r.Method {
		case http.MethodGet:
		case http.MethodPatch:
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			for key, want := range map[string]string{
				"description":        `"updated"`,
				"icon":               `null`,
				"cover_image":        `null`,
				"event_webhooks_url": `null`,
				"tags":               `[]`,
			} {
				if got := string(payload[key]); got != want {
					t.Fatalf("%s = %s, want %s", key, got, want)
				}
			}
			if _, ok := payload["custom_install_url"]; ok {
				t.Fatal("custom_install_url was included when unset")
			}
			body = `{"id":"app","name":"Application","description":"updated","flags_new":"4294967296"}`
		default:
			t.Fatalf("method = %q, want GET or PATCH", r.Method)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	application, err := session.ApplicationGet("app", WithHeader("X-Test", "application-resource"))
	if err != nil {
		t.Fatalf("ApplicationGet returned error: %v", err)
	}
	if application.ID != "app" || application.Name != "Application" || application.FlagsNew != "4294967296" {
		t.Fatalf("application = %#v", application)
	}

	description := "updated"
	empty := ""
	tags := []string{}
	application, err = session.ApplicationEdit("app", &ApplicationEditParams{
		Description:      &description,
		Icon:             &empty,
		CoverImage:       &empty,
		Tags:             &tags,
		EventWebhooksURL: &empty,
	}, WithHeader("X-Test", "application-resource"))
	if err != nil {
		t.Fatalf("ApplicationEdit returned error: %v", err)
	}
	if application.Description != description {
		t.Fatalf("description = %q, want %q", application.Description, description)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if _, ok := session.Ratelimiter.buckets[EndpointApplication("app")]; !ok {
		t.Fatalf("bucket %q was not created", EndpointApplication("app"))
	}
}

func TestApplicationResourceResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		status   int
		body     string
		wantREST bool
	}{
		{name: "get malformed json", method: http.MethodGet, status: http.StatusOK, body: `{`},
		{name: "get null response", method: http.MethodGet, status: http.StatusOK, body: `null`},
		{name: "get rest error", method: http.MethodGet, status: http.StatusUnauthorized, body: `{"code":0,"message":"Unauthorized"}`, wantREST: true},
		{name: "edit malformed json", method: http.MethodPatch, status: http.StatusOK, body: `{`},
		{name: "edit null response", method: http.MethodPatch, status: http.StatusOK, body: `null`},
		{name: "edit rest error", method: http.MethodPatch, status: http.StatusForbidden, body: `{"code":0,"message":"Forbidden"}`, wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != tt.method {
					t.Fatalf("method = %q, want %q", r.Method, tt.method)
				}
				return &http.Response{
					StatusCode: tt.status,
					Status:     http.StatusText(tt.status),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			var application *Application
			if tt.method == http.MethodGet {
				application, err = session.ApplicationGet("app", WithRestRetries(0))
			} else {
				application, err = session.ApplicationEdit("app", &ApplicationEditParams{}, WithRestRetries(0))
			}
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
