package discordgo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Example from Discord's Application Identity Profile resource documentation.
const applicationIdentityProfileExample = `{
  "username": "johndoe123",
  "metadata": null,
  "data": {
    "primary": {
      "season": "Season 3",
      "rank_name": "Silver",
      "rank_image": {"url": "https://example.com/assets/rank-images/silver.png"},
      "highest_rank": "Platinum",
      "highest_rank_image": {"url": "https://example.com/assets/rank-images/platinum.png"},
      "featured_played_character": "John Doe",
      "featured_played_character_image": {"url": "https://example.com/assets/character-images/john-doe.png"},
      "playtime_hours": 69.41,
      "total_wins": 57,
      "current_period_wins": 8,
      "total_games": 100,
      "current_period_games": 10,
      "total_kills": 253,
      "current_period_kills": 35,
      "total_assists": 478,
      "current_period_assists": 68,
      "total_deaths": 561,
      "current_period_deaths": 21
    },
    "dynamic": [
      {
        "type": 1,
        "name": "my_string",
        "value": "hello"
      },
      {
        "type": 2,
        "name": "my_number",
        "value": 123.45
      },
      {
        "type": 3,
        "name": "my_media",
        "value": {"url": "https://example.com/some-media.png"}
      }
    ]
  }
}`

func TestApplicationIdentityProfileJSON(t *testing.T) {
	var profile ApplicationIdentityProfile
	if err := json.Unmarshal([]byte(applicationIdentityProfileExample), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Username == nil || *profile.Username != "johndoe123" || profile.Data == nil || profile.Data.Primary == nil {
		t.Fatalf("profile = %#v", profile)
	}
	if profile.Data.Primary.PlaytimeHours == nil || *profile.Data.Primary.PlaytimeHours != 69.41 || len(profile.Data.Dynamic) != 3 {
		t.Fatalf("profile data = %#v", profile.Data)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var want, got interface{}
	if err := json.Unmarshal([]byte(applicationIdentityProfileExample), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip = %s", encoded)
	}
	if profile.Data.Dynamic[0].Type != ApplicationIdentityProfileDynamicFieldString || profile.Data.Dynamic[1].Type != ApplicationIdentityProfileDynamicFieldNumber || profile.Data.Dynamic[2].Type != ApplicationIdentityProfileDynamicFieldMedia {
		t.Fatal("dynamic field types do not match the documented values")
	}
	if err := json.Unmarshal([]byte(`{"username":null,"metadata":null,"data":null}`), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Username != nil || profile.Metadata != nil || profile.Data != nil {
		t.Fatalf("null profile = %#v", profile)
	}
}

func TestApplicationIdentityProfileParamsJSON(t *testing.T) {
	for _, test := range []struct{ name, input string }{
		{"omitted", `{}`},
		{"empty replacement", `{"data":{}}`},
		{"empty username", `{"username":""}`},
		{"zero stats", `{"data":{"primary":{"season":"","playtime_hours":0,"total_wins":0,"current_period_wins":0,"total_games":0,"current_period_games":0,"total_kills":0,"current_period_kills":0,"total_assists":0,"current_period_assists":0,"total_deaths":0,"current_period_deaths":0}}}`},
		{"dynamic values", `{"data":{"dynamic":[{"type":1,"name":"text","value":""},{"type":2,"name":"score","value":0},{"type":3,"name":"image","value":{"url":"https://example.com/image.png"}}]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var params ApplicationIdentityProfileParams
			if err := json.Unmarshal([]byte(test.input), &params); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			var got, want interface{}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.input), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("payload = %s, want %s", data, test.input)
			}
		})
	}
}

func TestApplicationIdentityRequests(t *testing.T) {
	const externalID = "player/one ?#%+"
	const provider = "CUSTOM/type"
	const profilePath = "/applications/app/users/user/identities/player%2Fone%20%3F%23%25+/profile"
	const externalPath = "/applications/app/application-identities/CUSTOM%2Ftype/player%2Fone%20%3F%23%25+"
	const deletePath = "/users/user/application-identities/app/CUSTOM%2Ftype/player%2Fone%20%3F%23%25+/delete"
	const identitiesJSON = `{"identities":[{"user_id":"user","provider_type":"CUSTOM/type","provider_id":"issuer","provider_issued_user_id":"player/one ?#%+"}]}`
	opts := []RequestOption{WithHeader("X-Test", "identities")}
	checkIdentities := func(t *testing.T, identities []*ApplicationIdentity) error {
		t.Helper()
		if len(identities) != 1 || identities[0].UserID != "user" || identities[0].ProviderType != provider || identities[0].ProviderID != "issuer" || identities[0].ProviderIssuedUserID != externalID {
			t.Fatalf("identities = %#v", identities)
		}
		return nil
	}
	for _, test := range []struct {
		name, method, path, query, body, response string
		status                                    int
		call                                      func(*testing.T, *Session) error
	}{
		{name: "read profile", method: "GET", path: profilePath, status: 200, response: applicationIdentityProfileExample, call: func(t *testing.T, s *Session) error {
			p, err := s.ApplicationIdentityProfile("app", "user", externalID, opts...)
			if err == nil && (p == nil || p.Username == nil || *p.Username != "johndoe123") {
				t.Fatalf("profile = %#v", p)
			}
			return err
		}},
		{name: "first write", method: "PATCH", path: profilePath, status: 201, body: `{}`, call: func(t *testing.T, s *Session) error {
			return s.ApplicationIdentityProfileUpdate("app", "user", externalID, nil, opts...)
		}},
		{name: "replace data", method: "PATCH", path: profilePath, status: 204, body: `{"data":{}}`, call: func(t *testing.T, s *Session) error {
			return s.ApplicationIdentityProfileUpdate("app", "user", externalID, &ApplicationIdentityProfileParams{Data: &ApplicationIdentityProfileData{}}, opts...)
		}},
		{name: "lookup user", method: "GET", path: "/users/user/application-identities/app", status: 200, response: identitiesJSON, call: func(t *testing.T, s *Session) error {
			ids, err := s.UserApplicationIdentities("user", "app", opts...)
			if err != nil {
				return err
			}
			return checkIdentities(t, ids)
		}},
		{name: "lookup external", method: "GET", path: externalPath, query: "provider_id=issuer%2F%3F%23%2B", status: 200, response: identitiesJSON, call: func(t *testing.T, s *Session) error {
			ids, err := s.ApplicationIdentities("app", provider, externalID, "issuer/?#+", opts...)
			if err != nil {
				return err
			}
			return checkIdentities(t, ids)
		}},
		{name: "lookup without provider ID", method: "GET", path: externalPath, status: 200, response: `{"identities":[]}`, call: func(t *testing.T, s *Session) error {
			ids, err := s.ApplicationIdentities("app", provider, externalID, "", opts...)
			if err == nil && len(ids) != 0 {
				t.Fatalf("identities = %#v", ids)
			}
			return err
		}},
		{name: "delete", method: "POST", path: deletePath, status: 204, body: `{"provider_id":"issuer/?#+"}`, call: func(t *testing.T, s *Session) error {
			return s.ApplicationIdentityDelete("user", "app", provider, externalID, "issuer/?#+", opts...)
		}},
		{name: "delete without provider ID", method: "POST", path: deletePath, status: 204, body: `{}`, call: func(t *testing.T, s *Session) error {
			return s.ApplicationIdentityDelete("user", "app", provider, externalID, "", opts...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != test.method || r.URL.EscapedPath() != "/api/v"+APIVersion+test.path || r.URL.RawQuery != test.query {
					t.Fatalf("request = %s %s; want %s %s?%s", r.Method, r.URL, test.method, test.path, test.query)
				}
				if r.Header.Get("Authorization") != "Bot test" || r.Header.Get("X-Test") != "identities" {
					t.Fatal("missing auth or request option")
				}
				var body []byte
				if r.Body != nil {
					body, err = io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
				}
				if string(body) != test.body {
					t.Fatalf("body = %s, want %s", body, test.body)
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.response)), Request: r}, nil
			})
			if err := test.call(t, s); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("requests = %d", calls)
			}
		})
	}
}

func TestApplicationIdentityResponses(t *testing.T) {
	for _, method := range []struct {
		name string
		read bool
		call func(*Session) error
	}{
		{"profile", true, func(s *Session) error { _, err := s.ApplicationIdentityProfile("app", "user", "external"); return err }},
		{"user identities", true, func(s *Session) error { _, err := s.UserApplicationIdentities("user", "app"); return err }},
		{"external identities", true, func(s *Session) error { _, err := s.ApplicationIdentities("app", "CUSTOM", "external", ""); return err }},
		{"update", false, func(s *Session) error { return s.ApplicationIdentityProfileUpdate("app", "user", "external", nil) }},
		{"delete", false, func(s *Session) error { return s.ApplicationIdentityDelete("user", "app", "CUSTOM", "external", "") }},
	} {
		t.Run(method.name, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				status int
				body   string
			}{
				{"bad request", 400, `{"code":50035,"message":"Invalid Form Body"}`},
				{"forbidden", 403, `{"code":50001,"message":"Missing Access"}`},
				{"malformed JSON", 200, `{`},
			} {
				if !method.read && test.status == 200 {
					continue
				}
				t.Run(test.name, func(t *testing.T) {
					s, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: r}, nil
					})
					err = method.call(s)
					if test.status == 200 {
						if !errors.Is(err, ErrJSONUnmarshal) {
							t.Fatalf("error = %v, want JSON error", err)
						}
					} else {
						var restErr *RESTError
						if !errors.As(err, &restErr) || restErr.Response.StatusCode != test.status {
							t.Fatalf("error = %v, want REST status %d", err, test.status)
						}
					}
				})
			}
		})
	}
}
