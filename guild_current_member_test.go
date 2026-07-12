package discordgo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func guildCurrentMemberString(value string) **string {
	pointer := &value
	return &pointer
}

func TestGuildCurrentMemberParams(t *testing.T) {
	var nullString *string

	tests := []struct {
		name   string
		params GuildCurrentMemberParams
		want   string
	}{
		{
			name: "omits unset fields",
			want: `{}`,
		},
		{
			name: "sets fields",
			params: GuildCurrentMemberParams{
				Nick:   guildCurrentMemberString("Bot"),
				Banner: guildCurrentMemberString("data:image/png;base64,banner"),
				Avatar: guildCurrentMemberString("data:image/png;base64,avatar"),
				Bio:    guildCurrentMemberString("Guild bot"),
			},
			want: `{"nick":"Bot","banner":"data:image/png;base64,banner","avatar":"data:image/png;base64,avatar","bio":"Guild bot"}`,
		},
		{
			name: "clears fields",
			params: GuildCurrentMemberParams{
				Nick:   &nullString,
				Banner: &nullString,
				Avatar: &nullString,
				Bio:    &nullString,
			},
			want: `{"nick":null,"banner":null,"avatar":null,"bio":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.params)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func TestGuildCurrentMemberEditRequestAndResponse(t *testing.T) {
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}
		if r.URL.Path != "/guilds/guild/members/@me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/members/@me")
		}
		if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "profile refresh" {
			t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "profile refresh")
		}

		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		want := map[string]string{
			"nick":   `"Bot"`,
			"banner": `"data:image/png;base64,banner"`,
			"avatar": `"data:image/png;base64,avatar"`,
			"bio":    `"Guild bot"`,
		}
		for field, expected := range want {
			if got := string(payload[field]); got != expected {
				t.Fatalf("%s = %s, want %s", field, got, expected)
			}
		}

		_, _ = w.Write([]byte(`{
			"user":{"id":"current","username":"Bot"},
			"nick":"Bot",
			"banner":"banner-hash",
			"avatar":"avatar-hash",
			"roles":[]
		}`))
	}))
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

	member, err := session.GuildCurrentMemberEdit("guild", &GuildCurrentMemberParams{
		Nick:   guildCurrentMemberString("Bot"),
		Banner: guildCurrentMemberString("data:image/png;base64,banner"),
		Avatar: guildCurrentMemberString("data:image/png;base64,avatar"),
		Bio:    guildCurrentMemberString("Guild bot"),
	}, WithAuditLogReason("profile refresh"))
	if err != nil {
		t.Fatalf("GuildCurrentMemberEdit returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if member == nil || member.User == nil || member.User.ID != "current" || member.GuildID != "guild" {
		t.Fatalf("member = %#v", member)
	}
	if member.Nick != "Bot" || member.Banner != "banner-hash" || member.Avatar != "avatar-hash" {
		t.Fatalf("member profile = %#v", member)
	}
}

func TestGuildCurrentMemberEditNullableRequests(t *testing.T) {
	var nullString *string
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}

		switch requests {
		case 1:
			if len(payload) != 0 {
				t.Fatalf("omitted payload = %#v, want empty object", payload)
			}
		case 2:
			for _, field := range []string{"nick", "banner", "avatar", "bio"} {
				if got := string(payload[field]); got != "null" {
					t.Fatalf("%s = %s, want null", field, got)
				}
			}
		default:
			t.Fatalf("unexpected request %d", requests)
		}

		_, _ = w.Write([]byte(`{"user":{"id":"current","username":"Bot"},"roles":[]}`))
	}))
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

	if _, err = session.GuildCurrentMemberEdit("guild", &GuildCurrentMemberParams{}); err != nil {
		t.Fatalf("GuildCurrentMemberEdit omitted fields returned error: %v", err)
	}
	if _, err = session.GuildCurrentMemberEdit("guild", &GuildCurrentMemberParams{
		Nick:   &nullString,
		Banner: &nullString,
		Avatar: &nullString,
		Bio:    &nullString,
	}); err != nil {
		t.Fatalf("GuildCurrentMemberEdit null fields returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestGuildCurrentMemberEditFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/guilds/api-error/members/@me":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":50035,"message":"Invalid Form Body"}`))
		case "/guilds/invalid-json/members/@me":
			_, _ = w.Write([]byte(`{`))
		case "/guilds/null-member/members/@me":
			_, _ = w.Write([]byte(`null`))
		case "/guilds/missing-user/members/@me":
			_, _ = w.Write([]byte(`{"roles":[]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
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

	for _, guildID := range []string{"api-error", "invalid-json", "null-member", "missing-user"} {
		t.Run(guildID, func(t *testing.T) {
			member, err := session.GuildCurrentMemberEdit(guildID, &GuildCurrentMemberParams{})
			if err == nil {
				t.Fatalf("GuildCurrentMemberEdit(%q) returned nil error", guildID)
			}
			if member != nil {
				t.Fatalf("GuildCurrentMemberEdit(%q) member = %#v, want nil", guildID, member)
			}
		})
	}
}
