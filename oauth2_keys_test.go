package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
