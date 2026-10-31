package discordgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

//////////////////////////////////////////////////////////////////////////////
/////////////////////////////////////////////////////////////// START OF TESTS

// TestChannelMessageSend tests the ChannelMessageSend() function. This should not return an error.
func TestChannelMessageSend(t *testing.T) {

	if envChannel == "" {
		t.Skip("Skipping, DG_CHANNEL not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.ChannelMessageSend(envChannel, "Running REST API Tests!")
	if err != nil {
		t.Errorf("ChannelMessageSend returned error: %+v", err)
	}
}

/*
// removed for now, only works on BOT accounts now
func TestUserAvatar(t *testing.T) {

	if dg == nil {
		t.Skip("Cannot TestUserAvatar, dg not set.")
	}

	u, err := dg.User("@me")
	if err != nil {
		t.Error("error fetching @me user,", err)
	}

	a, err := dg.UserAvatar(u.ID)
	if err != nil {
		if err.Error() == `HTTP 404 NOT FOUND, {"code": 0, "message": "404: Not Found"}` {
			t.Skip("Skipped, @me doesn't have an Avatar")
		}
		t.Errorf(err.Error())
	}

	if a == nil {
		t.Errorf("a == nil, should be image.Image")
	}
}
*/

/* Running this causes an error due to 2/hour rate limit on username changes
func TestUserUpdate(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot test logout, dg not set.")
	}

	u, err := dg.User("@me")
	if err != nil {
		t.Errorf(err.Error())
	}

	s, err := dg.UserUpdate(envEmail, envPassword, "testname", u.Avatar, "")
	if err != nil {
		t.Error(err.Error())
	}
	if s.Username != "testname" {
		t.Error("Username != testname")
	}
	s, err = dg.UserUpdate(envEmail, envPassword, u.Username, u.Avatar, "")
	if err != nil {
		t.Error(err.Error())
	}
	if s.Username != u.Username {
		t.Error("Username != " + u.Username)
	}
}
*/

func TestUserUpdateComplexNullableImages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}
		if r.URL.Path != "/users/@me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/users/@me")
		}

		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		switch requests {
		case 1:
			if got := string(payload["username"]); got != `"new-name"` {
				t.Fatalf("username = %s, want new-name", got)
			}
			if _, ok := payload["avatar"]; ok {
				t.Fatalf("legacy empty avatar was sent: %s", payload["avatar"])
			}
			if _, ok := payload["banner"]; ok {
				t.Fatalf("legacy empty banner was sent: %s", payload["banner"])
			}
		case 2:
			if got := string(payload["avatar"]); got != "null" {
				t.Fatalf("avatar = %s, want null", got)
			}
			if got := string(payload["banner"]); got != "null" {
				t.Fatalf("banner = %s, want null", got)
			}
		default:
			t.Fatalf("unexpected request %d", requests)
		}

		_, _ = w.Write([]byte(`{"id":"user","username":"new-name"}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointUsers := EndpointUsers
	EndpointUsers = server.URL + "/users/"
	t.Cleanup(func() {
		EndpointUsers = oldEndpointUsers
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	user, err := session.UserUpdate("new-name", "", "")
	if err != nil {
		t.Fatalf("UserUpdate returned error: %v", err)
	}
	if user == nil || user.ID != "user" || user.Username != "new-name" {
		t.Fatalf("user = %#v", user)
	}

	empty := ""
	user, err = session.UserUpdateComplex(&UserUpdateParams{Avatar: &empty, Banner: &empty})
	if err != nil {
		t.Fatalf("UserUpdateComplex returned error: %v", err)
	}
	if user == nil || user.ID != "user" {
		t.Fatalf("user = %#v", user)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestUserApplicationRoleConnectionDelete(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodDelete)
		}
		if r.URL.Path != "/users/@me/applications/app/role-connection" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/users/@me/applications/app/role-connection")
		}
		if got := r.Header.Get("X-Test-Request-Option"); got != "present" {
			t.Fatalf("X-Test-Request-Option = %q, want present", got)
		}

		if requests == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, `{"message":"failure"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	oldEndpointUsers := EndpointUsers
	EndpointUsers = server.URL + "/users/"
	t.Cleanup(func() {
		EndpointUsers = oldEndpointUsers
	})

	session, err := New("Bearer test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	requestOption := WithHeader("X-Test-Request-Option", "present")
	if err = session.UserApplicationRoleConnectionDelete("app", requestOption, WithRestRetries(0)); err != nil {
		t.Fatalf("UserApplicationRoleConnectionDelete returned error: %v", err)
	}
	if err = session.UserApplicationRoleConnectionDelete("app", requestOption, WithRestRetries(0)); err == nil {
		t.Fatal("UserApplicationRoleConnectionDelete returned nil for REST error")
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestUserApplicationEntitlementsBuildsQueryAndDecodesResponse(t *testing.T) {
	trueValue := true
	falseValue := false
	tests := []struct {
		name      string
		options   *UserApplicationEntitlementFilterOptions
		wantQuery string
	}{
		{
			name: "all filters",
			options: &UserApplicationEntitlementFilterOptions{
				SKUIDs:          []string{"sku-one", "sku-two"},
				ExcludeConsumed: &trueValue,
			},
			wantQuery: "exclude_consumed=true&sku_ids=sku-one&sku_ids=sku-two",
		},
		{
			name:      "explicit false",
			options:   &UserApplicationEntitlementFilterOptions{ExcludeConsumed: &falseValue},
			wantQuery: "exclude_consumed=false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bearer test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
				}
				wantPath := "/api/v" + APIVersion + "/users/@me/applications/application/entitlements"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
				}
				if r.URL.RawQuery != tt.wantQuery {
					t.Fatalf("RawQuery = %q, want %q", r.URL.RawQuery, tt.wantQuery)
				}
				if tt.name == "all filters" {
					wantSKUIDs := []string{"sku-one", "sku-two"}
					gotSKUIDs := r.URL.Query()["sku_ids"]
					if len(gotSKUIDs) != len(wantSKUIDs) || gotSKUIDs[0] != wantSKUIDs[0] || gotSKUIDs[1] != wantSKUIDs[1] {
						t.Fatalf("sku_ids = %#v, want repeated values %#v", gotSKUIDs, wantSKUIDs)
					}
				}
				if got := r.Header.Get("X-Test"); got != "entitlements" {
					t.Fatalf("X-Test = %q, want %q", got, "entitlements")
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`[{
						"id":"entitlement",
						"sku_id":"sku-one",
						"application_id":"application",
						"user_id":"user",
						"guild_id":null,
						"deleted":false,
						"starts_at":"2026-07-13T12:00:00Z",
						"ends_at":null,
						"type":10,
						"fulfilled_at":"2026-07-13T12:01:00Z",
						"fulfillment_status":3,
						"consumed":false,
						"gifter_user_id":"gifter",
						"parent_id":"parent"
					}]`)),
					Request: r,
				}, nil
			})

			entitlements, err := session.UserApplicationEntitlements(
				"application",
				tt.options,
				WithHeader("X-Test", "entitlements"),
			)
			if err != nil {
				t.Fatalf("UserApplicationEntitlements returned error: %v", err)
			}
			if len(entitlements) != 1 {
				t.Fatalf("len(entitlements) = %d, want 1", len(entitlements))
			}
			entitlement := entitlements[0]
			if entitlement.ID != "entitlement" || entitlement.SKUID != "sku-one" || entitlement.ApplicationID != "application" || entitlement.UserID != "user" {
				t.Fatalf("entitlement identity = %#v", entitlement)
			}
			if entitlement.Type != EntitlementTypeQuestReward || entitlement.Deleted || entitlement.GuildID != "" || entitlement.EndsAt != nil {
				t.Fatalf("entitlement state = %#v", entitlement)
			}
			if entitlement.StartsAt == nil || !entitlement.StartsAt.Equal(time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("starts_at = %v", entitlement.StartsAt)
			}
			if entitlement.FulfilledAt == nil || !entitlement.FulfilledAt.Equal(time.Date(2026, 7, 13, 12, 1, 0, 0, time.UTC)) {
				t.Fatalf("fulfilled_at = %v", entitlement.FulfilledAt)
			}
			if entitlement.FulfillmentStatus == nil || *entitlement.FulfillmentStatus != EntitlementFulfillmentStatusFulfilled {
				t.Fatalf("fulfillment_status = %v", entitlement.FulfillmentStatus)
			}
			if entitlement.Consumed == nil || *entitlement.Consumed || entitlement.GifterUserID != "gifter" || entitlement.ParentID != "parent" {
				t.Fatalf("entitlement purchase fields = %#v", entitlement)
			}
		})
	}
}

func TestUserApplicationEntitlementsDefaultsOmitQuery(t *testing.T) {
	tests := []struct {
		name    string
		options *UserApplicationEntitlementFilterOptions
	}{
		{name: "nil options"},
		{name: "zero options", options: &UserApplicationEntitlementFilterOptions{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bearer test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.RawQuery != "" {
					t.Fatalf("RawQuery = %q, want empty", r.URL.RawQuery)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`[]`)),
					Request:    r,
				}, nil
			})

			entitlements, err := session.UserApplicationEntitlements("application", tt.options)
			if err != nil {
				t.Fatalf("UserApplicationEntitlements returned error: %v", err)
			}
			if entitlements == nil || len(entitlements) != 0 {
				t.Fatalf("entitlements = %#v, want non-nil empty slice", entitlements)
			}
		})
	}
}

func TestUserApplicationEntitlementsResponseErrors(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantREST      bool
		wantUnmarshal bool
	}{
		{name: "REST error", status: http.StatusForbidden, body: `{"code":50001,"message":"Missing Access"}`, wantREST: true},
		{name: "malformed response", status: http.StatusOK, body: `{`, wantUnmarshal: true},
		{name: "null response", status: http.StatusOK, body: `null`, wantUnmarshal: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bearer test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.status,
					Status:     strconv.Itoa(tt.status) + " " + http.StatusText(tt.status),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			entitlements, err := session.UserApplicationEntitlements("application", nil)
			if entitlements != nil {
				t.Fatalf("entitlements = %#v, want nil", entitlements)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
				}
			}
			if tt.wantUnmarshal && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %T %v, want ErrJSONUnmarshal", err, err)
			}
		})
	}
}

//func (s *Session) UserChannelCreate(recipientID string) (st *Channel, err error) {

func TestUserChannelCreate(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot TestUserChannelCreate, dg not set.")
	}

	if envAdmin == "" {
		t.Skip("Skipped, DG_ADMIN not set.")
	}

	_, err := dg.UserChannelCreate(envAdmin)
	if err != nil {
		t.Error(err)
	}

	// TODO make sure the channel was added
}

func TestUserGuilds(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot TestUserGuilds, dg not set.")
	}

	_, err := dg.UserGuilds(10, "", "", false)
	if err != nil {
		t.Error(err)
	}
}

func TestGuildBulkBan(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/guilds/guild/bulk-ban" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/bulk-ban")
		}
		if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "raid%20cleanup" {
			t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "raid%20cleanup")
		}

		var data GuildBulkBanParams
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if strings.Join(data.UserIDs, ",") != "user-1,user-2" {
			t.Fatalf("user_ids = %#v, want user-1 and user-2", data.UserIDs)
		}
		if data.DeleteMessageSeconds != 3600 {
			t.Fatalf("delete_message_seconds = %d, want 3600", data.DeleteMessageSeconds)
		}

		_, _ = w.Write([]byte(`{"banned_users":["user-1"],"failed_users":["user-2"]}`))
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

	result, err := session.GuildBulkBan("guild", &GuildBulkBanParams{
		UserIDs:              []string{"user-1", "user-2"},
		DeleteMessageSeconds: 3600,
	}, WithAuditLogReason("raid cleanup"))
	if err != nil {
		t.Fatalf("GuildBulkBan returned error: %v", err)
	}
	if result == nil || strings.Join(result.BannedUsers, ",") != "user-1" || strings.Join(result.FailedUsers, ",") != "user-2" {
		t.Fatalf("result = %#v, want user-1 banned and user-2 failed", result)
	}

	if _, err := session.GuildBulkBan("guild", nil); err == nil {
		t.Fatal("GuildBulkBan returned nil error for nil data")
	}
	if _, err := session.GuildBulkBan("guild", &GuildBulkBanParams{}); err == nil {
		t.Fatal("GuildBulkBan returned nil error for empty user IDs")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestStickerRetrieval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}

		switch r.URL.Path {
		case "/stickers/sticker":
			_, _ = w.Write([]byte(`{"id":"sticker","name":"Wave","type":1,"format_type":3}`))
		case "/sticker-packs":
			_, _ = w.Write([]byte(`{"sticker_packs":[{"id":"pack","name":"Wumpus Beyond","sku_id":"sku","stickers":[]}]}`))
		case "/sticker-packs/pack":
			_, _ = w.Write([]byte(`{"id":"pack","name":"Wumpus Beyond","sku_id":"sku","stickers":[{"id":"sticker","name":"Wave","type":1,"format_type":3}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	oldEndpointStickers := EndpointStickers
	oldEndpointStickerPacks := EndpointStickerPacks
	EndpointStickers = server.URL + "/stickers/"
	EndpointStickerPacks = server.URL + "/sticker-packs"
	t.Cleanup(func() {
		EndpointStickers = oldEndpointStickers
		EndpointStickerPacks = oldEndpointStickerPacks
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	sticker, err := session.Sticker("sticker")
	if err != nil {
		t.Fatalf("Sticker returned error: %v", err)
	}
	if sticker.ID != "sticker" || sticker.Name != "Wave" || sticker.FormatType != StickerFormatTypeLottie {
		t.Fatalf("sticker = %#v", sticker)
	}

	packs, err := session.StickerPacks()
	if err != nil {
		t.Fatalf("StickerPacks returned error: %v", err)
	}
	if len(packs) != 1 || packs[0].ID != "pack" || packs[0].SKUID != "sku" {
		t.Fatalf("packs = %#v", packs)
	}

	pack, err := session.StickerPack("pack")
	if err != nil {
		t.Fatalf("StickerPack returned error: %v", err)
	}
	if pack.ID != "pack" || len(pack.Stickers) != 1 || pack.Stickers[0].ID != "sticker" {
		t.Fatalf("pack = %#v", pack)
	}
}

func TestStickerRetrievalInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{`))
	}))
	t.Cleanup(server.Close)

	oldEndpointStickers := EndpointStickers
	oldEndpointStickerPacks := EndpointStickerPacks
	EndpointStickers = server.URL + "/stickers/"
	EndpointStickerPacks = server.URL + "/sticker-packs"
	t.Cleanup(func() {
		EndpointStickers = oldEndpointStickers
		EndpointStickerPacks = oldEndpointStickerPacks
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "sticker", call: func() error { _, err := session.Sticker("sticker"); return err }},
		{name: "sticker packs", call: func() error { _, err := session.StickerPacks(); return err }},
		{name: "sticker pack", call: func() error { _, err := session.StickerPack("pack"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("returned nil error for invalid JSON")
			}
		})
	}
}

func TestGuildStickerEndpoints(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /guilds/guild/stickers":
			_, _ = w.Write([]byte(`[{"id":"sticker","name":"Wave","type":2,"format_type":1}]`))
		case http.MethodGet + " /guilds/guild/stickers/sticker":
			_, _ = w.Write([]byte(`{"id":"sticker","name":"Wave","type":2,"format_type":1}`))
		case http.MethodPost + " /guilds/guild/stickers":
			if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "new%20sticker" {
				t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "new%20sticker")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm returned error: %v", err)
			}
			if r.FormValue("name") != "Wave" || r.FormValue("description") != "A wave" || r.FormValue("tags") != "wave, hello" {
				t.Fatalf("form = %#v", r.MultipartForm.Value)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("FormFile returned error: %v", err)
			}
			defer file.Close()
			contents, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("ReadAll returned error: %v", err)
			}
			if header.Filename != "wave.png" || header.Header.Get("Content-Type") != "image/png" || string(contents) != "png" {
				t.Fatalf("file = %q/%q/%q", header.Filename, header.Header.Get("Content-Type"), contents)
			}
			_, _ = w.Write([]byte(`{"id":"created","name":"Wave","type":2,"format_type":1}`))
		case http.MethodPatch + " /guilds/guild/stickers/sticker":
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if payload["name"] != "Waving" || payload["tags"] != "wave" {
				t.Fatalf("payload = %#v", payload)
			}
			if description, ok := payload["description"]; !ok || description != nil {
				t.Fatalf("description = %#v, present = %t", description, ok)
			}
			_, _ = w.Write([]byte(`{"id":"sticker","name":"Waving","type":2,"format_type":1}`))
		case http.MethodDelete + " /guilds/guild/stickers/sticker":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	oldEndpointGuilds := EndpointGuilds
	EndpointGuilds = server.URL + "/guilds/"
	t.Cleanup(func() { EndpointGuilds = oldEndpointGuilds })

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	stickers, err := session.GuildStickers("guild")
	if err != nil || len(stickers) != 1 || stickers[0].ID != "sticker" {
		t.Fatalf("GuildStickers = %#v, %v", stickers, err)
	}
	sticker, err := session.GuildSticker("guild", "sticker")
	if err != nil || sticker.ID != "sticker" {
		t.Fatalf("GuildSticker = %#v, %v", sticker, err)
	}
	created, err := session.GuildStickerCreate("guild", &GuildStickerCreateParams{
		Name:        "Wave",
		Description: "A wave",
		Tags:        "wave, hello",
		File:        &File{Name: "wave.png", ContentType: "image/png", Reader: strings.NewReader("png")},
	}, WithAuditLogReason("new sticker"))
	if err != nil || created.ID != "created" {
		t.Fatalf("GuildStickerCreate = %#v, %v", created, err)
	}
	edited, err := session.GuildStickerEdit("guild", "sticker", &GuildStickerEditParams{
		Name:            "Waving",
		Tags:            "wave",
		DescriptionNull: true,
	})
	if err != nil || edited.Name != "Waving" {
		t.Fatalf("GuildStickerEdit = %#v, %v", edited, err)
	}
	if err := session.GuildStickerDelete("guild", "sticker"); err != nil {
		t.Fatalf("GuildStickerDelete returned error: %v", err)
	}
	if requests != 5 {
		t.Fatalf("requests = %d, want 5", requests)
	}
}

func TestGuildStickerInvalidInputs(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, err := session.GuildStickerCreate("guild", nil); err == nil {
		t.Fatal("GuildStickerCreate returned nil error for nil data")
	}
	if _, err := session.GuildStickerCreate("guild", &GuildStickerCreateParams{}); err == nil {
		t.Fatal("GuildStickerCreate returned nil error for nil file")
	}
	if _, err := session.GuildStickerCreate("guild", &GuildStickerCreateParams{File: &File{Name: "sticker.png"}}); err == nil {
		t.Fatal("GuildStickerCreate returned nil error for nil file reader")
	}
	if _, err := session.GuildStickerEdit("guild", "sticker", nil); err == nil {
		t.Fatal("GuildStickerEdit returned nil error for nil data")
	}
}

func TestGuildBulkBanResponseValidation(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		valid   bool
	}{
		{name: "empty result arrays", payload: `{"banned_users":[],"failed_users":[]}`, valid: true},
		{name: "malformed JSON", payload: `{`},
		{name: "null result", payload: `null`},
		{name: "missing banned users", payload: `{"failed_users":[]}`},
		{name: "missing failed users", payload: `{"banned_users":[]}`},
		{name: "null banned users", payload: `{"banned_users":null,"failed_users":[]}`},
		{name: "null failed users", payload: `{"banned_users":[],"failed_users":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.payload)),
					Request:    r,
				}, nil
			})

			result, err := session.GuildBulkBan("guild", &GuildBulkBanParams{UserIDs: []string{"user"}})
			if tt.valid {
				if err != nil {
					t.Fatalf("GuildBulkBan returned error: %v", err)
				}
				if result == nil || result.BannedUsers == nil || result.FailedUsers == nil {
					t.Fatalf("GuildBulkBan result = %#v, want non-nil empty arrays", result)
				}
				return
			}
			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("GuildBulkBan error = %v, want ErrJSONUnmarshal", err)
			}
			if result != nil {
				t.Fatalf("GuildBulkBan result = %#v, want nil", result)
			}
		})
	}
}

func TestGuildMembersSearchSetsGuildID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/guild/members/search" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/members/search")
		}
		if query := r.URL.Query().Get("query"); query != "user" {
			t.Fatalf("query = %q, want %q", query, "user")
		}
		if limit := r.URL.Query().Get("limit"); limit != "2" {
			t.Fatalf("limit = %q, want %q", limit, "2")
		}
		_, _ = w.Write([]byte(`[{"user":{"id":"user","username":"user","discriminator":"0"},"avatar":"guild-avatar"}]`))
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

	members, err := session.GuildMembersSearch("guild", "user", 2)
	if err != nil {
		t.Fatalf("GuildMembersSearch returned error: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("len(members) = %d, want 1", len(members))
	}
	if members[0].GuildID != "guild" {
		t.Fatalf("GuildID = %q, want %q", members[0].GuildID, "guild")
	}
	if got, want := members[0].AvatarURL(""), EndpointGuildMemberAvatar("guild", "user", "guild-avatar"); got != want {
		t.Fatalf("AvatarURL = %q, want %q", got, want)
	}
}

func TestGuildMemberMethodsRejectMalformedResponses(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		payload string
	}{
		{name: "get null member", method: "get", payload: `null`},
		{name: "get malformed JSON", method: "get", payload: `{`},
		{name: "get missing user", method: "get", payload: `{}`},
		{name: "list null response", method: "list", payload: `null`},
		{name: "list partial JSON", method: "list", payload: `[{"user":{"id":"user"}},`},
		{name: "list null member", method: "list", payload: `[null]`},
		{name: "list missing user", method: "list", payload: `[{}]`},
		{name: "search null response", method: "search", payload: `null`},
		{name: "search partial JSON", method: "search", payload: `[{"user":{"id":"user"}},`},
		{name: "search null member", method: "search", payload: `[null]`},
		{name: "search missing user", method: "search", payload: `[{}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.payload)),
					Request:    r,
				}, nil
			})

			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("guild member method panicked: %v", recovered)
				}
			}()

			var resultNonNil bool
			switch tt.method {
			case "get":
				member, callErr := session.GuildMember("guild", "user")
				resultNonNil, err = member != nil, callErr
			case "list":
				members, callErr := session.GuildMembers("guild", "", 1000)
				resultNonNil, err = members != nil, callErr
			case "search":
				members, callErr := session.GuildMembersSearch("guild", "user", 1000)
				resultNonNil, err = members != nil, callErr
			}

			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("guild member method error = %v, want ErrJSONUnmarshal", err)
			}
			if resultNonNil {
				t.Fatal("guild member method returned malformed data with an error")
			}
		})
	}
}

func TestGuildMemberMethodsAcceptValidResponses(t *testing.T) {
	getMember := func(session *Session) ([]*Member, error) {
		member, err := session.GuildMember("guild", "user")
		if member == nil {
			return nil, err
		}
		return []*Member{member}, err
	}
	listMembers := func(session *Session) ([]*Member, error) {
		return session.GuildMembers("guild", "", 1000)
	}
	searchMembers := func(session *Session) ([]*Member, error) {
		return session.GuildMembersSearch("guild", "user", 1000)
	}

	tests := []struct {
		name    string
		payload string
		call    func(*Session) ([]*Member, error)
		want    int
	}{
		{name: "get member", payload: `{"user":{"id":"user"}}`, call: getMember, want: 1},
		{name: "list members", payload: `[{"user":{"id":"user"}}]`, call: listMembers, want: 1},
		{name: "search members", payload: `[{"user":{"id":"user"}}]`, call: searchMembers, want: 1},
		{name: "empty member list", payload: `[]`, call: listMembers},
		{name: "empty member search", payload: `[]`, call: searchMembers},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.payload)),
					Request:    r,
				}, nil
			})

			members, err := tt.call(session)
			if err != nil {
				t.Fatalf("guild member method returned error: %v", err)
			}
			if members == nil {
				t.Fatal("guild member method returned a nil result")
			}
			if len(members) != tt.want {
				t.Fatalf("len(members) = %d, want %d", len(members), tt.want)
			}
			if tt.want == 1 && (members[0].GuildID != "guild" || members[0].User.ID != "user") {
				t.Fatalf("member = %#v, want guild and user IDs populated", members[0])
			}
		})
	}
}

func TestGuildMessagesSearchBuildsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/messages/search" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/messages/search")
		}

		query := r.URL.Query()
		for key, want := range map[string]string{
			"limit":            "25",
			"offset":           "5",
			"max_id":           "200",
			"min_id":           "100",
			"slop":             "2",
			"content":          "hello",
			"mention_everyone": "true",
			"pinned":           "false",
			"sort_by":          "timestamp",
			"sort_order":       "desc",
			"include_nsfw":     "true",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
		for key, want := range map[string]string{
			"channel_id":            "channel-1,channel-2",
			"author_type":           "user,webhook",
			"author_id":             "author",
			"mentions":              "mentioned-user",
			"mentions_role_id":      "mentioned-role",
			"replied_to_user_id":    "reply-user",
			"replied_to_message_id": "reply-message",
			"has":                   "file,embed",
			"embed_type":            "rich",
			"embed_provider":        "YouTube",
			"link_hostname":         "example.com",
			"attachment_filename":   "report.pdf",
			"attachment_extension":  "pdf",
		} {
			if got := strings.Join(query[key], ","); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}

		_, _ = w.Write([]byte(`{"doing_deep_historical_index":false,"total_results":1,"messages":[[{"id":"message","channel_id":"channel-1"}]],"threads":[{"id":"thread"}],"members":[{"id":"thread-member"}]}`))
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

	trueValue := true
	falseValue := false
	result, err := session.GuildMessagesSearch("guild", &GuildMessagesSearchOptions{
		Limit:                25,
		Offset:               5,
		MaxID:                "200",
		MinID:                "100",
		Slop:                 2,
		Content:              "hello",
		ChannelIDs:           []string{"channel-1", "channel-2"},
		AuthorTypes:          []string{"user", "webhook"},
		AuthorIDs:            []string{"author"},
		Mentions:             []string{"mentioned-user"},
		MentionRoleIDs:       []string{"mentioned-role"},
		MentionEveryone:      &trueValue,
		RepliedToUserIDs:     []string{"reply-user"},
		RepliedToMessageIDs:  []string{"reply-message"},
		Pinned:               &falseValue,
		Has:                  []string{"file", "embed"},
		EmbedTypes:           []string{"rich"},
		EmbedProviders:       []string{"YouTube"},
		LinkHostnames:        []string{"example.com"},
		AttachmentFilenames:  []string{"report.pdf"},
		AttachmentExtensions: []string{"pdf"},
		SortBy:               "timestamp",
		SortOrder:            "desc",
		IncludeNSFW:          &trueValue,
	})
	if err != nil {
		t.Fatalf("GuildMessagesSearch returned error: %v", err)
	}
	if result.TotalResults != 1 || len(result.Messages) != 1 || len(result.Messages[0]) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestThreadsSearchBuildsQueryAndDecodesResponse(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		wantPath := "/api/v" + APIVersion + "/channels/channel/threads/search"
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Test"); got != "thread-search" {
			t.Fatalf("X-Test = %q, want %q", got, "thread-search")
		}

		query := r.URL.Query()
		for key, want := range map[string]string{
			"name":        "release notes",
			"slop":        "0",
			"min_id":      "100",
			"max_id":      "200",
			"tag_setting": "match_all",
			"archived":    "false",
			"sort_by":     "last_message_time",
			"sort_order":  "desc",
			"limit":       "25",
			"offset":      "50",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
		if got, want := query["tag"], []string{"tag-one", "tag-two"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("tag = %#v, want %#v", got, want)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"threads":[{"id":"thread","name":"release notes","type":11}],
				"members":[{"id":"thread","user_id":"user","join_timestamp":"2026-07-13T12:00:00Z","flags":0}],
				"has_more":true,
				"first_messages":[{"id":"message","channel_id":"thread","content":"first"}],
				"total_results":2
			}`)),
			Request: r,
		}, nil
	})

	archived := false
	slop := 0
	result, err := session.ThreadsSearch("channel", &ThreadSearchOptions{
		Name:       "release notes",
		Slop:       &slop,
		MinID:      "100",
		MaxID:      "200",
		Tags:       []string{"tag-one", "tag-two"},
		TagSetting: ThreadSearchTagSettingMatchAll,
		Archived:   &archived,
		SortBy:     ThreadSearchSortByLastMessageTime,
		SortOrder:  ThreadSearchSortOrderDescending,
		Limit:      25,
		Offset:     50,
	}, WithHeader("X-Test", "thread-search"))
	if err != nil {
		t.Fatalf("ThreadsSearch returned error: %v", err)
	}
	if result.TotalResults != 2 || !result.HasMore {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Threads) != 1 || result.Threads[0].ID != "thread" || result.Threads[0].Name != "release notes" {
		t.Fatalf("threads = %#v", result.Threads)
	}
	if len(result.Members) != 1 || result.Members[0].ID != "thread" || result.Members[0].UserID != "user" {
		t.Fatalf("members = %#v", result.Members)
	}
	if len(result.FirstMessages) != 1 || result.FirstMessages[0].ID != "message" || result.FirstMessages[0].Content != "first" {
		t.Fatalf("first_messages = %#v", result.FirstMessages)
	}
}

func TestThreadsSearchDefaultsOmitQuery(t *testing.T) {
	tests := []struct {
		name string
		opts *ThreadSearchOptions
	}{
		{name: "nil options"},
		{name: "zero options", opts: &ThreadSearchOptions{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.RawQuery != "" {
					t.Fatalf("RawQuery = %q, want empty", r.URL.RawQuery)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`{"threads":[],"members":[],"has_more":false,"total_results":0}`)),
					Request:    r,
				}, nil
			})

			result, err := session.ThreadsSearch("channel", tt.opts)
			if err != nil {
				t.Fatalf("ThreadsSearch returned error: %v", err)
			}
			if result == nil || result.TotalResults != 0 || result.FirstMessages != nil {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestSearchResponseErrors(t *testing.T) {
	searches := []struct {
		name string
		call func(*Session) (interface{}, error)
	}{
		{"threads", func(s *Session) (interface{}, error) {
			result, err := s.ThreadsSearch("channel", nil)
			if result == nil {
				return nil, err
			}
			return result, err
		}},
		{"guild messages", func(s *Session) (interface{}, error) {
			result, err := s.GuildMessagesSearch("guild", nil)
			if result == nil {
				return nil, err
			}
			return result, err
		}},
	}
	tests := []struct {
		name          string
		status        int
		body          string
		wantIndex     bool
		wantREST      bool
		wantUnmarshal bool
	}{
		{
			name:      "index not ready",
			status:    http.StatusAccepted,
			body:      `{"message":"Index not yet available. Try again later","code":110000,"documents_indexed":7,"retry_after":2}`,
			wantIndex: true,
		},
		{name: "malformed index response", status: http.StatusAccepted, body: `{`, wantREST: true},
		{name: "null index response", status: http.StatusAccepted, body: `null`, wantREST: true},
		{name: "http error", status: http.StatusBadRequest, body: `{"code":50035,"message":"Invalid Form Body"}`, wantREST: true},
		{name: "malformed success response", status: http.StatusOK, body: `{`, wantUnmarshal: true},
		{name: "null success response", status: http.StatusOK, body: `null`, wantUnmarshal: true},
	}

	for _, search := range searches {
		for _, tt := range tests {
			t.Run(search.name+"/"+tt.name, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatalf("New returned error: %v", err)
				}
				session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: tt.status,
						Status:     strconv.Itoa(tt.status) + " " + http.StatusText(tt.status),
						Body:       io.NopCloser(strings.NewReader(tt.body)),
						Request:    r,
					}, nil
				})

				result, err := search.call(session)
				if result != nil {
					t.Fatalf("result = %#v, want nil", result)
				}

				if tt.wantIndex {
					var indexErr *SearchIndexNotReadyError
					if !errors.As(err, &indexErr) {
						t.Fatalf("error = %T %v, want *SearchIndexNotReadyError", err, err)
					}
					var legacyErr *ThreadSearchIndexNotReadyError
					if !errors.As(err, &legacyErr) || legacyErr != indexErr {
						t.Fatalf("error = %T %v, want ThreadSearchIndexNotReadyError alias to match", err, err)
					}
					if indexErr.Message != "Index not yet available. Try again later" || indexErr.Code != ErrCodeIndexNotYetAvailable || indexErr.DocumentsIndexed != 7 || indexErr.RetryAfter != 2 {
						t.Fatalf("index error = %#v", indexErr)
					}
					var restErr *RESTError
					if !errors.As(err, &restErr) || restErr.Response.StatusCode != http.StatusAccepted {
						t.Fatalf("error = %T %v, want wrapped 202 RESTError", err, err)
					}
					return
				}
				if tt.wantREST {
					var restErr *RESTError
					if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
						t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
					}
					var indexErr *SearchIndexNotReadyError
					if errors.As(err, &indexErr) {
						t.Fatalf("error = %T %v, do not want typed index error", err, err)
					}
					return
				}
				if tt.wantUnmarshal {
					if !errors.Is(err, ErrJSONUnmarshal) {
						t.Fatalf("error = %T %v, want ErrJSONUnmarshal", err, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("search returned error: %v", err)
				}
			})
		}
	}
}

func TestGuildWelcomeScreen(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/guilds/guild/welcome-screen" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/welcome-screen")
		}

		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"description":"Welcome","welcome_channels":[{"channel_id":"channel","description":"Read the rules","emoji_id":null,"emoji_name":"👋"}]}`))
		case http.MethodPatch:
			if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "community%20refresh" {
				t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "community%20refresh")
			}

			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if got := string(payload["enabled"]); got != "true" {
				t.Fatalf("enabled = %s, want true", got)
			}
			if got := string(payload["description"]); got != "null" {
				t.Fatalf("description = %s, want null", got)
			}
			var channels []GuildWelcomeScreenChannel
			if err := json.Unmarshal(payload["welcome_channels"], &channels); err != nil {
				t.Fatalf("json.Unmarshal welcome_channels returned error: %v", err)
			}
			if len(channels) != 1 || channels[0].ChannelID != "channel" || channels[0].EmojiID != nil || channels[0].EmojiName == nil || *channels[0].EmojiName != "👋" {
				t.Fatalf("welcome_channels = %#v", channels)
			}

			_, _ = w.Write([]byte(`{"description":null,"welcome_channels":[{"channel_id":"channel","description":"Read the rules","emoji_id":null,"emoji_name":"👋"}]}`))
		default:
			t.Fatalf("method = %q, want GET or PATCH", r.Method)
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

	welcomeScreen, err := session.GuildWelcomeScreen("guild")
	if err != nil {
		t.Fatalf("GuildWelcomeScreen returned error: %v", err)
	}
	if welcomeScreen == nil || welcomeScreen.Description == nil || *welcomeScreen.Description != "Welcome" || len(welcomeScreen.WelcomeChannels) != 1 {
		t.Fatalf("welcomeScreen = %#v", welcomeScreen)
	}

	enabled := true
	enabledValue := &enabled
	emojiName := "👋"
	channels := []GuildWelcomeScreenChannel{{
		ChannelID:   "channel",
		Description: "Read the rules",
		EmojiName:   &emojiName,
	}}
	clearDescription := (*string)(nil)
	welcomeScreen, err = session.GuildWelcomeScreenEdit("guild", &GuildWelcomeScreenParams{
		Enabled:         &enabledValue,
		WelcomeChannels: &channels,
		Description:     &clearDescription,
	}, WithAuditLogReason("community refresh"))
	if err != nil {
		t.Fatalf("GuildWelcomeScreenEdit returned error: %v", err)
	}
	if welcomeScreen == nil || welcomeScreen.Description != nil || len(welcomeScreen.WelcomeChannels) != 1 {
		t.Fatalf("welcomeScreen = %#v", welcomeScreen)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestGuildWelcomeScreenRejectsNullResponses(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) (*GuildWelcomeScreen, error)
	}{
		{"get", func(s *Session) (*GuildWelcomeScreen, error) {
			return s.GuildWelcomeScreen("guild")
		}},
		{"edit", func(s *Session) (*GuildWelcomeScreen, error) {
			return s.GuildWelcomeScreenEdit("guild", &GuildWelcomeScreenParams{})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader("null")),
					Request:    r,
				}, nil
			})

			welcomeScreen, err := test.call(session)
			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
			if welcomeScreen != nil {
				t.Fatalf("welcomeScreen = %#v, want nil", welcomeScreen)
			}
		})
	}
}

func TestGuildIncidentActionsEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPut)
		}
		if r.URL.Path != "/guilds/guild/incident-actions" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/incident-actions")
		}

		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if got := string(payload["invites_disabled_until"]); got != `"2026-07-11T10:00:00Z"` {
			t.Fatalf("invites_disabled_until = %s", got)
		}
		if got := string(payload["dms_disabled_until"]); got != "null" {
			t.Fatalf("dms_disabled_until = %s, want null", got)
		}

		_, _ = w.Write([]byte(`{"invites_disabled_until":"2026-07-11T10:00:00Z","dms_disabled_until":null,"dm_spam_detected_at":null,"raid_detected_at":null}`))
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

	invitesUntil := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	invitesAction := &invitesUntil
	disableDMs := (*time.Time)(nil)
	incidents, err := session.GuildIncidentActionsEdit("guild", &GuildIncidentActionsParams{
		InvitesDisabledUntil: &invitesAction,
		DMsDisabledUntil:     &disableDMs,
	})
	if err != nil {
		t.Fatalf("GuildIncidentActionsEdit returned error: %v", err)
	}
	if incidents == nil || incidents.InvitesDisabledUntil == nil || !incidents.InvitesDisabledUntil.Equal(invitesUntil) || incidents.DMsDisabledUntil != nil {
		t.Fatalf("incidents = %#v", incidents)
	}
}

func TestGuildIncidentActionsEditRejectsNullResponse(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("null")),
			Request:    r,
		}, nil
	})

	incidents, err := session.GuildIncidentActionsEdit("guild", &GuildIncidentActionsParams{})
	if !errors.Is(err, ErrJSONUnmarshal) {
		t.Fatalf("GuildIncidentActionsEdit error = %v, want ErrJSONUnmarshal", err)
	}
	if incidents != nil {
		t.Fatalf("incidents = %#v, want nil", incidents)
	}
}

func TestGuildVanityURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/vanity-url" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/vanity-url")
		}

		_, _ = w.Write([]byte(`{"code":"discord-go","uses":42}`))
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

	invite, err := session.GuildVanityURL("guild")
	if err != nil {
		t.Fatalf("GuildVanityURL returned error: %v", err)
	}
	if invite == nil || invite.Code != "discord-go" || invite.Uses != 42 {
		t.Fatalf("invite = %#v, want code %q with %d uses", invite, "discord-go", 42)
	}
}

func TestGuildVanityURLAllowsNullCode(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":null,"uses":0}`)),
			Request:    r,
		}, nil
	})

	invite, err := session.GuildVanityURL("guild")
	if err != nil {
		t.Fatalf("GuildVanityURL returned error: %v", err)
	}
	if invite == nil || invite.Code != "" || invite.Uses != 0 {
		t.Fatalf("invite = %#v, want empty code and zero uses", invite)
	}
}

func TestGuildTemplateCreateWithError(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		wantPath := "/api/v" + APIVersion + "/guilds/guild/templates"
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Test"); got != "template" {
			t.Fatalf("X-Test = %q, want %q", got, "template")
		}

		var params GuildTemplateParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if params.Name != "Template" || params.Description != "Description" {
			t.Fatalf("params = %#v", params)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":"template","name":"Template","source_guild_id":"guild"}`)),
			Request:    r,
		}, nil
	})

	template, err := session.GuildTemplateCreateWithError(
		"guild",
		&GuildTemplateParams{Name: "Template", Description: "Description"},
		WithHeader("X-Test", "template"),
	)
	if err != nil {
		t.Fatalf("GuildTemplateCreateWithError returned error: %v", err)
	}
	if template == nil || template.Code != "template" || template.Name != "Template" || template.SourceGuildID != "guild" {
		t.Fatalf("template = %#v", template)
	}
}

func TestGuildTemplateCreateWithErrorResponseErrors(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantREST      bool
		wantUnmarshal bool
	}{
		{name: "REST error", status: http.StatusForbidden, body: `{"code":50001,"message":"Missing Access"}`, wantREST: true},
		{name: "malformed response", status: http.StatusOK, body: `{`, wantUnmarshal: true},
		{name: "empty response", status: http.StatusOK, wantUnmarshal: true},
		{name: "null response", status: http.StatusOK, body: `null`, wantUnmarshal: true},
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
					Status:     strconv.Itoa(tt.status) + " " + http.StatusText(tt.status),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			template, err := session.GuildTemplateCreateWithError("guild", &GuildTemplateParams{Name: "Template"})
			if template != nil {
				t.Fatalf("template = %#v, want nil", template)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
				}
			}
			if tt.wantUnmarshal && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %T %v, want ErrJSONUnmarshal", err, err)
			}
		})
	}
}

func TestGuildTemplateCreateLegacyWrapper(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "success", body: `{"code":"template"}`, wantCode: "template"},
		{name: "error remains nil result", body: `{`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			template := session.GuildTemplateCreate("guild", &GuildTemplateParams{Name: "Template"})
			if tt.wantCode == "" {
				if template != nil {
					t.Fatalf("template = %#v, want nil", template)
				}
				return
			}
			if template == nil || template.Code != tt.wantCode {
				t.Fatalf("template = %#v, want code %q", template, tt.wantCode)
			}
		})
	}
}

func TestChannelInviteCreateWithTargetUsersFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/channels/channel/invites" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/channels/channel/invites")
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}

		var payload struct {
			MaxAge              int              `json:"max_age"`
			MaxUses             int              `json:"max_uses"`
			Temporary           bool             `json:"temporary"`
			Unique              bool             `json:"unique"`
			TargetType          InviteTargetType `json:"target_type"`
			TargetApplicationID string           `json:"target_application_id"`
			RoleIDs             []string         `json:"role_ids"`
		}
		if err := json.Unmarshal([]byte(r.MultipartForm.Value["payload_json"][0]), &payload); err != nil {
			t.Fatalf("json.Unmarshal payload returned error: %v", err)
		}
		if payload.MaxAge != 60 || payload.MaxUses != 2 || !payload.Temporary || !payload.Unique {
			t.Fatalf("payload = %#v", payload)
		}
		if payload.TargetType != InviteTargetEmbeddedApplication || payload.TargetApplicationID != "app" {
			t.Fatalf("payload target = %#v", payload)
		}
		if len(payload.RoleIDs) != 2 || payload.RoleIDs[0] != "role-1" || payload.RoleIDs[1] != "role-2" {
			t.Fatalf("role_ids = %#v", payload.RoleIDs)
		}

		file, header, err := r.FormFile("target_users_file")
		if err != nil {
			t.Fatalf("FormFile returned error: %v", err)
		}
		defer file.Close()
		contents, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if header.Filename != "users.csv" || string(contents) != "user_id\n1\n" {
			t.Fatalf("file = %q/%q", header.Filename, contents)
		}

		_, _ = w.Write([]byte(`{"type":0,"code":"invite"}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointChannels := EndpointChannels
	EndpointChannels = server.URL + "/channels/"
	t.Cleanup(func() {
		EndpointChannels = oldEndpointChannels
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	invite, err := session.ChannelInviteCreate("channel", Invite{
		MaxAge:              60,
		MaxUses:             2,
		Temporary:           true,
		Unique:              true,
		TargetType:          InviteTargetEmbeddedApplication,
		TargetApplicationID: "app",
		RoleIDs:             []string{"role-1", "role-2"},
		TargetUsersFile: &File{
			Name:        "users.csv",
			ContentType: "text/csv",
			Reader:      strings.NewReader("user_id\n1\n"),
		},
	})
	if err != nil {
		t.Fatalf("ChannelInviteCreate returned error: %v", err)
	}
	if invite.Code != "invite" || invite.Type != InviteTypeGuild {
		t.Fatalf("invite = %#v", invite)
	}
}

func TestInviteTargetUsersEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /invites/invite/target-users":
			_, _ = w.Write([]byte("user_id\n1\n"))
		case http.MethodPut + " /invites/invite/target-users":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
				t.Fatalf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm returned error: %v", err)
			}
			if _, ok := r.MultipartForm.Value["payload_json"]; ok {
				t.Fatalf("payload_json was included in target-users upload")
			}
			file, header, err := r.FormFile("target_users_file")
			if err != nil {
				t.Fatalf("FormFile returned error: %v", err)
			}
			defer file.Close()
			contents, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("ReadAll returned error: %v", err)
			}
			if header.Filename != "users.csv" || string(contents) != "user_id\n2\n" {
				t.Fatalf("file = %q/%q", header.Filename, contents)
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet + " /invites/invite/target-users/job-status":
			_, _ = w.Write([]byte(`{"status":2,"total_users":1,"processed_users":1,"created_at":"2026-01-01T00:00:00Z","completed_at":"2026-01-01T00:00:01Z"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	oldEndpointAPI := EndpointAPI
	EndpointAPI = server.URL + "/"
	t.Cleanup(func() {
		EndpointAPI = oldEndpointAPI
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	data, err := session.InviteTargetUsers("invite")
	if err != nil {
		t.Fatalf("InviteTargetUsers returned error: %v", err)
	}
	if string(data) != "user_id\n1\n" {
		t.Fatalf("target users data = %q", data)
	}

	if err := session.InviteTargetUsersUpdate("invite", &File{Name: "users.csv", ContentType: "text/csv", Reader: strings.NewReader("user_id\n2\n")}); err != nil {
		t.Fatalf("InviteTargetUsersUpdate returned error: %v", err)
	}
	if err := session.InviteTargetUsersUpdate("invite", nil); err == nil {
		t.Fatal("InviteTargetUsersUpdate returned nil error for nil file")
	}

	job, err := session.InviteTargetUsersJobStatus("invite")
	if err != nil {
		t.Fatalf("InviteTargetUsersJobStatus returned error: %v", err)
	}
	if job.Status != InviteTargetUsersJobStatusCompleted || job.TotalUsers != 1 || job.ProcessedUsers != 1 {
		t.Fatalf("job = %#v", job)
	}
}

func TestChannelVoiceStatusSet(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPut)
		}
		if r.URL.Path != "/channels/channel/voice-status" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/channels/channel/voice-status")
		}
		var payload struct {
			Status *string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		requests++
		switch requests {
		case 1:
			if payload.Status == nil || *payload.Status != "Live" {
				t.Fatalf("status = %#v, want Live", payload.Status)
			}
		case 2:
			if payload.Status != nil {
				t.Fatalf("status = %#v, want nil", payload.Status)
			}
		default:
			t.Fatalf("unexpected request %d", requests)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	oldEndpointChannels := EndpointChannels
	EndpointChannels = server.URL + "/channels/"
	t.Cleanup(func() {
		EndpointChannels = oldEndpointChannels
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	status := "Live"
	if err := session.ChannelVoiceStatusSet("channel", &status); err != nil {
		t.Fatalf("ChannelVoiceStatusSet returned error: %v", err)
	}
	if err := session.ChannelVoiceStatusSet("channel", nil); err != nil {
		t.Fatalf("ChannelVoiceStatusSet clear returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestSoundboardEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case http.MethodPost + " /channels/channel/send-soundboard-sound":
			var payload struct {
				SoundID       string `json:"sound_id"`
				SourceGuildID string `json:"source_guild_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode send returned error: %v", err)
			}
			if payload.SoundID != "sound" || payload.SourceGuildID != "guild" {
				t.Fatalf("send payload = %#v", payload)
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet + " /soundboard-default-sounds":
			_, _ = w.Write([]byte(`[{"sound_id":"default","name":"Default","volume":1}]`))
		case http.MethodGet + " /guilds/guild/soundboard-sounds":
			_, _ = w.Write([]byte(`{"items":[{"sound_id":"sound","name":"Sound","volume":1,"guild_id":"guild"}]}`))
		case http.MethodGet + " /guilds/guild/soundboard-sounds/sound":
			_, _ = w.Write([]byte(`{"sound_id":"sound","name":"Sound","volume":1,"guild_id":"guild"}`))
		case http.MethodPost + " /guilds/guild/soundboard-sounds":
			var payload SoundboardSoundParams
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode create returned error: %v", err)
			}
			if payload.Name != "Created" || payload.Sound == "" || payload.Volume == nil || *payload.Volume != 0.5 {
				t.Fatalf("create payload = %#v", payload)
			}
			_, _ = w.Write([]byte(`{"sound_id":"created","name":"Created","volume":0.5,"guild_id":"guild"}`))
		case http.MethodPatch + " /guilds/guild/soundboard-sounds/sound":
			var payload SoundboardSoundParams
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode edit returned error: %v", err)
			}
			if payload.Name != "Edited" {
				t.Fatalf("edit payload = %#v", payload)
			}
			_, _ = w.Write([]byte(`{"sound_id":"sound","name":"Edited","volume":1,"guild_id":"guild"}`))
		case http.MethodDelete + " /guilds/guild/soundboard-sounds/sound":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	oldEndpointChannels := EndpointChannels
	oldEndpointGuilds := EndpointGuilds
	oldEndpointSoundboardDefaultSounds := EndpointSoundboardDefaultSounds
	EndpointChannels = server.URL + "/channels/"
	EndpointGuilds = server.URL + "/guilds/"
	EndpointSoundboardDefaultSounds = server.URL + "/soundboard-default-sounds"
	t.Cleanup(func() {
		EndpointChannels = oldEndpointChannels
		EndpointGuilds = oldEndpointGuilds
		EndpointSoundboardDefaultSounds = oldEndpointSoundboardDefaultSounds
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	if err := session.SoundboardSoundSend("channel", "sound", "guild"); err != nil {
		t.Fatalf("SoundboardSoundSend returned error: %v", err)
	}
	defaultSounds, err := session.SoundboardDefaultSounds()
	if err != nil {
		t.Fatalf("SoundboardDefaultSounds returned error: %v", err)
	}
	if len(defaultSounds) != 1 || defaultSounds[0].SoundID != "default" {
		t.Fatalf("default sounds = %#v", defaultSounds)
	}
	sounds, err := session.GuildSoundboardSounds("guild")
	if err != nil {
		t.Fatalf("GuildSoundboardSounds returned error: %v", err)
	}
	if len(sounds) != 1 || sounds[0].GuildID != "guild" {
		t.Fatalf("guild sounds = %#v", sounds)
	}
	sound, err := session.GuildSoundboardSound("guild", "sound")
	if err != nil {
		t.Fatalf("GuildSoundboardSound returned error: %v", err)
	}
	if sound.SoundID != "sound" {
		t.Fatalf("sound = %#v", sound)
	}

	volume := 0.5
	created, err := session.GuildSoundboardSoundCreate("guild", &SoundboardSoundParams{Name: "Created", Sound: "data:audio/ogg;base64,AAAA", Volume: &volume})
	if err != nil {
		t.Fatalf("GuildSoundboardSoundCreate returned error: %v", err)
	}
	if created.SoundID != "created" {
		t.Fatalf("created = %#v", created)
	}
	if _, err := session.GuildSoundboardSoundCreate("guild", nil); err == nil {
		t.Fatal("GuildSoundboardSoundCreate returned nil error for nil data")
	}
	edited, err := session.GuildSoundboardSoundEdit("guild", "sound", &SoundboardSoundParams{Name: "Edited"})
	if err != nil {
		t.Fatalf("GuildSoundboardSoundEdit returned error: %v", err)
	}
	if edited.Name != "Edited" {
		t.Fatalf("edited = %#v", edited)
	}
	if _, err := session.GuildSoundboardSoundEdit("guild", "sound", nil); err == nil {
		t.Fatal("GuildSoundboardSoundEdit returned nil error for nil data")
	}
	if err := session.GuildSoundboardSoundDelete("guild", "sound"); err != nil {
		t.Fatalf("GuildSoundboardSoundDelete returned error: %v", err)
	}
}

func TestGuildScheduledEventRecurrencePayloads(t *testing.T) {
	requests := 0
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		rawRule, ok := payload["recurrence_rule"]
		if !ok {
			t.Fatalf("recurrence_rule missing from %s request", r.Method)
		}

		switch r.Method + " " + r.URL.Path {
		case http.MethodPost + " /guilds/guild/scheduled-events":
			var rule GuildScheduledEventRecurrenceRuleParams
			if err := json.Unmarshal(rawRule, &rule); err != nil {
				t.Fatalf("Unmarshal create recurrence_rule returned error: %v", err)
			}
			if !rule.Start.Equal(time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)) || rule.Frequency != GuildScheduledEventRecurrenceRuleFrequencyWeekly || rule.Interval != 2 {
				t.Fatalf("create recurrence_rule = %#v", rule)
			}
			if len(rule.ByWeekday) != 1 || rule.ByWeekday[0] != GuildScheduledEventRecurrenceRuleWeekdayWednesday {
				t.Fatalf("create by_weekday = %#v", rule.ByWeekday)
			}
			_, _ = w.Write([]byte(`{
				"id":"event",
				"guild_id":"guild",
				"scheduled_start_time":"2026-07-15T12:00:00Z",
				"recurrence_rule":{
					"start":"2026-07-15T12:00:00Z",
					"end":null,
					"frequency":2,
					"interval":2,
					"by_weekday":[2],
					"count":null
				}
			}`))
		case http.MethodPatch + " /guilds/guild/scheduled-events/event":
			patches++
			if patches == 1 {
				var rule GuildScheduledEventRecurrenceRuleParams
				if err := json.Unmarshal(rawRule, &rule); err != nil {
					t.Fatalf("Unmarshal edit recurrence_rule returned error: %v", err)
				}
				if rule.Frequency != GuildScheduledEventRecurrenceRuleFrequencyMonthly || rule.Interval != 1 || len(rule.ByNWeekday) != 1 || rule.ByNWeekday[0].N != 4 || rule.ByNWeekday[0].Day != GuildScheduledEventRecurrenceRuleWeekdayWednesday {
					t.Fatalf("edit recurrence_rule = %#v", rule)
				}
				_, _ = w.Write([]byte(`{
					"id":"event",
					"guild_id":"guild",
					"scheduled_start_time":"2026-07-15T12:00:00Z",
					"recurrence_rule":{
						"start":"2026-07-15T12:00:00Z",
						"end":null,
						"frequency":1,
						"interval":1,
						"by_n_weekday":[{"n":4,"day":2}],
						"count":null
					}
				}`))
				return
			}
			if string(rawRule) != "null" {
				t.Fatalf("clear recurrence_rule = %s, want null", rawRule)
			}
			_, _ = w.Write([]byte(`{
				"id":"event",
				"guild_id":"guild",
				"scheduled_start_time":"2026-07-15T12:00:00Z",
				"recurrence_rule":null
			}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
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

	start := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	createRule := &GuildScheduledEventRecurrenceRuleParams{
		Start:     start,
		Frequency: GuildScheduledEventRecurrenceRuleFrequencyWeekly,
		Interval:  2,
		ByWeekday: []GuildScheduledEventRecurrenceRuleWeekday{GuildScheduledEventRecurrenceRuleWeekdayWednesday},
	}
	created, err := session.GuildScheduledEventCreate("guild", &GuildScheduledEventParams{
		Name:               "Recurring event",
		ScheduledStartTime: &start,
		PrivacyLevel:       GuildScheduledEventPrivacyLevelGuildOnly,
		EntityType:         GuildScheduledEventEntityTypeVoice,
		RecurrenceRule:     &createRule,
	})
	if err != nil {
		t.Fatalf("GuildScheduledEventCreate returned error: %v", err)
	}
	if created.RecurrenceRule == nil || created.RecurrenceRule.Frequency != GuildScheduledEventRecurrenceRuleFrequencyWeekly {
		t.Fatalf("created recurrence rule = %#v", created.RecurrenceRule)
	}

	editRule := &GuildScheduledEventRecurrenceRuleParams{
		Start:     start,
		Frequency: GuildScheduledEventRecurrenceRuleFrequencyMonthly,
		Interval:  1,
		ByNWeekday: []GuildScheduledEventRecurrenceRuleNWeekday{
			{N: 4, Day: GuildScheduledEventRecurrenceRuleWeekdayWednesday},
		},
	}
	edited, err := session.GuildScheduledEventEdit("guild", "event", &GuildScheduledEventParams{RecurrenceRule: &editRule})
	if err != nil {
		t.Fatalf("GuildScheduledEventEdit returned error: %v", err)
	}
	if edited.RecurrenceRule == nil || len(edited.RecurrenceRule.ByNWeekday) != 1 {
		t.Fatalf("edited recurrence rule = %#v", edited.RecurrenceRule)
	}

	var clearRule *GuildScheduledEventRecurrenceRuleParams
	cleared, err := session.GuildScheduledEventEdit("guild", "event", &GuildScheduledEventParams{RecurrenceRule: &clearRule})
	if err != nil {
		t.Fatalf("GuildScheduledEventEdit clear returned error: %v", err)
	}
	if cleared.RecurrenceRule != nil {
		t.Fatalf("cleared recurrence rule = %#v, want nil", cleared.RecurrenceRule)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestApplicationActivityInstanceEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/applications/app/activity-instances/instance" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/applications/app/activity-instances/instance")
		}
		_, _ = w.Write([]byte(`{"application_id":"app","instance_id":"instance","launch_id":"launch","location":{"id":"gc-guild-channel","kind":"gc","channel_id":"channel","guild_id":"guild"},"users":["user"]}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointApplications := EndpointApplications
	EndpointApplications = server.URL + "/applications"
	t.Cleanup(func() {
		EndpointApplications = oldEndpointApplications
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	instance, err := session.ApplicationActivityInstance("app", "instance")
	if err != nil {
		t.Fatalf("ApplicationActivityInstance returned error: %v", err)
	}
	if instance.ApplicationID != "app" || instance.InstanceID != "instance" {
		t.Fatalf("instance = %#v", instance)
	}
	if instance.Location == nil || instance.Location.ChannelID != "channel" {
		t.Fatalf("location = %#v", instance.Location)
	}
}

func TestCurrentApplicationEndpoints(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/applications/@me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/applications/@me")
		}

		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"app","name":"Application","event_webhooks_status":3,"event_webhooks_types":["APPLICATION_DEAUTHORIZED"],"team":{"id":"team","members":[{"role":"admin","permissions":["*"]}]}}`))
		case http.MethodPatch:
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			for key, want := range map[string]string{
				"description":               `"updated"`,
				"icon":                      `null`,
				"cover_image":               `null`,
				"tags":                      `[]`,
				"event_webhooks_status":     `2`,
				"event_webhooks_types":      `["APPLICATION_AUTHORIZED"]`,
				"interactions_endpoint_url": `""`,
			} {
				if got := string(payload[key]); got != want {
					t.Fatalf("%s = %s, want %s", key, got, want)
				}
			}
			if _, ok := payload["custom_install_url"]; ok {
				t.Fatal("custom_install_url was included when unset")
			}
			_, _ = w.Write([]byte(`{"id":"app","name":"Application","description":"updated","event_webhooks_status":2,"event_webhooks_types":["APPLICATION_AUTHORIZED"]}`))
		default:
			t.Fatalf("method = %q, want GET or PATCH", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	oldEndpoint := EndpointCurrentApplication
	EndpointCurrentApplication = server.URL + "/applications/@me"
	t.Cleanup(func() {
		EndpointCurrentApplication = oldEndpoint
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	application, err := session.CurrentApplication()
	if err != nil {
		t.Fatalf("CurrentApplication returned error: %v", err)
	}
	if application.EventWebhooksStatus != ApplicationWebhookEventStatusDisabledByDiscord {
		t.Fatalf("EventWebhooksStatus = %d", application.EventWebhooksStatus)
	}
	if len(application.EventWebhooksTypes) != 1 || application.EventWebhooksTypes[0] != ApplicationWebhookEventTypeApplicationDeauthorized {
		t.Fatalf("EventWebhooksTypes = %#v", application.EventWebhooksTypes)
	}
	if application.Team == nil || len(application.Team.Members) != 1 {
		t.Fatalf("Team = %#v", application.Team)
	}
	member := application.Team.Members[0]
	if member.Role != "admin" || len(member.Permissions) != 1 || member.Permissions[0] != "*" {
		t.Fatalf("Team member = %#v", member)
	}

	description := "updated"
	empty := ""
	tags := []string{}
	eventTypes := []ApplicationWebhookEventType{ApplicationWebhookEventTypeApplicationAuthorized}
	status := ApplicationWebhookEventStatusEnabled
	application, err = session.CurrentApplicationEdit(&ApplicationEditParams{
		Description:             &description,
		Icon:                    &empty,
		CoverImage:              &empty,
		InteractionsEndpointURL: &empty,
		Tags:                    &tags,
		EventWebhooksStatus:     &status,
		EventWebhooksTypes:      &eventTypes,
	})
	if err != nil {
		t.Fatalf("CurrentApplicationEdit returned error: %v", err)
	}
	if application.Description != description || application.EventWebhooksStatus != status || len(application.EventWebhooksTypes) != 1 || application.EventWebhooksTypes[0] != ApplicationWebhookEventTypeApplicationAuthorized {
		t.Fatalf("application = %#v", application)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestCurrentApplicationErrors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		call        func(*Session) (*Application, error)
		wantJSONErr bool
	}{
		{
			name:   "get http error",
			status: http.StatusInternalServerError,
			body:   `{"message":"failed"}`,
			call: func(s *Session) (*Application, error) {
				return s.CurrentApplication()
			},
		},
		{
			name:        "get malformed response",
			status:      http.StatusOK,
			body:        `{`,
			wantJSONErr: true,
			call: func(s *Session) (*Application, error) {
				return s.CurrentApplication()
			},
		},
		{
			name:   "edit http error",
			status: http.StatusInternalServerError,
			body:   `{"message":"failed"}`,
			call: func(s *Session) (*Application, error) {
				return s.CurrentApplicationEdit(&ApplicationEditParams{})
			},
		},
		{
			name:        "edit malformed response",
			status:      http.StatusOK,
			body:        `{`,
			wantJSONErr: true,
			call: func(s *Session) (*Application, error) {
				return s.CurrentApplicationEdit(&ApplicationEditParams{})
			},
		},
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

			application, err := tt.call(session)
			if err == nil {
				t.Fatal("call returned nil error")
			}
			if application != nil {
				t.Fatalf("application = %#v, want nil", application)
			}
			if tt.wantJSONErr && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}
}

func TestAutoModerationRuleCreateCurrentVariants(t *testing.T) {
	disabled := false
	tests := []struct {
		name           string
		rule           *AutoModerationRule
		wantProtection *bool
	}{
		{
			name: "member profile rule",
			rule: &AutoModerationRule{
				Name:        "profile",
				EventType:   AutoModerationEventMemberUpdate,
				TriggerType: AutoModerationEventTriggerMemberProfile,
				TriggerMetadata: &AutoModerationTriggerMetadata{
					KeywordFilter: []string{"blocked"},
					RegexPatterns: []string{"^blocked$"},
				},
				Actions: []AutoModerationAction{{Type: AutoModerationRuleActionBlockMemberInteraction}},
			},
		},
		{
			name: "mention raid protection disabled",
			rule: &AutoModerationRule{
				Name:        "mentions",
				EventType:   AutoModerationEventMessageSend,
				TriggerType: AutoModerationEventTriggerMentionSpam,
				TriggerMetadata: &AutoModerationTriggerMetadata{
					MentionTotalLimit:            5,
					MentionRaidProtectionEnabled: &disabled,
				},
				Actions: []AutoModerationAction{{Type: AutoModerationRuleActionBlockMessage}},
			},
			wantProtection: &disabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
				}
				wantPath := "/api/v" + APIVersion + "/guilds/guild/auto-moderation/rules"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
				}
				if got := r.Header.Get("X-Test"); got != "automod" {
					t.Fatalf("X-Test = %q, want automod", got)
				}

				var payload AutoModerationRule
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("Decode returned error: %v", err)
				}
				if payload.EventType != tt.rule.EventType || payload.TriggerType != tt.rule.TriggerType || len(payload.Actions) != 1 || payload.Actions[0].Type != tt.rule.Actions[0].Type {
					t.Fatalf("payload = %#v", payload)
				}
				if tt.wantProtection != nil && (payload.TriggerMetadata == nil || payload.TriggerMetadata.MentionRaidProtectionEnabled == nil || *payload.TriggerMetadata.MentionRaidProtectionEnabled != *tt.wantProtection) {
					t.Fatalf("MentionRaidProtectionEnabled = %#v", payload.TriggerMetadata)
				}
				body, err := json.Marshal(&payload)
				if err != nil {
					t.Fatalf("json.Marshal returned error: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(bytes.NewReader(body)),
					Request:    r,
				}, nil
			})

			created, err := session.AutoModerationRuleCreate("guild", tt.rule, WithHeader("X-Test", "automod"))
			if err != nil {
				t.Fatalf("AutoModerationRuleCreate returned error: %v", err)
			}
			if created.EventType != tt.rule.EventType || created.TriggerType != tt.rule.TriggerType || created.Actions[0].Type != tt.rule.Actions[0].Type {
				t.Fatalf("created rule = %#v", created)
			}
		})
	}
}

func TestWebhookDeleteWithTokenAllowsNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodDelete)
		}
		if r.URL.Path != "/webhooks/webhook/token" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/webhooks/webhook/token")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	oldEndpointWebhooks := EndpointWebhooks
	EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		EndpointWebhooks = oldEndpointWebhooks
	})

	session, err := New("")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	webhook, err := session.WebhookDeleteWithToken("webhook", "token")
	if err != nil {
		t.Fatalf("WebhookDeleteWithToken returned error: %v", err)
	}
	if webhook != nil {
		t.Fatalf("WebhookDeleteWithToken returned webhook = %#v, want nil", webhook)
	}
}

func TestWebhookTokenEndpointsOmitAuthorization(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) error
		body string
	}{
		{
			name: "get webhook with token",
			call: func(s *Session) error {
				_, err := s.WebhookWithToken("webhook", "token")
				return err
			},
			body: `{"id":"webhook","type":1}`,
		},
		{
			name: "execute webhook",
			call: func(s *Session) error {
				_, err := s.WebhookExecute("webhook", "token", false, &WebhookParams{Content: "hello"})
				return err
			},
			body: ``,
		},
		{
			name: "get webhook message",
			call: func(s *Session) error {
				_, err := s.WebhookMessage("webhook", "token", "message")
				return err
			},
			body: `{"id":"message","channel_id":"channel"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if got := r.Header.Get("Authorization"); got != "" {
					t.Fatalf("Authorization header = %q, want empty", got)
				}

				statusCode := http.StatusOK
				status := "200 OK"
				if tt.body == "" {
					statusCode = http.StatusNoContent
					status = "204 No Content"
				}

				return &http.Response{
					StatusCode: statusCode,
					Status:     status,
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			if err := tt.call(session); err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			if !called {
				t.Fatal("HTTP transport was not called")
			}
		})
	}
}

func TestInteractionTokenEndpointsOmitAuthorization(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) error
	}{
		{
			name: "initial response",
			call: func(s *Session) error {
				return s.InteractionRespond(&Interaction{ID: "interaction", Token: "token"}, &InteractionResponse{Type: InteractionResponsePong})
			},
		},
		{
			name: "followup message",
			call: func(s *Session) error {
				_, err := s.FollowupMessageCreate(&Interaction{AppID: "application", Token: "token"}, false, &WebhookParams{Content: "hello"})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if got := r.Header.Get("Authorization"); got != "" {
					t.Fatalf("Authorization header = %q, want empty", got)
				}

				return &http.Response{
					StatusCode: http.StatusNoContent,
					Status:     "204 No Content",
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    r,
				}, nil
			})

			if err := tt.call(session); err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			if !called {
				t.Fatal("HTTP transport was not called")
			}
		})
	}
}

func TestWebhookExecuteComplexJSON(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.AllowedMentions = &MessageAllowedMentions{Parse: []AllowedMentionType{}}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/webhooks/webhook/token"
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, wantPath)
		}
		if r.URL.RawQuery != "wait=true&with_components=true" {
			t.Fatalf("query = %q, want wait=true&with_components=true", r.URL.RawQuery)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Fatalf("Authorization = %q, want empty", authorization)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatalf("json.Unmarshal fields returned error: %v", err)
		}
		if _, ok := fields["applied_tags"]; !ok {
			t.Fatalf("payload fields = %#v, want applied_tags", fields)
		}
		if _, ok := fields["poll"]; !ok {
			t.Fatalf("payload fields = %#v, want poll", fields)
		}

		var payload WebhookParams
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json.Unmarshal payload returned error: %v", err)
		}
		if payload.ThreadName != "topic" || len(payload.AppliedTags) != 2 || payload.AppliedTags[0] != "tag-one" || payload.AppliedTags[1] != "tag-two" {
			t.Fatalf("thread payload = %#v/%#v", payload.ThreadName, payload.AppliedTags)
		}
		if payload.Poll == nil || payload.Poll.Question.Text != "Choose" || len(payload.Poll.Answers) != 2 || payload.Poll.Duration != 24 || !payload.Poll.AllowMultiselect {
			t.Fatalf("Poll = %#v", payload.Poll)
		}
		if payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
			t.Fatalf("AllowedMentions = %#v", payload.AllowedMentions)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"channel","content":"sent"}`)),
			Request:    r,
		}, nil
	})

	message, err := session.WebhookExecuteComplex(
		"webhook",
		"token",
		WebhookExecuteOptions{Wait: true, WithComponents: true},
		&WebhookParams{
			ThreadName:  "topic",
			AppliedTags: []string{"tag-one", "tag-two"},
			Poll: &Poll{
				Question: PollMedia{Text: "Choose"},
				Answers: []PollAnswer{
					{Media: &PollMedia{Text: "One"}},
					{Media: &PollMedia{Text: "Two"}},
				},
				AllowMultiselect: true,
				Duration:         24,
				LayoutType:       PollLayoutTypeDefault,
			},
		},
	)
	if err != nil {
		t.Fatalf("WebhookExecuteComplex returned error: %v", err)
	}
	if message == nil || message.ID != "message" || message.ChannelID != "channel" || message.Content != "sent" {
		t.Fatalf("message = %#v", message)
	}
}

func TestWebhookExecuteComplexMultipart(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.RawQuery != "thread_id=thread&wait=true&with_components=true" {
			t.Fatalf("query = %q, want thread_id=thread&wait=true&with_components=true", r.URL.RawQuery)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}

		payloadJSON := r.MultipartForm.Value["payload_json"]
		if len(payloadJSON) != 1 {
			t.Fatalf("payload_json = %#v", payloadJSON)
		}
		var payload WebhookParams
		if err := json.Unmarshal([]byte(payloadJSON[0]), &payload); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if payload.Poll == nil || payload.Poll.Question.Text != "Upload?" || len(payload.Poll.Answers) != 2 {
			t.Fatalf("Poll = %#v", payload.Poll)
		}

		file, header, err := r.FormFile("files[0]")
		if err != nil {
			t.Fatalf("FormFile returned error: %v", err)
		}
		defer file.Close()
		contents, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if header.Filename != "note.txt" || string(contents) != "attachment" {
			t.Fatalf("file = %q/%q", header.Filename, contents)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"thread"}`)),
			Request:    r,
		}, nil
	})

	message, err := session.WebhookExecuteComplex(
		"webhook",
		"token",
		WebhookExecuteOptions{Wait: true, ThreadID: "thread", WithComponents: true},
		&WebhookParams{
			Files: []*File{{
				Name:        "note.txt",
				ContentType: "text/plain",
				Reader:      strings.NewReader("attachment"),
			}},
			Poll: &Poll{
				Question: PollMedia{Text: "Upload?"},
				Answers: []PollAnswer{
					{Media: &PollMedia{Text: "Yes"}},
					{Media: &PollMedia{Text: "No"}},
				},
				Duration: 1,
			},
		},
	)
	if err != nil {
		t.Fatalf("WebhookExecuteComplex returned error: %v", err)
	}
	if message == nil || message.ID != "message" || message.ChannelID != "thread" {
		t.Fatalf("message = %#v", message)
	}
}

func TestWebhookExecuteComplexErrors(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	message, err := session.WebhookExecuteComplex("webhook", "token", WebhookExecuteOptions{}, nil)
	if err == nil || err.Error() != "webhook data cannot be nil" {
		t.Fatalf("error = %v, want webhook data cannot be nil", err)
	}
	if message != nil {
		t.Fatalf("message = %#v, want nil", message)
	}

	t.Run("malformed response", func(t *testing.T) {
		session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{`)),
				Request:    r,
			}, nil
		})
		message, err := session.WebhookExecuteComplex(
			"webhook",
			"token",
			WebhookExecuteOptions{Wait: true},
			&WebhookParams{Content: "hello"},
		)
		if !errors.Is(err, ErrJSONUnmarshal) {
			t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
		}
		if message != nil {
			t.Fatalf("message = %#v, want nil", message)
		}
	})

	t.Run("http error without wait", func(t *testing.T) {
		session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Body:       io.NopCloser(strings.NewReader(`{"code":50035,"message":"Invalid Form Body"}`)),
				Request:    r,
			}, nil
		})
		message, err := session.WebhookExecuteComplex(
			"webhook",
			"token",
			WebhookExecuteOptions{},
			&WebhookParams{Content: "hello"},
		)
		var restErr *RESTError
		if !errors.As(err, &restErr) || restErr.Response.StatusCode != http.StatusBadRequest {
			t.Fatalf("error = %T %v, want 400 RESTError", err, err)
		}
		if message != nil {
			t.Fatalf("message = %#v, want nil", message)
		}
	})
}

func TestWebhookExecuteLegacyQueries(t *testing.T) {
	tests := []struct {
		name      string
		wantQuery string
		call      func(*Session) (*Message, error)
	}{
		{
			name:      "execute",
			wantQuery: "wait=true",
			call: func(s *Session) (*Message, error) {
				return s.WebhookExecute("webhook", "token", true, &WebhookParams{Content: "hello"})
			},
		},
		{
			name:      "thread execute",
			wantQuery: "thread_id=thread&wait=true",
			call: func(s *Session) (*Message, error) {
				return s.WebhookThreadExecute("webhook", "token", true, "thread", &WebhookParams{Content: "hello"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.RawQuery != tt.wantQuery {
					t.Fatalf("query = %q, want %q", r.URL.RawQuery, tt.wantQuery)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`{"id":"message"}`)),
					Request:    r,
				}, nil
			})

			message, err := tt.call(session)
			if err != nil {
				t.Fatalf("execute returned error: %v", err)
			}
			if message == nil || message.ID != "message" {
				t.Fatalf("message = %#v", message)
			}
		})
	}
}

func TestWebhookMessageOptionsQueries(t *testing.T) {
	content := "updated"
	tests := []struct {
		name      string
		method    string
		wantQuery string
		call      func(*Session) error
	}{
		{
			name:      "get in thread",
			method:    http.MethodGet,
			wantQuery: "thread_id=thread",
			call: func(s *Session) error {
				_, err := s.WebhookMessageWithOptions("webhook", "token", "message", WebhookMessageOptions{ThreadID: "thread"})
				return err
			},
		},
		{
			name:      "edit in thread with components",
			method:    http.MethodPatch,
			wantQuery: "thread_id=thread&with_components=true",
			call: func(s *Session) error {
				_, err := s.WebhookMessageEditWithOptions(
					"webhook",
					"token",
					"message",
					WebhookMessageEditOptions{ThreadID: "thread", WithComponents: true},
					&WebhookEdit{Content: &content},
				)
				return err
			},
		},
		{
			name:      "delete in thread",
			method:    http.MethodDelete,
			wantQuery: "thread_id=thread",
			call: func(s *Session) error {
				return s.WebhookMessageDeleteWithOptions("webhook", "token", "message", WebhookMessageOptions{ThreadID: "thread"})
			},
		},
		{
			name:   "legacy get omits query",
			method: http.MethodGet,
			call: func(s *Session) error {
				_, err := s.WebhookMessage("webhook", "token", "message")
				return err
			},
		},
		{
			name:   "legacy edit omits query",
			method: http.MethodPatch,
			call: func(s *Session) error {
				_, err := s.WebhookMessageEdit("webhook", "token", "message", &WebhookEdit{Content: &content})
				return err
			},
		},
		{
			name:   "legacy delete omits query",
			method: http.MethodDelete,
			call: func(s *Session) error {
				return s.WebhookMessageDelete("webhook", "token", "message")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != tt.method {
					t.Fatalf("method = %q, want %q", r.Method, tt.method)
				}
				wantPath := "/api/v" + APIVersion + "/webhooks/webhook/token/messages/message"
				if r.URL.Path != wantPath || r.URL.RawQuery != tt.wantQuery {
					t.Fatalf("URL = %s, want %s?%s", r.URL.String(), wantPath, tt.wantQuery)
				}
				if authorization := r.Header.Get("Authorization"); authorization != "" {
					t.Fatalf("Authorization = %q, want empty", authorization)
				}
				status := http.StatusOK
				body := `{"id":"message","channel_id":"thread"}`
				if tt.method == http.MethodDelete {
					status = http.StatusNoContent
					body = ""
				}
				return &http.Response{
					StatusCode: status,
					Status:     http.StatusText(status),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    r,
				}, nil
			})

			if err := tt.call(session); err != nil {
				t.Fatalf("webhook message call returned error: %v", err)
			}
		})
	}
}

func TestWebhookMessageEditFlags(t *testing.T) {
	tests := []struct {
		name        string
		edit        *WebhookEdit
		wantFlags   string
		wantPresent bool
		status      int
	}{
		{
			name:   "omitted",
			edit:   &WebhookEdit{},
			status: http.StatusOK,
		},
		{
			name:        "nonzero",
			edit:        &WebhookEdit{Flags: MessageFlagsSuppressEmbeds},
			wantFlags:   "4",
			wantPresent: true,
			status:      http.StatusOK,
		},
		{
			name:        "explicit zero",
			edit:        &WebhookEdit{FlagsSet: true},
			wantFlags:   "0",
			wantPresent: true,
			status:      http.StatusOK,
		},
		{
			name:        "unsupported flag propagates REST error",
			edit:        &WebhookEdit{Flags: MessageFlagsEphemeral, FlagsSet: true},
			wantFlags:   "64",
			wantPresent: true,
			status:      http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPatch {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
				}
				wantPath := "/api/v" + APIVersion + "/webhooks/webhook/token/messages/message"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
				}
				if authorization := r.Header.Get("Authorization"); authorization != "" {
					t.Fatalf("Authorization = %q, want empty", authorization)
				}

				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("Decode returned error: %v", err)
				}
				flags, present := payload["flags"]
				if present != tt.wantPresent {
					t.Fatalf("flags present = %t, want %t", present, tt.wantPresent)
				}
				if present && string(flags) != tt.wantFlags {
					t.Fatalf("flags = %s, want %s", flags, tt.wantFlags)
				}

				body := `{"id":"message","channel_id":"channel"}`
				if tt.status == http.StatusBadRequest {
					body = `{"code":50035,"message":"Invalid Form Body"}`
				}
				return &http.Response{
					StatusCode: tt.status,
					Status:     http.StatusText(tt.status),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    r,
				}, nil
			})

			message, err := session.WebhookMessageEdit("webhook", "token", "message", tt.edit)
			if tt.status == http.StatusBadRequest {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
				}
				if message != nil {
					t.Fatalf("message = %#v, want nil", message)
				}
				return
			}
			if err != nil {
				t.Fatalf("WebhookMessageEdit returned error: %v", err)
			}
			if message == nil || message.ID != "message" {
				t.Fatalf("message = %#v", message)
			}
		})
	}
}

func TestInteractionResponseEditAddsPoll(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}
		wantPath := "/api/v" + APIVersion + "/webhooks/application/token/messages/@original"
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		var payload WebhookEdit
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if payload.Poll == nil || payload.Poll.Question.Text != "Ready?" || len(payload.Poll.Answers) != 2 || payload.Poll.Duration != 1 {
			t.Fatalf("Poll = %#v", payload.Poll)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"channel"}`)),
			Request:    r,
		}, nil
	})

	message, err := session.InteractionResponseEdit(
		&Interaction{AppID: "application", Token: "token"},
		&WebhookEdit{Poll: &Poll{
			Question: PollMedia{Text: "Ready?"},
			Answers: []PollAnswer{
				{Media: &PollMedia{Text: "Yes"}},
				{Media: &PollMedia{Text: "No"}},
			},
			Duration: 1,
		}},
	)
	if err != nil {
		t.Fatalf("InteractionResponseEdit returned error: %v", err)
	}
	if message == nil || message.ID != "message" {
		t.Fatalf("message = %#v", message)
	}
}

func TestInteractionRespondWithResponseJSON(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	atomic.StoreInt64(session.Ratelimiter.global, time.Now().Add(time.Hour).UnixNano())
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/interactions/interaction/token/callback"
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, wantPath)
		}
		if r.URL.RawQuery != "with_response=true" {
			t.Fatalf("query = %q, want with_response=true", r.URL.RawQuery)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Fatalf("Authorization = %q, want empty", authorization)
		}

		var payload InteractionResponse
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if payload.Type != InteractionResponseLaunchActivity {
			t.Fatalf("response type = %d, want %d", payload.Type, InteractionResponseLaunchActivity)
		}

		body := `{"interaction":{"id":"interaction","type":2,"activity_instance_id":"activity"},"resource":{"type":12,"activity_instance":{"id":"activity"}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	callback, err := session.InteractionRespondWithResponse(
		&Interaction{ID: "interaction", Token: "token"},
		&InteractionResponse{Type: InteractionResponseLaunchActivity},
		WithContext(ctx),
	)
	if err != nil {
		t.Fatalf("InteractionRespondWithResponse returned error: %v", err)
	}
	if callback == nil || callback.Interaction == nil {
		t.Fatalf("callback = %#v", callback)
	}
	if callback.Interaction.ID != "interaction" || callback.Interaction.Type != InteractionApplicationCommand || callback.Interaction.ActivityInstanceID != "activity" {
		t.Fatalf("callback interaction = %#v", callback.Interaction)
	}
	if callback.Resource == nil || callback.Resource.Type != InteractionResponseLaunchActivity || callback.Resource.ActivityInstance == nil || callback.Resource.ActivityInstance.ID != "activity" {
		t.Fatalf("callback resource = %#v", callback.Resource)
	}
	if len(session.Ratelimiter.buckets) != 0 {
		t.Fatalf("bucket count = %d, want 0", len(session.Ratelimiter.buckets))
	}
}

func TestInteractionRespondWithResponseMultipart(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.AllowedMentions = &MessageAllowedMentions{Parse: []AllowedMentionType{}}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.RawQuery != "with_response=true" {
			t.Fatalf("query = %q, want with_response=true", r.URL.RawQuery)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(r.MultipartForm.Value["payload_json"][0]), &payload); err != nil {
			t.Fatalf("json.Unmarshal payload returned error: %v", err)
		}
		data, ok := payload["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("data = %#v, want object", payload["data"])
		}
		assertAllowedMentionsParse(t, data, []string{})

		file, header, err := r.FormFile("files[0]")
		if err != nil {
			t.Fatalf("FormFile returned error: %v", err)
		}
		defer file.Close()
		contents, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if header.Filename != "note.txt" || string(contents) != "attachment" {
			t.Fatalf("file = %q/%q", header.Filename, contents)
		}

		body := `{"interaction":{"id":"interaction","type":2,"response_message_id":"message","response_message_loading":false,"response_message_ephemeral":true},"resource":{"type":4,"message":{"id":"message","channel_id":"channel","content":"uploaded","components":[{"type":10,"id":1,"content":"done"}]}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	callback, err := session.InteractionRespondWithResponse(
		&Interaction{ID: "interaction", Token: "token"},
		&InteractionResponse{
			Type: InteractionResponseChannelMessageWithSource,
			Data: &InteractionResponseData{
				Content: "uploaded",
				Files: []*File{{
					Name:        "note.txt",
					ContentType: "text/plain",
					Reader:      strings.NewReader("attachment"),
				}},
			},
		},
	)
	if err != nil {
		t.Fatalf("InteractionRespondWithResponse returned error: %v", err)
	}
	if callback == nil || callback.Interaction == nil || callback.Resource == nil || callback.Resource.Message == nil {
		t.Fatalf("callback = %#v", callback)
	}
	if callback.Interaction.ResponseMessageID != "message" || callback.Interaction.ResponseMessageLoading == nil || *callback.Interaction.ResponseMessageLoading {
		t.Fatalf("callback interaction = %#v", callback.Interaction)
	}
	if callback.Interaction.ResponseMessageEphemeral == nil || !*callback.Interaction.ResponseMessageEphemeral {
		t.Fatalf("ResponseMessageEphemeral = %#v", callback.Interaction.ResponseMessageEphemeral)
	}
	if callback.Resource.Message.ID != "message" || callback.Resource.Message.Content != "uploaded" || len(callback.Resource.Message.Components) != 1 {
		t.Fatalf("callback message = %#v", callback.Resource.Message)
	}
}

func TestInteractionRespondWithResponseErrors(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	tests := []struct {
		name string
		call func() (*InteractionCallbackResponse, error)
		want string
	}{
		{
			name: "nil interaction",
			call: func() (*InteractionCallbackResponse, error) {
				return session.InteractionRespondWithResponse(nil, &InteractionResponse{Type: InteractionResponsePong})
			},
			want: "interaction cannot be nil",
		},
		{
			name: "nil response",
			call: func() (*InteractionCallbackResponse, error) {
				return session.InteractionRespondWithResponse(&Interaction{ID: "interaction", Token: "token"}, nil)
			},
			want: "interaction response cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callback, err := tt.call()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if callback != nil {
				t.Fatalf("callback = %#v, want nil", callback)
			}
		})
	}

	t.Run("malformed response", func(t *testing.T) {
		session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{`)),
				Request:    r,
			}, nil
		})
		callback, err := session.InteractionRespondWithResponse(
			&Interaction{ID: "interaction", Token: "token"},
			&InteractionResponse{Type: InteractionResponsePong},
		)
		if !errors.Is(err, ErrJSONUnmarshal) {
			t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
		}
		if callback != nil {
			t.Fatalf("callback = %#v, want nil", callback)
		}
	})

	t.Run("http error", func(t *testing.T) {
		session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Body:       io.NopCloser(strings.NewReader(`{"code":50035,"message":"Invalid Form Body"}`)),
				Request:    r,
			}, nil
		})
		callback, err := session.InteractionRespondWithResponse(
			&Interaction{ID: "interaction", Token: "token"},
			&InteractionResponse{Type: InteractionResponsePong},
		)
		var restErr *RESTError
		if !errors.As(err, &restErr) || restErr.Response.StatusCode != http.StatusBadRequest {
			t.Fatalf("error = %T %v, want 400 RESTError", err, err)
		}
		if callback != nil {
			t.Fatalf("callback = %#v, want nil", callback)
		}
	})
}

func TestInteractionRespondPreservesNoResponseQuery(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.RawQuery != "" {
			t.Fatalf("query = %q, want empty", r.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Status:     "204 No Content",
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	if err := session.InteractionRespond(
		&Interaction{ID: "interaction", Token: "token"},
		&InteractionResponse{Type: InteractionResponsePong},
	); err != nil {
		t.Fatalf("InteractionRespond returned error: %v", err)
	}
}

func TestSessionAllowedMentionsAppliedToRESTPayloads(t *testing.T) {
	content := "@everyone hello"

	tests := []struct {
		name    string
		call    func(*Session) error
		payload func(map[string]interface{}) map[string]interface{}
	}{
		{
			name: "channel message send",
			call: func(s *Session) error {
				_, err := s.ChannelMessageSendComplex("channel", &MessageSend{Content: content})
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} { return body },
		},
		{
			name: "channel message edit",
			call: func(s *Session) error {
				_, err := s.ChannelMessageEditComplex(NewMessageEdit("channel", "message").SetContent(content))
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} { return body },
		},
		{
			name: "forum thread start",
			call: func(s *Session) error {
				_, err := s.ForumThreadStartComplex("forum", &ThreadStart{Name: "post", AutoArchiveDuration: 60}, &MessageSend{Content: content})
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} {
				message, ok := body["message"].(map[string]interface{})
				if !ok {
					t.Fatalf("message payload = %#v, want object", body["message"])
				}
				return message
			},
		},
		{
			name: "webhook execute",
			call: func(s *Session) error {
				_, err := s.WebhookExecute("webhook", "token", true, &WebhookParams{Content: content})
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} { return body },
		},
		{
			name: "webhook message edit",
			call: func(s *Session) error {
				_, err := s.WebhookMessageEdit("webhook", "token", "message", &WebhookEdit{Content: &content})
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} { return body },
		},
		{
			name: "interaction response",
			call: func(s *Session) error {
				return s.InteractionRespond(&Interaction{ID: "interaction", Token: "token"}, &InteractionResponse{
					Type: InteractionResponseChannelMessageWithSource,
					Data: &InteractionResponseData{Content: content},
				})
			},
			payload: func(body map[string]interface{}) map[string]interface{} {
				data, ok := body["data"].(map[string]interface{})
				if !ok {
					t.Fatalf("data payload = %#v, want object", body["data"])
				}
				return data
			},
		},
		{
			name: "followup create",
			call: func(s *Session) error {
				_, err := s.FollowupMessageCreate(&Interaction{AppID: "application", Token: "token"}, true, &WebhookParams{Content: content})
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} { return body },
		},
		{
			name: "followup edit",
			call: func(s *Session) error {
				_, err := s.FollowupMessageEdit(&Interaction{AppID: "application", Token: "token"}, "message", &WebhookEdit{Content: &content})
				return err
			},
			payload: func(body map[string]interface{}) map[string]interface{} { return body },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.AllowedMentions = &MessageAllowedMentions{Parse: []AllowedMentionType{}}

			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("ReadAll returned error: %v", err)
				}

				var payload map[string]interface{}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("request body is not JSON: %v\n%s", err, body)
				}
				assertAllowedMentionsParse(t, tt.payload(payload), []string{})

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"channel"}`)),
					Request:    r,
				}, nil
			})

			if err := tt.call(session); err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			if !called {
				t.Fatal("HTTP transport was not called")
			}
		})
	}
}

func TestChannelMessageEditFlags(t *testing.T) {
	tests := []struct {
		name        string
		flags       MessageFlags
		flagsSet    bool
		wantFlags   string
		wantPresent bool
	}{
		{name: "omitted"},
		{
			name:        "nonzero",
			flags:       MessageFlagsSuppressEmbeds,
			wantFlags:   "4",
			wantPresent: true,
		},
		{
			name:        "explicit zero",
			flagsSet:    true,
			wantFlags:   "0",
			wantPresent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPatch {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
				}
				wantPath := "/api/v" + APIVersion + "/channels/channel/messages/message"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
				}

				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("Decode returned error: %v", err)
				}
				flags, present := payload["flags"]
				if present != test.wantPresent {
					t.Fatalf("flags present = %t, want %t", present, test.wantPresent)
				}
				if present && string(flags) != test.wantFlags {
					t.Fatalf("flags = %s, want %s", flags, test.wantFlags)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"channel"}`)),
					Request:    r,
				}, nil
			})

			message, err := session.ChannelMessageEditComplex(&MessageEdit{
				ID:       "message",
				Channel:  "channel",
				Flags:    test.flags,
				FlagsSet: test.flagsSet,
			})
			if err != nil {
				t.Fatalf("ChannelMessageEditComplex returned error: %v", err)
			}
			if message == nil || message.ID != "message" {
				t.Fatalf("message = %#v", message)
			}
		})
	}
}

func TestSessionAllowedMentionsDoesNotOverridePayload(t *testing.T) {
	content := "@everyone hello"

	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.AllowedMentions = &MessageAllowedMentions{Parse: []AllowedMentionType{}}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("request body is not JSON: %v\n%s", err, body)
		}
		assertAllowedMentionsParse(t, payload, []string{"users"})

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"channel"}`)),
			Request:    r,
		}, nil
	})

	_, err = session.ChannelMessageSendComplex("channel", &MessageSend{
		Content: content,
		AllowedMentions: &MessageAllowedMentions{
			Parse: []AllowedMentionType{AllowedMentionTypeUsers},
		},
	})
	if err != nil {
		t.Fatalf("ChannelMessageSendComplex returned error: %v", err)
	}
}

func assertAllowedMentionsParse(t *testing.T, payload map[string]interface{}, want []string) {
	t.Helper()

	allowedMentions, ok := payload["allowed_mentions"].(map[string]interface{})
	if !ok {
		t.Fatalf("allowed_mentions = %#v, want object", payload["allowed_mentions"])
	}

	parse, ok := allowedMentions["parse"].([]interface{})
	if !ok {
		t.Fatalf("allowed_mentions.parse = %#v, want array", allowedMentions["parse"])
	}
	if len(parse) != len(want) {
		t.Fatalf("len(allowed_mentions.parse) = %d, want %d", len(parse), len(want))
	}
	for i := range want {
		got, ok := parse[i].(string)
		if !ok {
			t.Fatalf("allowed_mentions.parse[%d] = %#v, want string", i, parse[i])
		}
		if got != want[i] {
			t.Fatalf("allowed_mentions.parse[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestNilRESTPayloadsReturnErrors(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session) error
		want string
	}{
		{
			name: "channel message send",
			call: func(s *Session) error {
				_, err := s.ChannelMessageSendComplex("channel", nil)
				return err
			},
			want: "message send data cannot be nil",
		},
		{
			name: "channel message send embed",
			call: func(s *Session) error {
				_, err := s.ChannelMessageSendEmbed("channel", nil)
				return err
			},
			want: "message embed cannot be nil",
		},
		{
			name: "channel message edit",
			call: func(s *Session) error {
				_, err := s.ChannelMessageEditComplex(nil)
				return err
			},
			want: "message edit data cannot be nil",
		},
		{
			name: "channel message edit embed",
			call: func(s *Session) error {
				_, err := s.ChannelMessageEditEmbeds("channel", "message", []*MessageEmbed{nil})
				return err
			},
			want: "message embed cannot be nil",
		},
		{
			name: "role edit",
			call: func(s *Session) error {
				_, err := s.GuildRoleEdit("guild", "role", nil)
				return err
			},
			want: "role data cannot be nil",
		},
		{
			name: "guild incident actions",
			call: func(s *Session) error {
				_, err := s.GuildIncidentActionsEdit("guild", nil)
				return err
			},
			want: "guild incident actions data cannot be nil",
		},
		{
			name: "guild welcome screen",
			call: func(s *Session) error {
				_, err := s.GuildWelcomeScreenEdit("guild", nil)
				return err
			},
			want: "guild welcome screen data cannot be nil",
		},
		{
			name: "webhook execute",
			call: func(s *Session) error {
				_, err := s.WebhookExecute("webhook", "token", false, nil)
				return err
			},
			want: "webhook data cannot be nil",
		},
		{
			name: "webhook message edit",
			call: func(s *Session) error {
				_, err := s.WebhookMessageEdit("webhook", "token", "message", nil)
				return err
			},
			want: "webhook edit data cannot be nil",
		},
		{
			name: "interaction response",
			call: func(s *Session) error {
				return s.InteractionRespond(&Interaction{ID: "interaction", Token: "token"}, nil)
			},
			want: "interaction response cannot be nil",
		},
		{
			name: "interaction",
			call: func(s *Session) error {
				return s.InteractionRespond(nil, &InteractionResponse{Type: InteractionResponsePong})
			},
			want: "interaction cannot be nil",
		},
		{
			name: "interaction response get",
			call: func(s *Session) error {
				_, err := s.InteractionResponse(nil)
				return err
			},
			want: "interaction cannot be nil",
		},
		{
			name: "interaction response edit interaction",
			call: func(s *Session) error {
				_, err := s.InteractionResponseEdit(nil, &WebhookEdit{})
				return err
			},
			want: "interaction cannot be nil",
		},
		{
			name: "interaction response edit data",
			call: func(s *Session) error {
				_, err := s.InteractionResponseEdit(&Interaction{AppID: "application", Token: "token"}, nil)
				return err
			},
			want: "webhook edit data cannot be nil",
		},
		{
			name: "interaction response delete",
			call: func(s *Session) error {
				return s.InteractionResponseDelete(nil)
			},
			want: "interaction cannot be nil",
		},
		{
			name: "followup message create interaction",
			call: func(s *Session) error {
				_, err := s.FollowupMessageCreate(nil, false, &WebhookParams{})
				return err
			},
			want: "interaction cannot be nil",
		},
		{
			name: "followup message create",
			call: func(s *Session) error {
				_, err := s.FollowupMessageCreate(&Interaction{AppID: "application", Token: "token"}, false, nil)
				return err
			},
			want: "webhook data cannot be nil",
		},
		{
			name: "followup message edit interaction",
			call: func(s *Session) error {
				_, err := s.FollowupMessageEdit(nil, "message", &WebhookEdit{})
				return err
			},
			want: "interaction cannot be nil",
		},
		{
			name: "followup message edit",
			call: func(s *Session) error {
				_, err := s.FollowupMessageEdit(&Interaction{AppID: "application", Token: "token"}, "message", nil)
				return err
			},
			want: "webhook edit data cannot be nil",
		},
		{
			name: "followup message delete",
			call: func(s *Session) error {
				return s.FollowupMessageDelete(nil, "message")
			},
			want: "interaction cannot be nil",
		},
		{
			name: "forum thread message",
			call: func(s *Session) error {
				_, err := s.ForumThreadStartComplex("channel", &ThreadStart{Name: "thread"}, nil)
				return err
			},
			want: "message send data cannot be nil",
		},
		{
			name: "forum thread message embed",
			call: func(s *Session) error {
				_, err := s.ForumThreadStartEmbeds("channel", "thread", 60, []*MessageEmbed{nil})
				return err
			},
			want: "message embed cannot be nil",
		},
		{
			name: "forum thread data",
			call: func(s *Session) error {
				_, err := s.ForumThreadStartComplex("channel", nil, &MessageSend{Content: "hello"})
				return err
			},
			want: "thread data cannot be nil",
		},
		{
			name: "application command create",
			call: func(s *Session) error {
				_, err := s.ApplicationCommandCreate("application", "guild", nil)
				return err
			},
			want: "application command data cannot be nil",
		},
		{
			name: "application command edit",
			call: func(s *Session) error {
				_, err := s.ApplicationCommandEdit("application", "guild", "command", nil)
				return err
			},
			want: "application command data cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				t.Fatalf("HTTP transport was called for nil input")
				return nil, nil
			})

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s panicked: %v", tt.name, r)
				}
			}()

			err = tt.call(session)
			if err == nil {
				t.Fatalf("%s returned nil error", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("%s error = %q, want %q", tt.name, err.Error(), tt.want)
			}
		})
	}
}

func TestAuthenticatedWebhookEndpointKeepsAuthorization(t *testing.T) {
	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	called := false
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if got := r.Header.Get("Authorization"); got != "Bot secret" {
			t.Fatalf("Authorization header = %q, want Bot secret", got)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"webhook","type":1}`)),
			Request:    r,
		}, nil
	})

	if _, err := session.Webhook("webhook"); err != nil {
		t.Fatalf("Webhook returned error: %v", err)
	}
	if !called {
		t.Fatal("HTTP transport was not called")
	}
}

func TestPermissionAllIncludesCurrentPermissionFlags(t *testing.T) {
	tests := []struct {
		name       string
		permission int64
	}{
		{"CreateInstantInvite", PermissionCreateInstantInvite},
		{"KickMembers", PermissionKickMembers},
		{"BanMembers", PermissionBanMembers},
		{"Administrator", PermissionAdministrator},
		{"ManageChannels", PermissionManageChannels},
		{"ManageGuild", PermissionManageGuild},
		{"AddReactions", PermissionAddReactions},
		{"ViewAuditLogs", PermissionViewAuditLogs},
		{"VoicePrioritySpeaker", PermissionVoicePrioritySpeaker},
		{"VoiceStreamVideo", PermissionVoiceStreamVideo},
		{"ViewChannel", PermissionViewChannel},
		{"SendMessages", PermissionSendMessages},
		{"SendTTSMessages", PermissionSendTTSMessages},
		{"ManageMessages", PermissionManageMessages},
		{"EmbedLinks", PermissionEmbedLinks},
		{"AttachFiles", PermissionAttachFiles},
		{"ReadMessageHistory", PermissionReadMessageHistory},
		{"MentionEveryone", PermissionMentionEveryone},
		{"UseExternalEmojis", PermissionUseExternalEmojis},
		{"ViewGuildInsights", PermissionViewGuildInsights},
		{"VoiceConnect", PermissionVoiceConnect},
		{"VoiceSpeak", PermissionVoiceSpeak},
		{"VoiceMuteMembers", PermissionVoiceMuteMembers},
		{"VoiceDeafenMembers", PermissionVoiceDeafenMembers},
		{"VoiceMoveMembers", PermissionVoiceMoveMembers},
		{"VoiceUseVAD", PermissionVoiceUseVAD},
		{"ChangeNickname", PermissionChangeNickname},
		{"ManageNicknames", PermissionManageNicknames},
		{"ManageRoles", PermissionManageRoles},
		{"ManageWebhooks", PermissionManageWebhooks},
		{"ManageGuildExpressions", PermissionManageGuildExpressions},
		{"UseApplicationCommands", PermissionUseApplicationCommands},
		{"VoiceRequestToSpeak", PermissionVoiceRequestToSpeak},
		{"ManageEvents", PermissionManageEvents},
		{"ManageThreads", PermissionManageThreads},
		{"CreatePublicThreads", PermissionCreatePublicThreads},
		{"CreatePrivateThreads", PermissionCreatePrivateThreads},
		{"UseExternalStickers", PermissionUseExternalStickers},
		{"SendMessagesInThreads", PermissionSendMessagesInThreads},
		{"UseEmbeddedActivities", PermissionUseEmbeddedActivities},
		{"ModerateMembers", PermissionModerateMembers},
		{"ViewCreatorMonetizationAnalytics", PermissionViewCreatorMonetizationAnalytics},
		{"UseSoundboard", PermissionUseSoundboard},
		{"CreateGuildExpressions", PermissionCreateGuildExpressions},
		{"CreateEvents", PermissionCreateEvents},
		{"UseExternalSounds", PermissionUseExternalSounds},
		{"SendVoiceMessages", PermissionSendVoiceMessages},
		{"SetVoiceChannelStatus", PermissionSetVoiceChannelStatus},
		{"SendPolls", PermissionSendPolls},
		{"UseExternalApps", PermissionUseExternalApps},
		{"PinMessages", PermissionPinMessages},
		{"BypassSlowmode", PermissionBypassSlowmode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if PermissionAll&tt.permission != tt.permission {
				t.Fatalf("PermissionAll missing %s (%d)", tt.name, tt.permission)
			}
		})
	}
}

func TestPermissionAllChannelIncludesCurrentChannelPermissions(t *testing.T) {
	tests := []struct {
		name       string
		permission int64
	}{
		{"UseExternalEmojis", PermissionUseExternalEmojis},
		{"UseApplicationCommands", PermissionUseApplicationCommands},
		{"ManageEvents", PermissionManageEvents},
		{"ManageThreads", PermissionManageThreads},
		{"CreatePublicThreads", PermissionCreatePublicThreads},
		{"CreatePrivateThreads", PermissionCreatePrivateThreads},
		{"UseExternalStickers", PermissionUseExternalStickers},
		{"SendMessagesInThreads", PermissionSendMessagesInThreads},
		{"UseEmbeddedActivities", PermissionUseEmbeddedActivities},
		{"UseSoundboard", PermissionUseSoundboard},
		{"CreateEvents", PermissionCreateEvents},
		{"UseExternalSounds", PermissionUseExternalSounds},
		{"SendVoiceMessages", PermissionSendVoiceMessages},
		{"SetVoiceChannelStatus", PermissionSetVoiceChannelStatus},
		{"SendPolls", PermissionSendPolls},
		{"UseExternalApps", PermissionUseExternalApps},
		{"PinMessages", PermissionPinMessages},
		{"BypassSlowmode", PermissionBypassSlowmode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if PermissionAllChannel&tt.permission != tt.permission {
				t.Fatalf("PermissionAllChannel missing %s (%d)", tt.name, tt.permission)
			}
		})
	}
}

func TestMemberPermissionsAdministratorIncludesCurrentChannelPermissions(t *testing.T) {
	var currentChannelPermissions int64 = PermissionUseExternalEmojis |
		PermissionUseApplicationCommands |
		PermissionManageEvents |
		PermissionManageThreads |
		PermissionCreatePublicThreads |
		PermissionCreatePrivateThreads |
		PermissionUseExternalStickers |
		PermissionSendMessagesInThreads |
		PermissionUseEmbeddedActivities |
		PermissionUseSoundboard |
		PermissionCreateEvents |
		PermissionUseExternalSounds |
		PermissionSendVoiceMessages |
		PermissionSetVoiceChannelStatus |
		PermissionSendPolls |
		PermissionUseExternalApps |
		PermissionPinMessages |
		PermissionBypassSlowmode

	guild := &Guild{
		ID:      "guild",
		OwnerID: "owner",
		Roles: []*Role{
			{ID: "guild"},
			{ID: "admin", Permissions: PermissionAdministrator},
		},
	}
	channel := &Channel{
		GuildID: "guild",
		PermissionOverwrites: []*PermissionOverwrite{
			{
				ID:   "guild",
				Type: PermissionOverwriteTypeRole,
				Deny: currentChannelPermissions,
			},
		},
	}

	permissions := memberPermissions(guild, channel, "member", []string{"admin"})
	if permissions&currentChannelPermissions != currentChannelPermissions {
		t.Fatalf("administrator permissions missing current channel flags: got %d, want %d", permissions&currentChannelPermissions, currentChannelPermissions)
	}
}

func TestMemberPermissionsAdministratorBypassesChannelOverwrites(t *testing.T) {
	guild := &Guild{
		ID:      "guild",
		OwnerID: "owner",
		Roles: []*Role{
			{ID: "guild"},
			{ID: "admin", Permissions: PermissionAdministrator},
		},
	}
	channel := &Channel{
		GuildID: "guild",
		PermissionOverwrites: []*PermissionOverwrite{
			{
				ID:   "guild",
				Type: PermissionOverwriteTypeRole,
				Deny: PermissionAdministrator | PermissionViewChannel | PermissionSendMessages,
			},
			{
				ID:   "member",
				Type: PermissionOverwriteTypeMember,
				Deny: PermissionManageChannels,
			},
		},
	}

	permissions := memberPermissions(guild, channel, "member", []string{"admin"})
	if permissions&PermissionAdministrator != PermissionAdministrator {
		t.Fatalf("administrator bit was removed by overwrites: got %d", permissions)
	}
	if permissions&PermissionAllChannel != PermissionAllChannel {
		t.Fatalf("administrator channel permissions missing: got %d, want all channel permissions", permissions)
	}
}

func TestMemberPermissionsSkipsNilRoles(t *testing.T) {
	guild := &Guild{
		ID: "guild",
		Roles: []*Role{
			nil,
			{ID: "guild", Permissions: PermissionViewChannel},
			{ID: "member", Permissions: PermissionSendMessages},
		},
	}
	channel := &Channel{GuildID: "guild"}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("memberPermissions panicked: %v", r)
		}
	}()
	permissions := memberPermissions(guild, channel, "user", []string{"member"})
	want := int64(PermissionViewChannel | PermissionSendMessages)
	if permissions&want != want {
		t.Fatalf("permissions = %d, want %d", permissions, want)
	}
}

func TestMemberPermissionsSkipsNilOverwrites(t *testing.T) {
	guild := &Guild{
		ID: "guild",
		Roles: []*Role{
			{ID: "guild", Permissions: PermissionViewChannel | PermissionSendMessages},
			{ID: "member"},
		},
	}
	channel := &Channel{
		GuildID: "guild",
		PermissionOverwrites: []*PermissionOverwrite{
			nil,
			{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionSendMessages, Allow: PermissionReadMessageHistory},
			nil,
			{ID: "member", Type: PermissionOverwriteTypeRole, Deny: PermissionViewChannel, Allow: PermissionSendMessages},
			nil,
			{ID: "user", Type: PermissionOverwriteTypeMember, Deny: PermissionReadMessageHistory, Allow: PermissionViewChannel},
		},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("memberPermissions panicked: %v", r)
		}
	}()
	permissions := memberPermissions(guild, channel, "user", []string{"member"})
	want := int64(PermissionViewChannel | PermissionSendMessages)
	if permissions != want {
		t.Fatalf("permissions = %d, want %d", permissions, want)
	}
}

func TestGateway(t *testing.T) {

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}
	_, err := dg.Gateway()
	if err != nil {
		t.Errorf("Gateway() returned error: %+v", err)
	}
}

func TestGatewayBot(t *testing.T) {

	if dgBot == nil {
		t.Skip("Skipping, dgBot not set.")
	}
	_, err := dgBot.GatewayBot()
	if err != nil {
		t.Errorf("GatewayBot() returned error: %+v", err)
	}
}

func TestVoiceRegions(t *testing.T) {

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.VoiceRegions()
	if err != nil {
		t.Errorf("VoiceRegions() returned error: %+v", err)
	}
}

func TestVoiceRegionsEndpointPath(t *testing.T) {
	want := EndpointAPI + "voice/regions"
	if EndpointVoiceRegions != want {
		t.Fatalf("EndpointVoiceRegions = %q, want %q", EndpointVoiceRegions, want)
	}
}

func TestGuildRoles(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.GuildRoles(envGuild)
	if err != nil {
		t.Errorf("GuildRoles(envGuild) returned error: %+v", err)
	}

}

func TestGuildMemberNickname(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	err := dg.GuildMemberNickname(envGuild, "@me/nick", "B1nzyRocks")
	if err != nil {
		t.Errorf("GuildNickname returned error: %+v", err)
	}
}

// TestChannelMessageSend2 tests the ChannelMessageSend() function. This should not return an error.
func TestChannelMessageSend2(t *testing.T) {

	if envChannel == "" {
		t.Skip("Skipping, DG_CHANNEL not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.ChannelMessageSend(envChannel, "All done running REST API Tests!")
	if err != nil {
		t.Errorf("ChannelMessageSend returned error: %+v", err)
	}
}

// TestGuildPruneCount tests GuildPruneCount() function. This should not return an error.
func TestGuildPruneCount(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.GuildPruneCount(envGuild, 1)
	if err != nil {
		t.Errorf("GuildPruneCount returned error: %+v", err)
	}
}

/*
// TestGuildPrune tests GuildPrune() function. This should not return an error.
func TestGuildPrune(t *testing.T) {

	if envGuild == "" {
		t.Skip("Skipping, DG_GUILD not set.")
	}

	if dg == nil {
		t.Skip("Skipping, dg not set.")
	}

	_, err := dg.GuildPrune(envGuild, 1)
	if err != nil {
		t.Errorf("GuildPrune returned error: %+v", err)
	}
}
*/

func Test_unmarshal(t *testing.T) {
	err := unmarshal([]byte{}, &struct{}{})
	if !errors.Is(err, ErrJSONUnmarshal) {
		t.Errorf("Unexpected error type: %T", err)
	}
}

func TestWithContext(t *testing.T) {
	// Set up a test context.
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "value")

	// Set up a test client.
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	testErr := errors.New("test")

	// Intercept the request to assert the context.
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		val, _ := r.Context().Value(key{}).(string)
		if val != "value" {
			t.Errorf("missing value in context (got %q, wanted %q)", val, "value")
		}
		return nil, testErr
	})

	// Run any client method using WithContext.
	_, err = session.User("", WithContext(ctx))

	// Verify that the assertion code was actually run.
	if !errors.Is(err, testErr) {
		t.Errorf("unexpected error %v returned from client", err)
	}
}

func TestRequestWithLockedBucketNonJSONRateLimitUsesRetryAfter(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header: http.Header{
				"Retry-After": []string{"2"},
			},
			Body:    io.NopCloser(strings.NewReader("error code: 1015")),
			Request: r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if rateLimitErr.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %v, want %v", rateLimitErr.RetryAfter, 2*time.Second)
	}
	if rateLimitErr.Message != "error code: 1015" {
		t.Fatalf("Message = %q, want %q", rateLimitErr.Message, "error code: 1015")
	}
}

func TestRequestWithLockedBucketJSONRateLimitUsesRetryAfterHeaderFallback(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header: http.Header{
				"Retry-After":       []string{"2"},
				"X-RateLimit-Scope": []string{"global"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":0,"global":true}`)),
			Request: r,
		}, nil
	})

	before := time.Now()
	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if rateLimitErr.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %v, want %v", rateLimitErr.RetryAfter, 2*time.Second)
	}

	reset := time.Unix(0, atomic.LoadInt64(session.Ratelimiter.global))
	if !reset.After(before) {
		t.Fatalf("global reset = %v, want after %v", reset, before)
	}
}

func TestRequestWithLockedBucketRateLimitWithoutRetryAfterDoesNotRetry(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	requests := int32(0)
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":0}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway)
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestRateLimitErrorRedactsWebhookToken(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":1}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointWebhookToken("webhook", "secret-token"), nil, webhookTokenBucketID("webhook"), WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if strings.Contains(rateLimitErr.URL, "secret-token") {
		t.Fatalf("RateLimit URL leaked webhook token: %q", rateLimitErr.URL)
	}
	if !strings.Contains(rateLimitErr.URL, redactedURLValue) {
		t.Fatalf("RateLimit URL = %q, want redacted token", rateLimitErr.URL)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("RateLimitError leaked webhook token: %q", err.Error())
	}
}

func TestRequestTransportErrorRedactsTokenURL(t *testing.T) {
	testErr := errors.New("network down")

	tests := []struct {
		name  string
		token string
		call  func(*Session) error
	}{
		{
			name:  "webhook token",
			token: "secret-token",
			call: func(session *Session) error {
				_, err := session.WebhookExecute("webhook", "secret-token", false, &WebhookParams{Content: "hello"})
				return err
			},
		},
		{
			name:  "interaction token",
			token: "interaction-token",
			call: func(session *Session) error {
				return session.InteractionRespond(&Interaction{ID: "interaction", Token: "interaction-token"}, &InteractionResponse{Type: InteractionResponsePong})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, testErr
			})

			err = tt.call(session)
			if !errors.Is(err, testErr) {
				t.Fatalf("error = %v, want wrapped network error", err)
			}
			var urlErr *url.Error
			if !errors.As(err, &urlErr) {
				t.Fatalf("error = %T %[1]v, want *url.Error", err)
			}
			if strings.Contains(err.Error(), tt.token) {
				t.Fatalf("transport error leaked token: %q", err.Error())
			}
			if strings.Contains(urlErr.URL, tt.token) {
				t.Fatalf("transport error URL leaked token: %q", urlErr.URL)
			}
			if !strings.Contains(urlErr.URL, redactedURLValue) {
				t.Fatalf("transport error URL = %q, want redacted token", urlErr.URL)
			}
		})
	}
}

func TestRequestConfigErrorRedactsInvalidTokenURL(t *testing.T) {
	tests := []struct {
		name  string
		token string
		call  func(*Session) error
	}{
		{
			name:  "webhook token",
			token: "secret%zz-token",
			call: func(session *Session) error {
				_, err := session.WebhookExecute("webhook", "secret%zz-token", false, &WebhookParams{Content: "hello"})
				return err
			},
		},
		{
			name:  "interaction token",
			token: "interaction%zz-token",
			call: func(session *Session) error {
				return session.InteractionRespond(&Interaction{ID: "interaction", Token: "interaction%zz-token"}, &InteractionResponse{Type: InteractionResponsePong})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot secret")
			if err != nil {
				t.Fatal(err)
			}

			err = tt.call(session)
			if err == nil {
				t.Fatal("error = nil, want invalid URL error")
			}
			var urlErr *url.Error
			if !errors.As(err, &urlErr) {
				t.Fatalf("error = %T %[1]v, want *url.Error", err)
			}
			if strings.Contains(err.Error(), tt.token) {
				t.Fatalf("request config error leaked token: %q", err.Error())
			}
			if strings.Contains(urlErr.URL, tt.token) {
				t.Fatalf("request config error URL leaked token: %q", urlErr.URL)
			}
			if !strings.Contains(urlErr.URL, redactedURLValue) {
				t.Fatalf("request config error URL = %q, want redacted token", urlErr.URL)
			}
		})
	}
}

func TestRateLimitEventRedactsWebhookToken(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true

	var gotURL string
	session.AddHandler(func(_ *Session, event *RateLimit) {
		gotURL = event.URL
	})

	requests := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":0.001}`)),
				Request:    r,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointWebhookToken("webhook", "secret-token"), nil, webhookTokenBucketID("webhook"))
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if strings.Contains(gotURL, "secret-token") {
		t.Fatalf("RateLimit event URL leaked webhook token: %q", gotURL)
	}
	if !strings.Contains(gotURL, redactedURLValue) {
		t.Fatalf("RateLimit event URL = %q, want redacted token", gotURL)
	}
}

func TestRequestRawBucketWaitRespectsContext(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		t.Fatal("HTTP transport should not be called after context deadline")
		return nil, nil
	})

	bucket := session.Ratelimiter.GetBucket(EndpointGateway)
	bucket.Lock()
	bucket.Remaining = 0
	bucket.reset = time.Now().Add(10 * time.Second)
	bucket.setReset(bucket.reset)
	bucket.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RequestWithBucketID() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RequestWithBucketID() slept for %v after context deadline", elapsed)
	}
	if called {
		t.Fatal("HTTP transport was called after context deadline")
	}
	if active := atomic.LoadInt32(&bucket.activeRequests); active != 0 {
		t.Fatalf("activeRequests = %d, want 0", active)
	}
}

func TestRequestWithLockedBucketClosesRateLimitBodyBeforeRetry(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	firstBody := &closeNotifyReadCloser{
		reader: strings.NewReader(`{"message":"rate limited","retry_after":0.001,"global":false}`),
		closed: make(chan struct{}),
	}
	attempts := 0

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       firstBody,
				Request:    r,
			}, nil
		}

		select {
		case <-firstBody.closed:
		case <-time.After(time.Second):
			t.Fatal("rate-limit response body was not closed before retry")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway)
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRequestWithLockedBucketRetriesUseBoundedStack(t *testing.T) {
	const retries = 100

	tests := []struct {
		name       string
		statusCode int
		status     string
		body       string
	}{
		{
			name:       "rate limit",
			statusCode: http.StatusTooManyRequests,
			status:     "429 Too Many Requests",
			body:       `{"message":"rate limited","retry_after":0.001,"global":false}`,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			status:     "500 Internal Server Error",
			body:       "server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}

			attempts := 0
			optionCalls := 0
			callers := make([]uintptr, 1024)
			minCallers := len(callers)
			maxCallers := 0
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				if got := r.Header.Get("X-Retry-Attempt"); got != strconv.Itoa(attempts) {
					t.Fatalf("X-Retry-Attempt = %q, want %q", got, strconv.Itoa(attempts))
				}
				requestBody, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("ReadAll request body returned error: %v", err)
				}
				if string(requestBody) != `{"name":"updated"}` {
					t.Fatalf("request body = %q, want %q", requestBody, `{"name":"updated"}`)
				}

				depth := runtime.Callers(0, callers)
				if depth == len(callers) {
					t.Fatal("retry stack exceeded measurement buffer")
				}
				if depth < minCallers {
					minCallers = depth
				}
				if depth > maxCallers {
					maxCallers = depth
				}

				if attempts <= retries {
					return &http.Response{
						StatusCode: tt.statusCode,
						Status:     tt.status,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(tt.body)),
						Request:    r,
					}, nil
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Request:    r,
				}, nil
			})

			setAttemptHeader := func(cfg *RequestConfig) {
				optionCalls++
				cfg.Request.Header.Set("X-Retry-Attempt", strconv.Itoa(optionCalls))
			}
			_, err = session.RequestWithBucketID("PATCH", EndpointGateway, map[string]string{"name": "updated"}, EndpointGateway, WithRestRetries(retries), setAttemptHeader)
			if err != nil {
				t.Fatalf("RequestWithBucketID() returned error: %v", err)
			}
			if attempts != retries+1 {
				t.Fatalf("attempts = %d, want %d", attempts, retries+1)
			}
			if optionCalls != attempts {
				t.Fatalf("request option calls = %d, want %d", optionCalls, attempts)
			}
			// Recursive retries add frames on every attempt. Allow a small
			// amount of runtime variation while requiring bounded call depth.
			if growth := maxCallers - minCallers; growth > 10 {
				t.Fatalf("retry stack grew by %d frames, want at most 10", growth)
			}
		})
	}
}

func TestRequestWithLockedBucketRateLimitRetryRespectsContext(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":1,"global":false}`)),
			Request:    r,
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RequestWithBucketID() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RequestWithBucketID() slept for %v after context deadline", elapsed)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	bucket := session.Ratelimiter.GetBucket(EndpointGateway)
	if active := atomic.LoadInt32(&bucket.activeRequests); active != 0 {
		t.Fatalf("activeRequests = %d, want 0", active)
	}
}

func TestRequestWithLockedBucketRetriesGetServerError(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`server error`)),
				Request:    r,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway)
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRequestWithLockedBucketRetriesPatchServerError(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`server error`)),
				Request:    r,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("PATCH", EndpointGateway, map[string]string{"name": "updated"}, EndpointGateway)
	if err != nil {
		t.Fatalf("RequestWithBucketID() returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRequestWithLockedBucketDoesNotRetryPostServerError(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	bodyClosed := make(chan struct{})
	body := &closeNotifyReadCloser{
		reader:   strings.NewReader(`server error`),
		closed:   bodyClosed,
		retained: make([]byte, 8<<20),
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     http.Header{},
			Body:       body,
			Request:    r,
		}, nil
	})

	_, err = session.RequestWithBucketID("POST", EndpointChannelMessages("channel"), map[string]string{
		"content": "hello",
	}, EndpointChannelMessages("channel"))
	if err == nil {
		t.Fatal("RequestWithBucketID() returned nil error, want RESTError")
	}
	var restErr *RESTError
	if !errors.As(err, &restErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RESTError", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	select {
	case <-bodyClosed:
	default:
		t.Fatal("response body was not closed before returning RESTError")
	}
	if restErr.Response.Body != http.NoBody {
		t.Fatalf("RESTError retained consumed response body %T", restErr.Response.Body)
	}
}

func TestRequestWithLockedBucketGlobalRateLimitSetsGlobalReset(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header: http.Header{
				"Retry-After":       []string{"1"},
				"X-RateLimit-Scope": []string{"global"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"message":"You are being rate limited.","retry_after":1,"global":true}`)),
			Request: r,
		}, nil
	})

	before := time.Now()
	_, err = session.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, WithRetryOnRatelimit(false))
	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RateLimitError", err)
	}
	if !rateLimitErr.Global {
		t.Fatal("RateLimitError.Global = false, want true")
	}

	reset := time.Unix(0, atomic.LoadInt64(session.Ratelimiter.global))
	if !reset.After(before) {
		t.Fatalf("global reset = %v, want after %v", reset, before)
	}
}

func TestChannelMessagesPinnedUsesPerChannelBucket(t *testing.T) {
	session, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header: http.Header{
				"X-RateLimit-Remaining":   []string{"1"},
				"X-RateLimit-Reset-After": []string{"0.1"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"items":[],"has_more":false}`)),
			Request: r,
		}, nil
	})

	before := time.Now()
	if _, err = session.ChannelMessagesPinned("channel-a", &before, 10); err != nil {
		t.Fatalf("ChannelMessagesPinned returned error: %v", err)
	}
	if _, err = session.ChannelMessagesPinned("channel-b", nil, 0); err != nil {
		t.Fatalf("ChannelMessagesPinned returned error: %v", err)
	}

	session.Ratelimiter.Lock()
	defer session.Ratelimiter.Unlock()

	for _, want := range []string{
		EndpointChannelMessagesPins("channel-a"),
		EndpointChannelMessagesPins("channel-b"),
	} {
		if _, ok := session.Ratelimiter.buckets[want]; !ok {
			t.Fatalf("bucket %q was not created", want)
		}
	}
	if len(session.Ratelimiter.buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2", len(session.Ratelimiter.buckets))
	}
	for key := range session.Ratelimiter.buckets {
		if strings.Contains(key, "?") {
			t.Fatalf("bucket %q includes query parameters", key)
		}
	}
}

func TestMessageReactionsByType(t *testing.T) {
	tests := []struct {
		name          string
		wantQueryType string
		call          func(*Session) ([]*User, error)
	}{
		{
			name:          "legacy method fetches normal reactions",
			wantQueryType: "0",
			call: func(s *Session) ([]*User, error) {
				return s.MessageReactions("channel", "message", "wave", 25, "", "after")
			},
		},
		{
			name:          "type method fetches burst reactions",
			wantQueryType: "1",
			call: func(s *Session) ([]*User, error) {
				return s.MessageReactionsByType("channel", "message", "wave", ReactionTypeBurst, 25, "", "after")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
				}
				wantPath := "/api/v" + APIVersion + "/channels/channel/messages/message/reactions/wave"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q", r.URL.Path)
				}
				query := r.URL.Query()
				if query.Get("type") != tt.wantQueryType {
					t.Fatalf("type = %q, want %q", query.Get("type"), tt.wantQueryType)
				}
				if query.Get("limit") != "25" || query.Get("after") != "after" {
					t.Fatalf("query = %q", r.URL.RawQuery)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`[{"id":"user","username":"User"}]`)),
					Request:    r,
				}, nil
			})

			users, err := tt.call(session)
			if err != nil {
				t.Fatalf("reaction fetch returned error: %v", err)
			}
			if len(users) != 1 || users[0].ID != "user" {
				t.Fatalf("users = %#v", users)
			}
		})
	}
}

func TestPollAnswerVotersOptions(t *testing.T) {
	tests := []struct {
		name      string
		query     *PollAnswerVotersOptions
		wantAfter string
		wantLimit string
	}{
		{name: "default page"},
		{
			name:      "paginated page",
			query:     &PollAnswerVotersOptions{After: "user", Limit: 100},
			wantAfter: "user",
			wantLimit: "100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
				}
				wantPath := "/api/v" + APIVersion + "/channels/channel/polls/message/answers/7"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
				}
				if got := r.URL.Query().Get("after"); got != tt.wantAfter {
					t.Fatalf("after = %q, want %q", got, tt.wantAfter)
				}
				if got := r.URL.Query().Get("limit"); got != tt.wantLimit {
					t.Fatalf("limit = %q, want %q", got, tt.wantLimit)
				}
				if got := r.Header.Get("X-Test"); got != "poll" {
					t.Fatalf("X-Test = %q, want %q", got, "poll")
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(`{"users":[{"id":"voter","username":"Voter"}]}`)),
					Request:    r,
				}, nil
			})

			var voters []*User
			if tt.query == nil {
				voters, err = session.PollAnswerVoters("channel", "message", 7, WithHeader("X-Test", "poll"))
			} else {
				voters, err = session.PollAnswerVotersWithOptions("channel", "message", 7, tt.query, WithHeader("X-Test", "poll"))
			}
			if err != nil {
				t.Fatalf("poll answer voters returned error: %v", err)
			}
			if len(voters) != 1 || voters[0].ID != "voter" {
				t.Fatalf("voters = %#v", voters)
			}
		})
	}
}

func TestPollAnswerVotersErrors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantJSONErr bool
	}{
		{name: "http error", status: http.StatusInternalServerError, body: `{"message":"failed"}`},
		{name: "malformed response", status: http.StatusOK, body: `{`, wantJSONErr: true},
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

			voters, err := session.PollAnswerVotersWithOptions("channel", "message", 7, &PollAnswerVotersOptions{Limit: 25})
			if err == nil {
				t.Fatal("PollAnswerVotersWithOptions returned nil error")
			}
			if voters != nil {
				t.Fatalf("voters = %#v, want nil", voters)
			}
			if tt.wantJSONErr && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}
}

func TestPollExpireRequestOptions(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if got := r.Header.Get("X-Test"); got != "expire" {
			t.Fatalf("X-Test = %q, want %q", got, "expire")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"message","channel_id":"channel"}`)),
			Request:    r,
		}, nil
	})

	message, err := session.PollExpire("channel", "message", WithHeader("X-Test", "expire"))
	if err != nil {
		t.Fatalf("PollExpire returned error: %v", err)
	}
	if message.ID != "message" {
		t.Fatalf("message = %#v", message)
	}
}

func TestEntitlement(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantID     string
		wantREST   bool
		wantDecode bool
	}{
		{name: "success", status: http.StatusOK, body: `{"id":"entitlement","sku_id":"sku","application_id":"application","type":1,"deleted":false}`, wantID: "entitlement"},
		{name: "HTTP error", status: http.StatusNotFound, body: `{"code":10029,"message":"Unknown entitlement"}`, wantREST: true},
		{name: "invalid JSON", status: http.StatusOK, body: `{`, wantDecode: true},
		{name: "null response", status: http.StatusOK, body: `null`, wantDecode: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
				}
				wantPath := "/api/v" + APIVersion + "/applications/application/entitlements/entitlement"
				if r.URL.Path != wantPath {
					t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
				}
				if got := r.Header.Get("X-Test"); got != "entitlement" {
					t.Fatalf("X-Test = %q, want %q", got, "entitlement")
				}

				return &http.Response{
					StatusCode: tt.status,
					Status:     http.StatusText(tt.status),
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			entitlement, err := session.Entitlement("application", "entitlement", WithHeader("X-Test", "entitlement"))
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.status {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.status)
				}
			} else if tt.wantDecode {
				if !errors.Is(err, ErrJSONUnmarshal) {
					t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
				}
			} else if err != nil {
				t.Fatalf("Entitlement returned error: %v", err)
			}

			if tt.wantID == "" {
				if entitlement != nil {
					t.Fatalf("entitlement = %#v, want nil", entitlement)
				}
			} else if entitlement == nil || entitlement.ID != tt.wantID || entitlement.ApplicationID != "application" {
				t.Fatalf("entitlement = %#v", entitlement)
			}
		})
	}
}

func TestRedactedHeaderValues(t *testing.T) {
	values := redactedHeaderValues("Authorization", []string{"Bot secret"})
	if len(values) != 1 || values[0] != redactedValue {
		t.Fatalf("redactedHeaderValues() = %#v, want %q", values, redactedValue)
	}

	values = redactedHeaderValues("User-Agent", []string{"discordgo"})
	if len(values) != 1 || values[0] != "discordgo" {
		t.Fatalf("redactedHeaderValues() changed non-secret header: %#v", values)
	}
}

func TestRequestUnauthorizedErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		token    string
		wantErr  error
		wantREST bool
	}{
		{name: "unprefixed token", token: "expired", wantErr: ErrUnauthorized},
		{name: "bearer token", token: "Bearer expired", wantREST: true},
		{name: "bot token", token: "Bot expired", wantREST: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New(tt.token)
			if err != nil {
				t.Fatal(err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Status:     "401 Unauthorized",
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"401: Unauthorized"}`)),
					Request:    r,
				}, nil
			})

			_, err = session.RequestWithBucketID(http.MethodGet, EndpointGateway, nil, EndpointGateway)
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("RequestWithBucketID() error = %T %[1]v, want %v", err, tt.wantErr)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) {
					t.Fatalf("RequestWithBucketID() error = %T %[1]v, want *RESTError", err)
				}
			}
		})
	}
}

func TestNewRestErrorRedactsRequestSecrets(t *testing.T) {
	req, err := http.NewRequest("POST", EndpointWebhookToken("webhook", "secret-token"), bytes.NewBufferString("secret request body"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bot secret")
	req.Header.Set("User-Agent", "discordgo")
	resp := &http.Response{
		Status:     "401 Unauthorized",
		StatusCode: http.StatusUnauthorized,
		Request:    req,
	}

	restErr := newRestError(req, resp, []byte(`{"code":0,"message":"unauthorized"}`))

	if got := restErr.Request.Header.Get("Authorization"); got != redactedValue {
		t.Fatalf("RESTError request authorization = %q, want %q", got, redactedValue)
	}
	if got := restErr.Response.Request.Header.Get("Authorization"); got != redactedValue {
		t.Fatalf("RESTError response request authorization = %q, want %q", got, redactedValue)
	}
	if got := restErr.Request.URL.String(); strings.Contains(got, "secret-token") {
		t.Fatalf("RESTError request URL leaked webhook token: %q", got)
	}
	if got := restErr.Response.Request.URL.String(); strings.Contains(got, "secret-token") {
		t.Fatalf("RESTError response request URL leaked webhook token: %q", got)
	}
	if restErr.Request.Body != nil {
		t.Fatal("RESTError request retained body")
	}
	if restErr.Request.GetBody != nil {
		t.Fatal("RESTError request retained GetBody")
	}
	if restErr.Response.Request.Body != nil {
		t.Fatal("RESTError response request retained body")
	}
	if restErr.Response.Request.GetBody != nil {
		t.Fatal("RESTError response request retained GetBody")
	}
	if got := restErr.Request.URL.String(); !strings.Contains(got, redactedURLValue) {
		t.Fatalf("RESTError request URL = %q, want redacted token", got)
	}
	if got := restErr.Request.Header.Get("User-Agent"); got != "discordgo" {
		t.Fatalf("RESTError request user agent = %q, want discordgo", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bot secret" {
		t.Fatalf("original request authorization = %q, want Bot secret", got)
	}
	if got := req.URL.String(); !strings.Contains(got, "secret-token") {
		t.Fatalf("original request URL = %q, want original token", got)
	}
	if req.Body == nil {
		t.Fatal("original request body was cleared")
	}
	if req.GetBody == nil {
		t.Fatal("original request GetBody was cleared")
	}
	originalBody, err := req.GetBody()
	if err != nil {
		t.Fatalf("original request GetBody returned error: %v", err)
	}
	defer originalBody.Close()
	body, err := io.ReadAll(originalBody)
	if err != nil {
		t.Fatalf("original request body read returned error: %v", err)
	}
	if string(body) != "secret request body" {
		t.Fatalf("original request body = %q, want secret request body", body)
	}
}

func TestRedactedURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "webhook token",
			url:  "https://discord.com/api/v10/webhooks/123456789/token-value/messages/@original?wait=true",
			want: "https://discord.com/api/v10/webhooks/123456789/REDACTED/messages/@original?wait=true",
		},
		{
			name: "interaction token",
			url:  "https://discord.com/api/v10/interactions/123456789/token-value/callback",
			want: "https://discord.com/api/v10/interactions/123456789/REDACTED/callback",
		},
		{
			name: "ordinary endpoint",
			url:  "https://discord.com/api/v10/channels/123/messages",
			want: "https://discord.com/api/v10/channels/123/messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactedURL(tt.url); got != tt.want {
				t.Fatalf("redactedURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactedIdentify(t *testing.T) {
	op := identifyOp{2, Identify{Token: "Bot secret"}}

	redacted := redactedIdentify(op)
	if redacted.Data.Token != redactedValue {
		t.Fatalf("redacted token = %q, want %q", redacted.Data.Token, redactedValue)
	}
	if op.Data.Token != "Bot secret" {
		t.Fatalf("redactedIdentify mutated original token: %q", op.Data.Token)
	}
}

func TestRedactedRESTBody(t *testing.T) {
	body := []byte(`{"access_token":"access-value","token":"webhook-value","password":"password-value","nested":{"refresh_token":"refresh-value","client_secret":"client-secret-value","safe":"ok"},"items":[{"token":"item-token-value"}]}`)

	got := redactedRESTBody(body)
	for _, secret := range []string{"access-value", "webhook-value", "password-value", "refresh-value", "client-secret-value", "item-token-value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedRESTBody() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedRESTBody() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedRESTBody() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedRESTBodyInvalidJSON(t *testing.T) {
	body := []byte("not-json")

	if got := redactedRESTBody(body); got != string(body) {
		t.Fatalf("redactedRESTBody() = %q, want %q", got, string(body))
	}
}

func TestRedactedRESTBodyMultipart(t *testing.T) {
	body := []byte("--boundary\r\nContent-Disposition: form-data; name=\"files[0]\"; filename=\"upload.txt\"\r\n\r\nprivate upload contents\r\n--boundary--")

	got := redactedRESTBody(body, "multipart/form-data; boundary=boundary")
	if got != redactedMultipartBody {
		t.Fatalf("redactedRESTBody() = %q, want %q", got, redactedMultipartBody)
	}
	for _, secret := range []string{"upload.txt", "private upload contents"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedRESTBody() leaked %q in %s", secret, got)
		}
	}
}

func TestRedactedRESTBodyFormURLEncoded(t *testing.T) {
	body := []byte("grant_type=authorization_code&client_secret=client-secret-value&access_token=access-value&refresh_token=refresh-value&password=password-value&code=code-value&safe=ok")

	got := redactedRESTBody(body, "application/x-www-form-urlencoded; charset=utf-8")
	for _, secret := range []string{"client-secret-value", "access-value", "refresh-value", "password-value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedRESTBody() leaked %q in %s", secret, got)
		}
	}

	values, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("redactedRESTBody() returned invalid form body %q: %v", got, err)
	}
	for _, key := range []string{"client_secret", "access_token", "refresh_token", "password"} {
		if values.Get(key) != redactedValue {
			t.Fatalf("redactedRESTBody() %s = %q, want %q", key, values.Get(key), redactedValue)
		}
	}
	if values.Get("safe") != "ok" || values.Get("code") != "code-value" {
		t.Fatalf("redactedRESTBody() = %s, want non-secret fields preserved", got)
	}
}

// roundTripperFunc implements http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type closeNotifyReadCloser struct {
	reader   *strings.Reader
	closed   chan struct{}
	retained []byte
}

func (r *closeNotifyReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *closeNotifyReadCloser) Close() error {
	close(r.closed)
	return nil
}

func TestGuildPruneDaysRequest(t *testing.T) {
	for _, days := range []uint32{1, 7, 30} {
		t.Run(strconv.FormatUint(uint64(days), 10), func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v"+APIVersion+"/guilds/guild/prune" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bot test" || r.Header.Get("X-Test") != "prune" {
					t.Fatal("missing authorization or request option")
				}
				var data map[string]uint32
				if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
					t.Fatal(err)
				}
				if len(data) != 1 || data["days"] != days {
					t.Fatalf("request body = %v, want days=%d", data, days)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"pruned":12}`)), Request: r}, nil
			})
			count, err := session.GuildPrune("guild", days, WithHeader("X-Test", "prune"))
			if err != nil || count != 12 || calls != 1 {
				t.Fatalf("GuildPrune = %d, %v; requests = %d", count, err, calls)
			}
		})
	}
}

func TestChannelEditSpoilerFlag(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v"+APIVersion+"/channels/channel" {
			t.Fatalf("request = %s %s", r.Method, r.URL)
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if string(payload["flags"]) != "2097152" || string(payload["nsfw"]) != "false" {
			t.Fatalf("payload = %v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"channel","type":0,"nsfw":false,"flags":2097152}`)), Request: r}, nil
	})
	flags := ChannelFlagSpoiler
	nsfw := false
	channel, err := session.ChannelEdit("channel", &ChannelEdit{Flags: &flags, NSFW: &nsfw})
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil || channel.Flags&ChannelFlagSpoiler == 0 || channel.NSFW {
		t.Fatalf("channel = %#v", channel)
	}
}

func TestUserGuildsWithOptionsRequest(t *testing.T) {
	shard, three := 0, 3
	tests := []struct {
		name  string
		query *UserGuildsOptions
		want  url.Values
	}{
		{name: "nil", query: nil, want: url.Values{}},
		{name: "empty", query: &UserGuildsOptions{}, want: url.Values{}},
		{name: "shard zero", query: &UserGuildsOptions{Shard: &shard}, want: url.Values{"shard": {"0"}}},
		{
			name:  "all",
			query: &UserGuildsOptions{Limit: 50, Before: "2", After: "1", WithCounts: true, Shard: &three},
			want:  url.Values{"limit": {"50"}, "before": {"2"}, "after": {"1"}, "with_counts": {"true"}, "shard": {"3"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/api/v"+APIVersion+"/users/@me/guilds" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bot test" || r.Header.Get("X-Test") != "guilds" {
					t.Fatal("missing authorization or request option")
				}
				if got := r.URL.Query(); !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("query = %v, want %v", got, tt.want)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"id":"guild","name":"Guild"}]`)), Request: r}, nil
			})
			guilds, err := session.UserGuildsWithOptions(tt.query, WithHeader("X-Test", "guilds"))
			if err != nil || calls != 1 || len(guilds) != 1 || guilds[0].ID != "guild" {
				t.Fatalf("UserGuildsWithOptions = %v, %v; requests = %d", guilds, err, calls)
			}
		})
	}
}

func TestUserGuildsDelegatesQuery(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		want := url.Values{"limit": {"10"}, "before": {"2"}, "after": {"1"}, "with_counts": {"true"}}
		if got := r.URL.Query(); !reflect.DeepEqual(got, want) {
			t.Fatalf("query = %v, want %v", got, want)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`)), Request: r}, nil
	})
	if _, err := session.UserGuilds(10, "2", "1", true); err != nil {
		t.Fatal(err)
	}
}

func TestInviteTargetUserAddRemove(t *testing.T) {
	tests := []struct {
		name       string
		call       func(*Session) error
		wantMethod string
	}{
		{
			name:       "add",
			call:       func(s *Session) error { return s.InviteTargetUserAdd("code", "user") },
			wantMethod: http.MethodPut,
		},
		{
			name:       "remove",
			call:       func(s *Session) error { return s.InviteTargetUserRemove("code", "user") },
			wantMethod: http.MethodDelete,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != tt.wantMethod {
					t.Fatalf("method = %s, want %s", r.Method, tt.wantMethod)
				}
				if want := "/api/v" + APIVersion + "/invites/code/target-users/user"; r.URL.Path != want {
					t.Fatalf("path = %s, want %s", r.URL.Path, want)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			if err := tt.call(session); err != nil || calls != 1 {
				t.Fatalf("err = %v; requests = %d", err, calls)
			}
		})
	}
}

func TestInviteTargetUsersBulk(t *testing.T) {
	tests := []struct {
		name     string
		call     func(*Session, []string) error
		wantPath string
	}{
		{
			name:     "bulk add",
			call:     func(s *Session, ids []string) error { return s.InviteTargetUsersBulkAdd("code", ids) },
			wantPath: "/api/v" + APIVersion + "/invites/code/target-users/bulk-add",
		},
		{
			name:     "bulk delete",
			call:     func(s *Session, ids []string) error { return s.InviteTargetUsersBulkDelete("code", ids) },
			wantPath: "/api/v" + APIVersion + "/invites/code/target-users/bulk-delete",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != tt.wantPath {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var payload struct {
					UserIDs []string `json:"user_ids"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(payload.UserIDs, []string{"1", "2"}) {
					t.Fatalf("user_ids = %v", payload.UserIDs)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			if err := tt.call(session, []string{"1", "2"}); err != nil || calls != 1 {
				t.Fatalf("err = %v; requests = %d", err, calls)
			}
		})
	}
}

func TestInviteTargetUsersBulkValidation(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
	}{
		{name: "empty", ids: nil},
		{name: "over limit", ids: make([]string, MaxInviteTargetUsers+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			if err := session.InviteTargetUsersBulkAdd("code", tt.ids); err == nil {
				t.Fatal("expected an error")
			}
			if calls != 0 {
				t.Fatalf("expected no request, got %d", calls)
			}
		})
	}
}

func TestChannelInviteCreateTargetUserIDs(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var payload struct {
			TargetUserIDs []string `json:"target_user_ids"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payload.TargetUserIDs, []string{"1", "2"}) {
			t.Fatalf("target_user_ids = %v", payload.TargetUserIDs)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"abc"}`)), Request: r}, nil
	})
	invite, err := session.ChannelInviteCreate("channel", Invite{TargetUserIDs: []string{"1", "2"}})
	if err != nil || calls != 1 || invite.Code != "abc" {
		t.Fatalf("ChannelInviteCreate = %v, %v; requests = %d", invite, err, calls)
	}
}

func TestChannelInviteCreateRejectsConflictingTargetUsers(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	_, err = session.ChannelInviteCreate("channel", Invite{
		TargetUserIDs:   []string{"1"},
		TargetUsersFile: &File{Name: "users.csv"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls != 0 {
		t.Fatalf("expected no request, got %d", calls)
	}
}

func TestApplicationAttachmentUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/applications/app/attachment" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/applications/app/attachment")
		}
		if got := r.Header.Get("X-Test-Request-Option"); got != "present" {
			t.Fatalf("X-Test-Request-Option = %q, want present", got)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}
		if _, ok := r.MultipartForm.Value["payload_json"]; ok {
			t.Fatal("payload_json was included in application attachment upload")
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile returned error: %v", err)
		}
		defer func() { _ = file.Close() }()
		contents, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if header.Filename != "activity.png" {
			t.Fatalf("filename = %q, want activity.png", header.Filename)
		}
		if got := header.Header.Get("Content-Type"); got != "image/png" {
			t.Fatalf("file Content-Type = %q, want image/png", got)
		}
		if string(contents) != "png" {
			t.Fatalf("file contents = %q, want png", contents)
		}

		_, _ = w.Write([]byte(`{
			"attachment": {
				"id": "attachment",
				"filename": "activity.png",
				"title": null,
				"description": "Activity image",
				"content_type": "image/png",
				"size": 3,
				"url": "https://cdn.discordapp.com/attachment",
				"proxy_url": "https://media.discordapp.net/attachment",
				"width": 640,
				"height": 480,
				"placeholder": "hash",
				"placeholder_version": 1,
				"ephemeral": true,
				"duration_secs": 1.5,
				"waveform": "wave",
				"flags": 1,
				"clip_participants": [{"id":"user","username":"tester"}],
				"clip_created_at": "2026-01-02T03:04:05Z",
				"application": {"id":"app","name":"Activity"}
			}
		}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointApplications := EndpointApplications
	EndpointApplications = server.URL + "/applications"
	t.Cleanup(func() {
		EndpointApplications = oldEndpointApplications
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	response, err := session.ApplicationAttachmentUpload("app", &File{
		Name:        "activity.png",
		ContentType: "image/png",
		Reader:      strings.NewReader("png"),
		FieldName:   "ignored",
	}, WithHeader("X-Test-Request-Option", "present"))
	if err != nil {
		t.Fatalf("ApplicationAttachmentUpload returned error: %v", err)
	}

	attachment := response.Attachment
	if attachment.ID != "attachment" || attachment.Filename != "activity.png" || attachment.Title != "" {
		t.Fatalf("attachment identity = %#v", attachment)
	}
	if attachment.Description != "Activity image" || attachment.ContentType != "image/png" || attachment.Size != 3 {
		t.Fatalf("attachment metadata = %#v", attachment)
	}
	if attachment.URL != "https://cdn.discordapp.com/attachment" || attachment.ProxyURL != "https://media.discordapp.net/attachment" {
		t.Fatalf("attachment URLs = %#v", attachment)
	}
	if attachment.Width != 640 || attachment.Height != 480 || attachment.Placeholder != "hash" || attachment.PlaceholderVersion != 1 {
		t.Fatalf("attachment dimensions = %#v", attachment)
	}
	if !attachment.Ephemeral || attachment.DurationSecs != 1.5 || attachment.Waveform != "wave" || attachment.Flags != MessageAttachmentFlagsIsClip {
		t.Fatalf("attachment media data = %#v", attachment)
	}
	if len(attachment.ClipParticipants) != 1 || attachment.ClipParticipants[0].ID != "user" {
		t.Fatalf("clip participants = %#v", attachment.ClipParticipants)
	}
	if attachment.ClipCreatedAt == nil || !attachment.ClipCreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("clip created at = %#v", attachment.ClipCreatedAt)
	}
	if attachment.Application == nil || attachment.Application.ID != "app" || attachment.Application.Name != "Activity" {
		t.Fatalf("application = %#v", attachment.Application)
	}
}

func TestApplicationAttachmentUploadInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{`},
		{name: "null response", body: `null`},
		{name: "missing attachment", body: `{}`},
		{name: "null attachment", body: `{"attachment":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			response, err := session.ApplicationAttachmentUpload("app", &File{
				Name:   "activity.png",
				Reader: strings.NewReader("png"),
			})
			if response != nil {
				t.Fatalf("response = %#v, want nil", response)
			}
			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}
}

func TestApplicationAttachmentUploadRESTError(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":50046,"message":"Invalid file uploaded"}`)),
			Request:    r,
		}, nil
	})

	response, err := session.ApplicationAttachmentUpload("app", &File{
		Name:   "activity.png",
		Reader: strings.NewReader("png"),
	})
	if response != nil {
		t.Fatalf("response = %#v, want nil", response)
	}
	var restErr *RESTError
	if !errors.As(err, &restErr) {
		t.Fatalf("error = %T %v, want *RESTError", err, err)
	}
	if restErr.Response.StatusCode != http.StatusBadRequest || restErr.Message == nil || restErr.Message.Code != ErrCodeInvalidFileUploaded {
		t.Fatalf("RESTError = %#v", restErr)
	}
}

func TestApplicationAttachmentUploadInvalidFile(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, err := session.ApplicationAttachmentUpload("app", nil); err == nil {
		t.Fatal("ApplicationAttachmentUpload returned nil error for nil file")
	}
	if _, err := session.ApplicationAttachmentUpload("app", &File{Name: "activity.png"}); err == nil {
		t.Fatal("ApplicationAttachmentUpload returned nil error for nil file reader")
	}
}

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

func TestWithAuditLogReasonRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, reason, header string }{
		{"plain", "moderation", "moderation"},
		{"multiline", "first line\nsecond line\r\n", "first%20line%0Asecond%20line%0D%0A"},
		{"unicode", "café 🚫", "caf%C3%A9%20%F0%9F%9A%AB"},
		{"percent and plus", "100% + %20", "100%25%20%2B%20%2520"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("X-Audit-Log-Reason")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			s, err := New("Bot dummy-token")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Request(http.MethodDelete, server.URL, nil, WithAuditLogReason(tc.reason)); err != nil {
				t.Fatalf("request failed before reason could be received: %v", err)
			}
			got := <-received
			if got != tc.header {
				t.Errorf("wire reason = %q, want %q", got, tc.header)
			}
			decoded, err := url.PathUnescape(got)
			if err != nil || decoded != tc.reason {
				t.Errorf("decoded reason = %q, error %v; want %q", decoded, err, tc.reason)
			}
		})
	}
}

func TestChannelEditMarshalNullableFields(t *testing.T) {
	guildText := ChannelTypeGuildText
	guildNews := ChannelTypeGuildNews
	icon := "data:image/png;base64,aWNvbg=="
	iconValue := &icon
	rtcRegion := "us-central"
	rtcRegionValue := &rtcRegion
	var nullString *string
	videoQualityMode := VideoQualityModeFull
	defaultAutoArchiveDuration := 1440

	tests := []struct {
		name string
		edit ChannelEdit
		want string
	}{
		{
			name: "omits unset nullable fields",
			edit: ChannelEdit{Name: "ticket"},
			want: `{"name":"ticket"}`,
		},
		{
			name: "sets zero channel type",
			edit: ChannelEdit{Type: &guildText},
			want: `{"type":0}`,
		},
		{
			name: "sets nonzero channel type",
			edit: ChannelEdit{Type: &guildNews},
			want: `{"type":5}`,
		},
		{
			name: "sets group dm icon",
			edit: ChannelEdit{Icon: &iconValue},
			want: `{"icon":"data:image/png;base64,aWNvbg=="}`,
		},
		{
			name: "clears group dm icon",
			edit: ChannelEdit{Icon: &nullString},
			want: `{"icon":null}`,
		},
		{
			name: "keeps non-empty topic",
			edit: ChannelEdit{Topic: "updates"},
			want: `{"topic":"updates"}`,
		},
		{
			name: "clears topic",
			edit: ChannelEdit{Topic: "ignored", TopicNull: true},
			want: `{"topic":null}`,
		},
		{
			name: "keeps nonzero user limit",
			edit: ChannelEdit{UserLimit: 25},
			want: `{"user_limit":25}`,
		},
		{
			name: "omits unset zero user limit",
			edit: ChannelEdit{UserLimit: 0},
			want: `{}`,
		},
		{
			name: "sets zero user limit",
			edit: ChannelEdit{UserLimitSet: true},
			want: `{"user_limit":0}`,
		},
		{
			name: "sets rtc region",
			edit: ChannelEdit{RTCRegion: &rtcRegionValue},
			want: `{"rtc_region":"us-central"}`,
		},
		{
			name: "clears rtc region",
			edit: ChannelEdit{RTCRegion: &nullString},
			want: `{"rtc_region":null}`,
		},
		{
			name: "sets video quality mode",
			edit: ChannelEdit{VideoQualityMode: &videoQualityMode},
			want: `{"video_quality_mode":2}`,
		},
		{
			name: "sets default auto archive duration",
			edit: ChannelEdit{DefaultAutoArchiveDuration: &defaultAutoArchiveDuration},
			want: `{"default_auto_archive_duration":1440}`,
		},
		{
			name: "keeps non-empty parent id",
			edit: ChannelEdit{ParentID: "category"},
			want: `{"parent_id":"category"}`,
		},
		{
			name: "clears parent id",
			edit: ChannelEdit{ParentID: "ignored", ParentIDNull: true},
			want: `{"parent_id":null}`,
		},
		{
			name: "keeps empty permission overwrites",
			edit: ChannelEdit{PermissionOverwrites: []*PermissionOverwrite{}},
			want: `{"permission_overwrites":[]}`,
		},
		{
			name: "clears parent and keeps empty permission overwrites",
			edit: ChannelEdit{ParentIDNull: true, PermissionOverwrites: []*PermissionOverwrite{}},
			want: `{"parent_id":null,"permission_overwrites":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.edit)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func TestChannelEditRequestAndResponse(t *testing.T) {
	guildText := ChannelTypeGuildText
	videoQualityMode := VideoQualityModeFull
	defaultAutoArchiveDuration := 1440
	var rtcRegion *string
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}
		if r.URL.Path != "/channels/channel" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/channels/channel")
		}
		if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "channel%20parity" {
			t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "channel%20parity")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		assertJSONEqual(t, body, []byte(`{
			"type":0,
			"topic":null,
			"user_limit":0,
			"rtc_region":null,
			"video_quality_mode":2,
			"default_auto_archive_duration":1440
		}`))

		_, _ = w.Write([]byte(`{
			"id":"channel",
			"guild_id":"guild",
			"name":"general",
			"type":0,
			"topic":null,
			"user_limit":0,
			"rtc_region":null,
			"video_quality_mode":2,
			"default_auto_archive_duration":1440
		}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointChannels := EndpointChannels
	EndpointChannels = server.URL + "/channels/"
	t.Cleanup(func() {
		EndpointChannels = oldEndpointChannels
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	channel, err := session.ChannelEdit("channel", &ChannelEdit{
		Type:                       &guildText,
		TopicNull:                  true,
		UserLimitSet:               true,
		RTCRegion:                  &rtcRegion,
		VideoQualityMode:           &videoQualityMode,
		DefaultAutoArchiveDuration: &defaultAutoArchiveDuration,
	}, WithAuditLogReason("channel parity"))
	if err != nil {
		t.Fatalf("ChannelEdit returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if channel == nil || channel.ID != "channel" || channel.GuildID != "guild" || channel.Type != ChannelTypeGuildText {
		t.Fatalf("channel = %#v, want edited guild text channel", channel)
	}
	if channel.RTCRegion != nil || channel.VideoQualityMode != VideoQualityModeFull {
		t.Fatalf("voice settings = %#v/%d, want automatic region and full video", channel.RTCRegion, channel.VideoQualityMode)
	}
	if channel.UserLimit != 0 || channel.DefaultAutoArchiveDuration != 1440 {
		t.Fatalf("channel limits = %d/%d, want 0/1440", channel.UserLimit, channel.DefaultAutoArchiveDuration)
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotJSON interface{}
	if err := json.Unmarshal(got, &gotJSON); err != nil {
		t.Fatalf("got is invalid JSON: %v", err)
	}

	var wantJSON interface{}
	if err := json.Unmarshal(want, &wantJSON); err != nil {
		t.Fatalf("want is invalid JSON: %v", err)
	}

	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func TestGameDirectMessageModerationMetadataUpdate(t *testing.T) {
	session, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	calls := 0
	currentID := ""
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want %s", r.Method, http.MethodPut)
		}
		want := "/api/v" + APIVersion + "/partner-sdk/dms/user-a/user-b/messages/" + currentID + "/moderation-metadata"
		if r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		if got := r.Header.Get("Authorization"); got != "Bot token" {
			t.Errorf("Authorization = %q, want Bot token", got)
		}
		if got := r.Header.Get("X-Test"); got != "moderation" {
			t.Errorf("X-Test = %q, want moderation", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll returned error: %v", err)
		}
		assertLobbyJSONEqual(t, body, []byte(`{"action":"hide","reason":"toxicity"}`))
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})

	metadata := map[string]string{"action": "hide", "reason": "toxicity"}
	for _, messageID := range []string{"message-1", "message-2"} {
		currentID = messageID
		if err := session.GameDirectMessageModerationMetadataUpdate("user-a", "user-b", messageID, metadata, WithHeader("X-Test", "moderation")); err != nil {
			t.Fatalf("GameDirectMessageModerationMetadataUpdate returned error: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("requests = %d, want 2", calls)
	}

	// Messages in the same DM share one bucket, like lobby messages.
	session.Ratelimiter.Lock()
	defer session.Ratelimiter.Unlock()
	want := EndpointGameDirectMessageModerationMetadata("user-a", "user-b", "")
	if _, ok := session.Ratelimiter.buckets[want]; !ok || len(session.Ratelimiter.buckets) != 1 {
		t.Fatalf("buckets = %v, want only %q", session.Ratelimiter.buckets, want)
	}
}

func TestGameDirectMessageModerationMetadataUpdateReturnsRESTErrors(t *testing.T) {
	session, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":10008,"message":"Unknown Message"}`)),
			Request:    r,
		}, nil
	})

	err = session.GameDirectMessageModerationMetadataUpdate("user-a", "user-b", "message", map[string]string{"action": "show"})
	var restErr *RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil || restErr.Message.Code != ErrCodeUnknownMessage {
		t.Fatalf("error = %v, want RESTError with code %d", err, ErrCodeUnknownMessage)
	}
}

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
		if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "profile%20refresh" {
			t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "profile%20refresh")
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

// https://docs.discord.com/developers/resources/guild#modify-guild
func TestGuildEditExplicitResets(t *testing.T) {
	verification := VerificationLevelNone
	tests := []struct {
		name   string
		params GuildParams
		want   string
	}{
		{"omitted", GuildParams{}, `{}`},
		{"zero", GuildParams{DefaultMessageNotificationsSet: true, ExplicitContentFilterSet: true, SystemChannelFlagsSet: true, VerificationLevel: &verification}, `{"default_message_notifications":0,"explicit_content_filter":0,"system_channel_flags":0,"verification_level":0}`},
		{"null", GuildParams{AfkChannelIDNull: true, IconNull: true, SplashNull: true, DiscoverySplashNull: true, BannerNull: true, SystemChannelIDNull: true, RulesChannelIDNull: true, PublicUpdatesChannelIDNull: true, DescriptionNull: true}, `{"afk_channel_id":null,"icon":null,"splash":null,"discovery_splash":null,"banner":null,"system_channel_id":null,"rules_channel_id":null,"public_updates_channel_id":null,"description":null}`},
		{"legacy values", GuildParams{Name: "guild", DefaultMessageNotifications: 1, ExplicitContentFilter: 2, SystemChannelFlags: 1, AfkChannelID: "channel", Icon: "image", Splash: "splash", DiscoverySplash: "discovery", Banner: "banner", SystemChannelID: "system", RulesChannelID: "rules", PublicUpdatesChannelID: "updates", Description: "description", AfkTimeout: 300}, `{"name":"guild","default_message_notifications":1,"explicit_content_filter":2,"system_channel_flags":1,"afk_channel_id":"channel","icon":"image","splash":"splash","discovery_splash":"discovery","banner":"banner","system_channel_id":"system","rules_channel_id":"rules","public_updates_channel_id":"updates","description":"description","afk_timeout":300}`},
		{"null precedence", GuildParams{AfkChannelID: "channel", AfkChannelIDNull: true, Icon: "image", IconNull: true}, `{"afk_channel_id":null,"icon":null}`},
		{"set nonzero", GuildParams{DefaultMessageNotifications: 1, DefaultMessageNotificationsSet: true, ExplicitContentFilter: 2, ExplicitContentFilterSet: true, SystemChannelFlags: 1, SystemChannelFlagsSet: true}, `{"default_message_notifications":1,"explicit_content_filter":2,"system_channel_flags":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			called := false
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if r.Method != http.MethodPatch || r.URL.Path != "/api/v"+APIVersion+"/guilds/guild" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				var got, want map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("body = %#v, want %s", got, tt.want)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"guild","name":"updated"}`)), Request: r}, nil
			})
			guild, err := session.GuildEdit("guild", &tt.params)
			if err != nil {
				t.Fatal(err)
			}
			if !called || guild == nil || guild.Name != "updated" {
				t.Fatalf("GuildEdit did not complete: %#v", guild)
			}
		})
	}
}

func TestGuildEditNilParams(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("nil guild params must not send a request")
		return nil, nil
	})
	guild, err := session.GuildEdit("guild", nil)
	if err == nil || guild != nil {
		t.Fatalf("GuildEdit(nil) = %#v, %v; want nil and error", guild, err)
	}
}

func TestGuildHomeSettings(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		wantPath := "/api/v" + APIVersion + "/guilds/guild/new-member-welcome"
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Test"); got != "guild-home-settings" {
			t.Fatalf("X-Test = %q, want guild-home-settings", got)
		}

		body := `{"guild_id":"guild","enabled":true,"welcome_message":{"author_ids":["author"],"message":"Welcome"},"new_member_actions":[{"channel_id":"rules","action_type":0,"title":"Read the rules","description":"Start here","emoji":{"id":null,"name":"👋","animated":false},"icon":"book"},{"channel_id":"chat","action_type":1,"title":"Say hello","description":"Meet everyone"}],"resource_channels":[{"channel_id":"faq","title":"FAQ","description":"Common questions","emoji":{"id":"emoji","name":null,"animated":true},"icon":"help"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	settings, err := session.GuildHomeSettings("guild", WithHeader("X-Test", "guild-home-settings"))
	if err != nil {
		t.Fatalf("GuildHomeSettings returned error: %v", err)
	}
	if settings == nil || settings.GuildID != "guild" || !settings.Enabled {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.WelcomeMessage == nil || settings.WelcomeMessage.Message != "Welcome" || len(settings.WelcomeMessage.AuthorIDs) != 1 || settings.WelcomeMessage.AuthorIDs[0] != "author" {
		t.Fatalf("welcome message = %#v", settings.WelcomeMessage)
	}
	if len(settings.NewMemberActions) != 2 || settings.NewMemberActions[0].ActionType != GuildHomeNewMemberActionTypeView || settings.NewMemberActions[1].ActionType != GuildHomeNewMemberActionTypeTalk {
		t.Fatalf("new member actions = %#v", settings.NewMemberActions)
	}
	if settings.NewMemberActions[0].Emoji == nil || settings.NewMemberActions[0].Emoji.Name != "👋" || settings.NewMemberActions[1].Emoji != nil {
		t.Fatalf("new member action emojis = %#v", settings.NewMemberActions)
	}
	if len(settings.ResourceChannels) != 1 || settings.ResourceChannels[0].ChannelID != "faq" || settings.ResourceChannels[0].Emoji == nil || settings.ResourceChannels[0].Emoji.ID != "emoji" || !settings.ResourceChannels[0].Emoji.Animated {
		t.Fatalf("resource channels = %#v", settings.ResourceChannels)
	}
}

func TestGuildHomeSettingsResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantNil    bool
		wantJSON   bool
		wantREST   bool
	}{
		{name: "no content", statusCode: http.StatusNoContent, wantNil: true},
		{name: "malformed JSON", statusCode: http.StatusOK, body: `{`, wantNil: true, wantJSON: true},
		{name: "null JSON", statusCode: http.StatusOK, body: `null`, wantNil: true, wantJSON: true},
		{name: "REST error", statusCode: http.StatusForbidden, body: `{"code":50001,"message":"Missing Access"}`, wantNil: true, wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.statusCode,
					Status:     http.StatusText(tt.statusCode),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			settings, err := session.GuildHomeSettings("guild")
			if tt.wantJSON && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
			if tt.wantREST {
				var restErr *RESTError
				if !errors.As(err, &restErr) || restErr.Response.StatusCode != tt.statusCode {
					t.Fatalf("error = %T %v, want %d RESTError", err, err, tt.statusCode)
				}
			}
			if !tt.wantJSON && !tt.wantREST && err != nil {
				t.Fatalf("GuildHomeSettings returned error: %v", err)
			}
			if tt.wantNil && settings != nil {
				t.Fatalf("settings = %#v, want nil", settings)
			}
		})
	}
}

func guildJoinRequestString(value string) **string {
	pointer := &value
	return &pointer
}

func guildJoinRequestSession(t *testing.T, handler http.HandlerFunc) *Session {
	t.Helper()

	server := httptest.NewServer(handler)
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
	return session
}

func TestGuildJoinRequestActionParams(t *testing.T) {
	var nullString *string
	tests := []struct {
		name   string
		params GuildJoinRequestActionParams
		want   string
	}{
		{
			name: "omits unset rejection reason",
			params: GuildJoinRequestActionParams{
				Action: GuildJoinRequestApplicationStatusApproved,
			},
			want: `{"action":"APPROVED"}`,
		},
		{
			name: "sets rejection reason",
			params: GuildJoinRequestActionParams{
				Action:          GuildJoinRequestApplicationStatusRejected,
				RejectionReason: guildJoinRequestString("Incomplete answers"),
			},
			want: `{"action":"REJECTED","rejection_reason":"Incomplete answers"}`,
		},
		{
			name: "clears rejection reason",
			params: GuildJoinRequestActionParams{
				Action:          GuildJoinRequestApplicationStatusRejected,
				RejectionReason: &nullString,
			},
			want: `{"action":"REJECTED","rejection_reason":null}`,
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

func TestGuildJoinRequests(t *testing.T) {
	requests := 0
	session := guildJoinRequestSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/requests" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/requests")
		}
		if got := r.Header.Get("X-Test"); got != "join-requests" {
			t.Fatalf("X-Test = %q, want join-requests", got)
		}

		switch requests {
		case 1:
			if r.URL.RawQuery != "" {
				t.Fatalf("query = %q, want empty", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
				"total":1,
				"guild_join_requests":[{
					"id":"request",
					"created_at":"2026-07-12T10:00:00Z",
					"reviewed_at":"2026-07-12T11:00:00Z",
					"application_status":"REJECTED",
					"rejection_reason":"Incomplete answers",
					"guild_id":"guild",
					"user_id":"applicant",
					"user":{"id":"applicant","username":"Applicant"},
					"form_responses":[
						{"field_type":"MULTIPLE_CHOICE","label":"Pick one","description":"Choose","required":true,"choices":["One","Two"],"response":1},
						{"field_type":"PARAGRAPH","label":"About you","description":"Tell us","required":true,"placeholder":"Details","response":"Long answer"},
						{"field_type":"TERMS","label":"Rules","description":"Accept","required":true,"values":["Be kind"],"response":true},
						{"field_type":"TEXT_INPUT","label":"Name","description":"Display name","required":false,"placeholder":"Name","response":"Applicant"}
					],
					"actioned_by_user":{"id":"reviewer","username":"Reviewer"}
				}]
			}`))
		case 2:
			if got := r.URL.Query().Get("status"); got != "SUBMITTED" {
				t.Fatalf("status = %q, want SUBMITTED", got)
			}
			if got := r.URL.Query().Get("limit"); got != "100" {
				t.Fatalf("limit = %q, want 100", got)
			}
			if got := r.URL.Query().Get("before"); got != "before" {
				t.Fatalf("before = %q, want before", got)
			}
			if got := r.URL.Query().Get("after"); got != "after" {
				t.Fatalf("after = %q, want after", got)
			}
			_, _ = w.Write([]byte(`{"total":0,"guild_join_requests":[]}`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	})

	result, err := session.GuildJoinRequests("guild", WithHeader("X-Test", "join-requests"))
	if err != nil {
		t.Fatalf("GuildJoinRequests returned error: %v", err)
	}
	if result.Total != 1 || len(result.GuildJoinRequests) != 1 {
		t.Fatalf("result = %#v", result)
	}
	request := result.GuildJoinRequests[0]
	if request.ID != "request" || request.GuildID != "guild" || request.UserID != "applicant" {
		t.Fatalf("request ids = %#v", request)
	}
	if request.CreatedAt != time.Date(2026, time.July, 12, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("created_at = %v", request.CreatedAt)
	}
	if request.ReviewedAt == nil || *request.ReviewedAt != time.Date(2026, time.July, 12, 11, 0, 0, 0, time.UTC) {
		t.Fatalf("reviewed_at = %v", request.ReviewedAt)
	}
	if request.ApplicationStatus == nil || *request.ApplicationStatus != GuildJoinRequestApplicationStatusRejected {
		t.Fatalf("application_status = %v", request.ApplicationStatus)
	}
	if request.RejectionReason == nil || *request.RejectionReason != "Incomplete answers" {
		t.Fatalf("rejection_reason = %v", request.RejectionReason)
	}
	if request.User == nil || request.User.ID != "applicant" || request.ActionedByUser == nil || request.ActionedByUser.ID != "reviewer" {
		t.Fatalf("users = %#v, %#v", request.User, request.ActionedByUser)
	}
	if len(request.FormResponses) != 4 {
		t.Fatalf("form responses = %#v", request.FormResponses)
	}
	multipleChoice, ok := request.FormResponses[0].(*GuildJoinRequestMultipleChoiceFormFieldResponse)
	if !ok || multipleChoice.Type() != GuildMemberVerificationFormFieldTypeMultipleChoice || multipleChoice.Response == nil || *multipleChoice.Response != 1 || len(multipleChoice.Choices) != 2 {
		t.Fatalf("multiple choice response = %#v", request.FormResponses[0])
	}
	paragraph, ok := request.FormResponses[1].(*GuildJoinRequestParagraphFormFieldResponse)
	if !ok || paragraph.Response == nil || *paragraph.Response != "Long answer" {
		t.Fatalf("paragraph response = %#v", request.FormResponses[1])
	}
	terms, ok := request.FormResponses[2].(*GuildJoinRequestTermsFormFieldResponse)
	if !ok || terms.Response == nil || !*terms.Response || len(terms.Values) != 1 {
		t.Fatalf("terms response = %#v", request.FormResponses[2])
	}
	textInput, ok := request.FormResponses[3].(*GuildJoinRequestTextInputFormFieldResponse)
	if !ok || textInput.Response == nil || *textInput.Response != "Applicant" {
		t.Fatalf("text input response = %#v", request.FormResponses[3])
	}

	result, err = session.GuildJoinRequestsWithOptions("guild", &GuildJoinRequestsOptions{
		Status: GuildJoinRequestApplicationStatusSubmitted,
		Limit:  100,
		Before: "before",
		After:  "after",
	}, WithHeader("X-Test", "join-requests"))
	if err != nil {
		t.Fatalf("GuildJoinRequestsWithOptions returned error: %v", err)
	}
	if result.Total != 0 || result.GuildJoinRequests == nil || len(result.GuildJoinRequests) != 0 {
		t.Fatalf("empty result = %#v", result)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestGuildJoinRequestAction(t *testing.T) {
	var nullString *string
	requests := 0
	session := guildJoinRequestSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}
		if r.URL.Path != "/guilds/guild/requests/request" {
			t.Fatalf("path = %q, want action path", r.URL.Path)
		}
		if got := r.Header.Get("X-Test"); got != "join-request-action" {
			t.Fatalf("X-Test = %q, want join-request-action", got)
		}

		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		switch requests {
		case 1:
			if got := string(payload["action"]); got != `"APPROVED"` {
				t.Fatalf("action = %s, want APPROVED", got)
			}
			if _, ok := payload["rejection_reason"]; ok {
				t.Fatalf("rejection_reason was sent: %s", payload["rejection_reason"])
			}
		case 2:
			if got := string(payload["action"]); got != `"REJECTED"` {
				t.Fatalf("action = %s, want REJECTED", got)
			}
			if got := string(payload["rejection_reason"]); got != `"Incomplete answers"` {
				t.Fatalf("rejection_reason = %s", got)
			}
		case 3:
			if got := string(payload["rejection_reason"]); got != "null" {
				t.Fatalf("rejection_reason = %s, want null", got)
			}
		default:
			t.Fatalf("unexpected request %d", requests)
		}

		status := "APPROVED"
		if requests > 1 {
			status = "REJECTED"
		}
		_, _ = w.Write([]byte(`{"id":"request","created_at":"2026-07-12T10:00:00Z","reviewed_at":null,"application_status":"` + status + `","rejection_reason":null,"guild_id":"guild","user_id":"applicant","form_responses":null}`))
	})

	approved, err := session.GuildJoinRequestAction("guild", "request", &GuildJoinRequestActionParams{
		Action: GuildJoinRequestApplicationStatusApproved,
	}, WithHeader("X-Test", "join-request-action"))
	if err != nil {
		t.Fatalf("approve returned error: %v", err)
	}
	if approved.ApplicationStatus == nil || *approved.ApplicationStatus != GuildJoinRequestApplicationStatusApproved {
		t.Fatalf("approved request = %#v", approved)
	}

	rejected, err := session.GuildJoinRequestAction("guild", "request", &GuildJoinRequestActionParams{
		Action:          GuildJoinRequestApplicationStatusRejected,
		RejectionReason: guildJoinRequestString("Incomplete answers"),
	}, WithHeader("X-Test", "join-request-action"))
	if err != nil {
		t.Fatalf("reject returned error: %v", err)
	}
	if rejected.ApplicationStatus == nil || *rejected.ApplicationStatus != GuildJoinRequestApplicationStatusRejected {
		t.Fatalf("rejected request = %#v", rejected)
	}

	if _, err = session.GuildJoinRequestAction("guild", "request", &GuildJoinRequestActionParams{
		Action:          GuildJoinRequestApplicationStatusRejected,
		RejectionReason: &nullString,
	}, WithHeader("X-Test", "join-request-action")); err != nil {
		t.Fatalf("null rejection reason returned error: %v", err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestGuildJoinRequestNullableResponse(t *testing.T) {
	session := guildJoinRequestSession(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"id":"request",
			"created_at":"2026-07-12T10:00:00Z",
			"reviewed_at":null,
			"application_status":null,
			"rejection_reason":null,
			"guild_id":"guild",
			"user_id":"applicant",
			"user":null,
			"form_responses":null,
			"actioned_by_user":null
		}`))
	})

	request, err := session.GuildJoinRequestAction("guild", "request", &GuildJoinRequestActionParams{
		Action: GuildJoinRequestApplicationStatusApproved,
	})
	if err != nil {
		t.Fatalf("GuildJoinRequestAction returned error: %v", err)
	}
	if request.ReviewedAt != nil || request.ApplicationStatus != nil || request.RejectionReason != nil || request.User != nil || request.FormResponses != nil || request.ActionedByUser != nil {
		t.Fatalf("nullable fields = %#v", request)
	}
}

func TestGuildJoinRequestFailures(t *testing.T) {
	session := guildJoinRequestSession(t, func(w http.ResponseWriter, r *http.Request) {
		guildID := strings.Split(strings.TrimPrefix(r.URL.Path, "/guilds/"), "/")[0]
		switch guildID {
		case "http-list", "http-action":
			http.Error(w, `{"code":50035,"message":"Invalid Form Body"}`, http.StatusBadRequest)
		case "malformed-list", "malformed-action":
			_, _ = w.Write([]byte(`{`))
		case "null-list", "null-action":
			_, _ = w.Write([]byte(`null`))
		case "missing-list":
			_, _ = w.Write([]byte(`{"total":0}`))
		case "null-item":
			_, _ = w.Write([]byte(`{"total":1,"guild_join_requests":[null]}`))
		case "invalid-form":
			_, _ = w.Write([]byte(`{"total":1,"guild_join_requests":[{"id":"request","created_at":"2026-07-12T10:00:00Z","reviewed_at":null,"application_status":"SUBMITTED","rejection_reason":null,"guild_id":"guild","user_id":"applicant","form_responses":[{"field_type":"TERMS","values":[],"response":"yes"}]}]}`))
		case "unknown-form":
			_, _ = w.Write([]byte(`{"total":1,"guild_join_requests":[{"id":"request","created_at":"2026-07-12T10:00:00Z","reviewed_at":null,"application_status":"SUBMITTED","rejection_reason":null,"guild_id":"guild","user_id":"applicant","form_responses":[{"field_type":"UNKNOWN"}]}]}`))
		default:
			t.Fatalf("unexpected guild id %q", guildID)
		}
	})

	listTests := []struct {
		guildID     string
		wantJSONErr bool
	}{
		{guildID: "http-list"},
		{guildID: "malformed-list", wantJSONErr: true},
		{guildID: "null-list", wantJSONErr: true},
		{guildID: "missing-list", wantJSONErr: true},
		{guildID: "null-item", wantJSONErr: true},
		{guildID: "invalid-form", wantJSONErr: true},
	}
	for _, tt := range listTests {
		t.Run(tt.guildID, func(t *testing.T) {
			result, err := session.GuildJoinRequests(tt.guildID)
			if err == nil {
				t.Fatal("GuildJoinRequests returned nil error")
			}
			if result != nil {
				t.Fatalf("result = %#v, want nil", result)
			}
			if tt.wantJSONErr && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}

	result, err := session.GuildJoinRequests("unknown-form")
	if err != nil {
		t.Fatalf("unknown form response returned error: %v", err)
	}
	if len(result.GuildJoinRequests) != 1 || len(result.GuildJoinRequests[0].FormResponses) != 1 || result.GuildJoinRequests[0].FormResponses[0].Type() != "UNKNOWN" {
		t.Fatalf("unknown form response = %#v", result)
	}

	actionTests := []struct {
		guildID     string
		wantJSONErr bool
	}{
		{guildID: "http-action"},
		{guildID: "malformed-action", wantJSONErr: true},
		{guildID: "null-action", wantJSONErr: true},
	}
	for _, tt := range actionTests {
		t.Run(tt.guildID, func(t *testing.T) {
			request, err := session.GuildJoinRequestAction(tt.guildID, "request", &GuildJoinRequestActionParams{
				Action: GuildJoinRequestApplicationStatusApproved,
			})
			if err == nil {
				t.Fatal("GuildJoinRequestAction returned nil error")
			}
			if request != nil {
				t.Fatalf("request = %#v, want nil", request)
			}
			if tt.wantJSONErr && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}
}

// Discord's template example uses integer placeholder IDs and permissions.
// https://docs.discord.com/developers/resources/guild-template#guild-template-object
const guildTemplateSnapshotJSON = `{"code":"template","source_guild_id":"678070694164299796","serialized_source_guild":{"name":"Friends & Family","afk_channel_id":null,"system_channel_id":2,"roles":[{"id":0,"name":"@everyone","permissions":104324689}],"channels":[{"id":1,"name":"Text Channels","type":4,"parent_id":null},{"id":2,"name":"general","type":0,"parent_id":1,"permission_overwrites":[{"id":0,"type":0,"allow":9007199254740993,"deny":"1024"}]}]}}`

func TestGuildTemplatePlaceholderIDs(t *testing.T) {
	var template GuildTemplate
	if err := json.Unmarshal([]byte(guildTemplateSnapshotJSON), &template); err != nil {
		t.Fatal(err)
	}
	guild := template.SerializedSourceGuild
	if template.Code != "template" || template.SourceGuildID != "678070694164299796" || guild == nil {
		t.Fatalf("template = %#v", template)
	}
	if guild.Name != "Friends & Family" || guild.AfkChannelID != "" || guild.SystemChannelID != "2" {
		t.Fatalf("guild = %#v", guild)
	}
	if len(guild.Roles) != 1 || guild.Roles[0].ID != "0" || guild.Roles[0].Permissions != 104324689 {
		t.Fatalf("roles = %#v", guild.Roles)
	}
	if len(guild.Channels) != 2 || guild.Channels[0].ID != "1" || guild.Channels[0].ParentID != "" || guild.Channels[1].ID != "2" || guild.Channels[1].ParentID != "1" {
		t.Fatalf("channels = %#v", guild.Channels)
	}
	overwrite := guild.Channels[1].PermissionOverwrites[0]
	if overwrite.ID != "0" || overwrite.Allow != 9007199254740993 || overwrite.Deny != 1024 {
		t.Fatalf("overwrite = %#v", overwrite)
	}
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip GuildTemplate
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.SerializedSourceGuild.Channels[1].PermissionOverwrites[0].Allow != overwrite.Allow {
		t.Fatal("round trip lost permission precision")
	}
}

func TestGuildTemplateRESTPlaceholderIDs(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := guildTemplateSnapshotJSON
		if strings.HasSuffix(r.URL.Path, "/templates") && r.Method == http.MethodGet {
			body = "[" + body + "]"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	checks := map[string]func() (*GuildTemplate, error){
		"get": func() (*GuildTemplate, error) { return session.GuildTemplate("template") },
		"create": func() (*GuildTemplate, error) {
			return session.GuildTemplateCreateWithError("guild", &GuildTemplateParams{Name: "test"})
		},
		"edit": func() (*GuildTemplate, error) {
			return session.GuildTemplateEdit("guild", "template", &GuildTemplateParams{Name: "test"})
		},
		"list": func() (*GuildTemplate, error) {
			templates, err := session.GuildTemplates("guild")
			if err != nil {
				return nil, err
			}
			if len(templates) != 1 {
				t.Fatalf("templates = %#v", templates)
			}
			return templates[0], nil
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			template, err := check()
			if err != nil {
				t.Fatal(err)
			}
			if template.SerializedSourceGuild.SystemChannelID != "2" {
				t.Fatal("placeholder was not decoded")
			}
		})
	}
}

func TestGuildTemplateSnapshotValidation(t *testing.T) {
	for _, snapshot := range []string{
		`null`, `{}`, `{"roles":null,"channels":[null]}`,
		`{"system_channel_id":"2","roles":[{"id":"0","permissions":"104324689"}]}`,
	} {
		var template GuildTemplate
		if err := json.Unmarshal([]byte(`{"serialized_source_guild":`+snapshot+`}`), &template); err != nil {
			t.Errorf("valid snapshot %s: %v", snapshot, err)
		}
	}
	for _, snapshot := range []string{
		`[]`, `{"id":true}`, `{"id":1.5}`, `{"id":-1}`,
		`{"id":18446744073709551616}`, `{"roles":{}}`,
		`{"roles":[{"permissions":9223372036854775808}]}`,
		`{"channels":[{"permission_overwrites":[{"allow":false}]}]}`,
	} {
		var template GuildTemplate
		if err := json.Unmarshal([]byte(`{"serialized_source_guild":`+snapshot+`}`), &template); err == nil {
			t.Errorf("invalid snapshot accepted: %s", snapshot)
		}
	}
	var guild Guild
	if err := json.Unmarshal([]byte(`{"roles":[{"id":0,"permissions":104324689}]}`), &guild); err == nil {
		t.Fatal("ordinary guild decoding unexpectedly accepts template placeholders")
	}
}

func guildWidgetSession(t *testing.T, handler http.HandlerFunc) *Session {
	t.Helper()

	server := httptest.NewServer(handler)
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
	return session
}

func TestGuildWidget(t *testing.T) {
	session := guildWidgetSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/widget.json" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/widget.json")
		}
		if got := r.Header.Get("X-Test"); got != "guild-widget" {
			t.Fatalf("X-Test = %q, want guild-widget", got)
		}

		_, _ = w.Write([]byte(`{
			"id":"guild",
			"name":"Guild",
			"instant_invite":"https://discord.gg/widget",
			"channels":[{"id":"channel","name":"Voice","position":2}],
			"members":[{
				"id":"0",
				"username":"Member",
				"discriminator":"0000",
				"avatar":null,
				"status":"online",
				"avatar_url":"https://cdn.discordapp.com/widget-avatar.png",
				"activity":{"name":"Game"},
				"deaf":true,
				"mute":false,
				"self_deaf":false,
				"self_mute":true,
				"suppress":false,
				"channel_id":"channel"
			}],
			"presence_count":42
		}`))
	})

	widget, err := session.GuildWidget("guild", WithHeader("X-Test", "guild-widget"))
	if err != nil {
		t.Fatalf("GuildWidget returned error: %v", err)
	}
	if widget.ID != "guild" || widget.Name != "Guild" || widget.PresenceCount != 42 {
		t.Fatalf("widget = %#v", widget)
	}
	if widget.InstantInvite == nil || *widget.InstantInvite != "https://discord.gg/widget" {
		t.Fatalf("instant invite = %v", widget.InstantInvite)
	}
	if len(widget.Channels) != 1 || widget.Channels[0].ID != "channel" || widget.Channels[0].Name != "Voice" || widget.Channels[0].Position != 2 {
		t.Fatalf("channels = %#v", widget.Channels)
	}
	if len(widget.Members) != 1 {
		t.Fatalf("members = %#v", widget.Members)
	}
	member := widget.Members[0]
	if member.ID != "0" || member.Username != "Member" || member.Discriminator != "0000" || member.Avatar != "" {
		t.Fatalf("member user = %#v", member.User)
	}
	if member.Status != StatusOnline || member.AvatarURL != "https://cdn.discordapp.com/widget-avatar.png" || member.ChannelID != "channel" {
		t.Fatalf("member presence = %#v", member)
	}
	if member.Activity == nil || member.Activity.Name != "Game" {
		t.Fatalf("member activity = %#v", member.Activity)
	}
	if member.Deaf == nil || !*member.Deaf || member.Mute == nil || *member.Mute || member.SelfDeaf == nil || *member.SelfDeaf || member.SelfMute == nil || !*member.SelfMute || member.Suppress == nil || *member.Suppress {
		t.Fatalf("member voice state = %#v", member)
	}
}

func TestGuildWidgetFailures(t *testing.T) {
	session := guildWidgetSession(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/guilds/malformed/widget.json":
			_, _ = w.Write([]byte(`{`))
		case "/guilds/null/widget.json":
			_, _ = w.Write([]byte(`null`))
		case "/guilds/rest-error/widget.json":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":50004,"message":"Guild widget disabled"}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	})

	tests := []struct {
		name          string
		guildID       string
		wantREST      bool
		wantUnmarshal bool
	}{
		{name: "malformed response", guildID: "malformed", wantUnmarshal: true},
		{name: "null response", guildID: "null", wantUnmarshal: true},
		{name: "REST error", guildID: "rest-error", wantREST: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			widget, err := session.GuildWidget(tt.guildID)
			if err == nil {
				t.Fatal("GuildWidget returned nil error")
			}
			if widget != nil {
				t.Fatalf("widget = %#v, want nil", widget)
			}
			if tt.wantUnmarshal && !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
			var restErr *RESTError
			if tt.wantREST && !errors.As(err, &restErr) {
				t.Fatalf("error = %v, want RESTError", err)
			}
		})
	}
}

func TestGuildWidgetImageURL(t *testing.T) {
	oldEndpointGuilds := EndpointGuilds
	EndpointGuilds = "https://example.com/api/v10/guilds/"
	t.Cleanup(func() {
		EndpointGuilds = oldEndpointGuilds
	})

	tests := []struct {
		name  string
		style GuildWidgetStyle
		want  string
	}{
		{name: "default", want: "https://example.com/api/v10/guilds/guild/widget.png"},
		{name: "shield", style: GuildWidgetStyleShield, want: "https://example.com/api/v10/guilds/guild/widget.png?style=shield"},
		{name: "banner 1", style: GuildWidgetStyleBanner1, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner1"},
		{name: "banner 2", style: GuildWidgetStyleBanner2, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner2"},
		{name: "banner 3", style: GuildWidgetStyleBanner3, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner3"},
		{name: "banner 4", style: GuildWidgetStyleBanner4, want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner4"},
		{name: "query encoding", style: GuildWidgetStyle("banner&custom"), want: "https://example.com/api/v10/guilds/guild/widget.png?style=banner%26custom"},
	}

	widget := &GuildWidget{ID: "guild"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := widget.ImageURL(tt.style); got != tt.want {
				t.Fatalf("ImageURL(%q) = %q, want %q", tt.style, got, tt.want)
			}
		})
	}
}

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

				body, err := io.ReadAll(r.Body)
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
				body, err := io.ReadAll(r.Body)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestMessageEditUploadRetrySafety(t *testing.T) {
	for _, api := range []struct {
		name string
		path string
		edit func(*Session, []*File) (*Message, error)
	}{
		{
			name: "channel", path: "/channels/channel/messages/message",
			edit: func(s *Session, files []*File) (*Message, error) {
				return s.ChannelMessageEditComplex(&MessageEdit{Channel: "channel", ID: "message", Files: files})
			},
		},
		{
			name: "webhook", path: "/webhooks/webhook/test-token/messages/message",
			edit: func(s *Session, files []*File) (*Message, error) {
				return s.WebhookMessageEdit("webhook", "test-token", "message", &WebhookEdit{Files: files})
			},
		},
		{
			name: "interaction", path: "/webhooks/application/test-token/messages/@original",
			edit: func(s *Session, files []*File) (*Message, error) {
				return s.InteractionResponseEdit(&Interaction{AppID: "application", Token: "test-token"}, &WebhookEdit{Files: files})
			},
		},
	} {
		for _, firstStatus := range []int{http.StatusBadGateway, http.StatusTooManyRequests} {
			t.Run(fmt.Sprintf("%s/%d", api.name, firstStatus), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				attempts := 0
				var attachments []*MessageAttachment
				var responseClosures []chan struct{}
				session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					defer func() { _ = r.Body.Close() }()
					attempts++
					if r.Method != http.MethodPatch || r.URL.Path != "/api/v"+APIVersion+api.path {
						t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					reader, err := r.MultipartReader()
					if err != nil {
						t.Fatal(err)
					}
					files, payloads := 0, 0
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(part)
						if err != nil {
							t.Fatal(err)
						}
						switch part.FormName() {
						case "payload_json":
							payloads++
							var payload map[string]json.RawMessage
							if err := json.Unmarshal(body, &payload); err != nil {
								t.Fatal(err)
							}
							if _, replacing := payload["attachments"]; replacing {
								t.Fatal("fixture must append uploads without replacing existing attachments")
							}
						case "files[0]":
							files++
							if part.FileName() != "upload.txt" || string(body) != "file contents" {
								t.Fatal("upload filename or contents changed")
							}
						default:
							t.Fatalf("unexpected multipart field %q", part.FormName())
						}
						if err := part.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if files != 1 || payloads != 1 {
						t.Fatalf("multipart files=%d payloads=%d, want one of each", files, payloads)
					}
					// Model the documented append semantics. A gateway error can
					// follow a committed upload; a rate limit rejects it outright.
					if attempts != 1 || firstStatus != http.StatusTooManyRequests {
						attachments = append(attachments, &MessageAttachment{ID: fmt.Sprint(len(attachments) + 1), Filename: "upload.txt"})
					}
					status := http.StatusOK
					body, err := json.Marshal(&Message{ID: "message", ChannelID: "channel", Attachments: attachments})
					if err != nil {
						t.Fatal(err)
					}
					if attempts == 1 {
						status = firstStatus
						body = []byte(`{"message":"upstream response unavailable"}`)
						if status == http.StatusTooManyRequests {
							body = []byte(`{"message":"rate limited","retry_after":0.001,"global":false}`)
						}
					}
					closed := make(chan struct{})
					responseClosures = append(responseClosures, closed)
					return &http.Response{
						StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
						Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r,
						Body: &closeNotifyReadCloser{reader: strings.NewReader(string(body)), closed: closed},
					}, nil
				})
				message, err := api.edit(session, []*File{{Name: "upload.txt", Reader: strings.NewReader("file contents")}})
				if firstStatus == http.StatusBadGateway {
					var restErr *RESTError
					if !errors.As(err, &restErr) || restErr.Response.StatusCode != firstStatus || message != nil || attempts != 1 {
						t.Errorf("ambiguous upload result: attempts=%d message=%v error=%v; want one attempt and original RESTError", attempts, message, err)
					}
				} else if err != nil || attempts != 2 || message == nil || len(message.Attachments) != 1 {
					t.Errorf("rate-limited upload result: attempts=%d message=%v error=%v; want one successful retry", attempts, message, err)
				}
				if len(attachments) != 1 {
					t.Errorf("uploaded attachments = %d, want 1; an ambiguous failure replayed the append", len(attachments))
				}
				for _, closed := range responseClosures {
					select {
					case <-closed:
					default:
						t.Error("response body was not closed")
					}
				}
			})
		}
	}
}

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

func TestSessionAllowedMentionsPreservesSharedPayloads(t *testing.T) {
	content := "@everyone <@123> shared announcement"
	message := func(mentions *MessageAllowedMentions) any {
		return &MessageSend{Content: content, AllowedMentions: mentions}
	}
	messageEdit := func(mentions *MessageAllowedMentions) any {
		return &MessageEdit{Channel: "channel", ID: "message", Content: &content, AllowedMentions: mentions}
	}
	webhook := func(mentions *MessageAllowedMentions) any {
		return &WebhookParams{Content: content, AllowedMentions: mentions}
	}
	webhookEdit := func(mentions *MessageAllowedMentions) any {
		return &WebhookEdit{Content: &content, AllowedMentions: mentions}
	}
	interaction := func(mentions *MessageAllowedMentions) any {
		return &InteractionResponse{Type: InteractionResponseChannelMessageWithSource, Data: &InteractionResponseData{Content: content, AllowedMentions: mentions}}
	}
	tests := []struct {
		name       string
		newPayload func(*MessageAllowedMentions) any
		call       func(*Session, any, string) error
	}{
		{"send", message, func(s *Session, payload any, id string) error {
			_, err := s.ChannelMessageSendComplex(id, payload.(*MessageSend))
			return err
		}},
		{"edit", messageEdit, func(s *Session, payload any, _ string) error {
			_, err := s.ChannelMessageEditComplex(payload.(*MessageEdit))
			return err
		}},
		{"forum", message, func(s *Session, payload any, id string) error {
			_, err := s.ForumThreadStartComplex(id, &ThreadStart{Name: "thread", AutoArchiveDuration: 60}, payload.(*MessageSend))
			return err
		}},
		{"webhook", webhook, func(s *Session, payload any, id string) error {
			_, err := s.WebhookExecute(id, "token", true, payload.(*WebhookParams))
			return err
		}},
		{"webhook_edit", webhookEdit, func(s *Session, payload any, id string) error {
			_, err := s.WebhookMessageEdit(id, "token", "message", payload.(*WebhookEdit))
			return err
		}},
		{"followup", webhook, func(s *Session, payload any, id string) error {
			_, err := s.FollowupMessageCreate(&Interaction{AppID: "app", Token: id}, true, payload.(*WebhookParams))
			return err
		}},
		{"followup_edit", webhookEdit, func(s *Session, payload any, id string) error {
			_, err := s.FollowupMessageEdit(&Interaction{AppID: "app", Token: id}, "message", payload.(*WebhookEdit))
			return err
		}},
		{"interaction", interaction, func(s *Session, payload any, id string) error {
			return s.InteractionRespond(&Interaction{ID: id, Token: "token"}, payload.(*InteractionResponse))
		}},
	}
	for _, tt := range tests {
		for _, explicit := range []bool{false, true} {
			t.Run(tt.name+"/explicit="+strconv.FormatBool(explicit), func(t *testing.T) {
				var mentions *MessageAllowedMentions
				if explicit {
					mentions = &MessageAllowedMentions{Parse: []AllowedMentionType{AllowedMentionTypeUsers}}
				}
				payload := tt.newPayload(mentions)
				before, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var requests atomic.Int32
				var sessions [2]*Session
				for i, parse := range [][]AllowedMentionType{{AllowedMentionTypeEveryone}, {}} {
					s, err := New("")
					if err != nil {
						t.Fatal(err)
					}
					s.AllowedMentions = &MessageAllowedMentions{Parse: parse}
					wantParse := parse
					if explicit {
						wantParse = mentions.Parse
					}
					s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						requests.Add(1)
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							return nil, err
						}
						field := ""
						switch tt.name {
						case "forum":
							field = "message"
						case "interaction":
							field = "data"
						}
						if field != "" {
							var nested map[string]json.RawMessage
							if err := json.Unmarshal(body[field], &nested); err != nil {
								return nil, err
							}
							body = nested
						}
						var got MessageAllowedMentions
						if err := json.Unmarshal(body["allowed_mentions"], &got); err != nil {
							return nil, err
						}
						if !reflect.DeepEqual(got.Parse, wantParse) {
							return nil, fmt.Errorf("allowed_mentions.parse = %v, want %v", got.Parse, wantParse)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"result"}`))}, nil
					})
					sessions[i] = s
				}

				const workers = 16
				start := make(chan struct{})
				results := make(chan error, workers)
				for i := range workers {
					go func() {
						<-start
						results <- tt.call(sessions[i%2], payload, strconv.Itoa(i+1))
					}()
				}
				close(start)
				var firstErr error
				for range workers {
					if err := <-results; err != nil && firstErr == nil {
						firstErr = err
					}
				}
				if firstErr != nil {
					t.Error(firstErr)
				}
				// Both policies must also remain effective when the same payload is reused serially.
				for i, s := range sessions {
					if err := tt.call(s, payload, strconv.Itoa(workers+i+1)); err != nil {
						t.Error(err)
					}
				}
				if requests.Load() != workers+2 {
					t.Errorf("requests = %d, want %d", requests.Load(), workers+2)
				}
				after, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Errorf("caller payload changed: before %s, after %s", before, after)
				}
			})
		}
	}
}

func TestRESTHelpersPreserveSharedEmbed(t *testing.T) {
	tests := []struct {
		name string
		call func(*Session, *MessageEmbed) error
	}{
		{"send", func(s *Session, embed *MessageEmbed) error {
			_, err := s.ChannelMessageSendEmbed("channel", embed)
			return err
		}},
		{"edit", func(s *Session, embed *MessageEmbed) error {
			_, err := s.ChannelMessageEditEmbed("channel", "message", embed)
			return err
		}},
		{"forum", func(s *Session, embed *MessageEmbed) error {
			_, err := s.ForumThreadStartEmbed("channel", "thread", 60, embed)
			return err
		}},
	}
	for _, tt := range tests {
		for _, embedType := range []EmbedType{"", EmbedTypeRich} {
			t.Run(tt.name+"/type="+string(embedType), func(t *testing.T) {
				s, err := New("")
				if err != nil {
					t.Fatal(err)
				}
				embed := &MessageEmbed{Type: embedType, Title: "Shared announcement", Description: "Read-only content"}
				var requests atomic.Int32
				s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					var payload struct {
						Embeds  []MessageEmbed `json:"embeds"`
						Message *MessageSend   `json:"message"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						return nil, err
					}
					embeds := payload.Embeds
					if tt.name == "forum" && payload.Message != nil {
						for _, item := range payload.Message.Embeds {
							embeds = append(embeds, *item)
						}
					}
					if len(embeds) != 1 || embeds[0].Title != "Shared announcement" || embeds[0].Description != "Read-only content" {
						return nil, fmt.Errorf("unexpected request embeds: %+v", embeds)
					}
					// Discord defaults an omitted type to rich. Explicit types must survive encoding.
					if embeds[0].Type != EmbedTypeRich && (embedType != "" || embeds[0].Type != "") {
						return nil, fmt.Errorf("unexpected request embed type: %q", embeds[0].Type)
					}
					requests.Add(1)
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"result"}`))}, nil
				})

				const workers = 32
				start := make(chan struct{})
				results := make(chan error, workers)
				for range workers {
					go func() {
						<-start
						results <- tt.call(s, embed)
					}()
				}
				close(start)
				for range workers {
					if err := <-results; err != nil {
						t.Error(err)
					}
				}
				if requests.Load() != workers {
					t.Errorf("requests = %d, want %d", requests.Load(), workers)
				}
				if embed.Type != embedType {
					t.Errorf("shared embed type changed to %q, want %q", embed.Type, embedType)
				}
			})
		}
	}
}

func TestRESTLegacyEmbedPreservesSharedPayload(t *testing.T) {
	tests := []struct {
		name string
		edit bool
		call func(*Session, any) error
	}{
		{"send", false, func(s *Session, payload any) error {
			_, err := s.ChannelMessageSendComplex("channel", payload.(*MessageSend))
			return err
		}},
		{"edit", true, func(s *Session, payload any) error {
			_, err := s.ChannelMessageEditComplex(payload.(*MessageEdit))
			return err
		}},
		{"forum", false, func(s *Session, payload any) error {
			_, err := s.ForumThreadStartComplex("channel", &ThreadStart{Name: "thread", AutoArchiveDuration: 60}, payload.(*MessageSend))
			return err
		}},
	}
	for _, tt := range tests {
		for _, conflict := range []bool{false, true} {
			t.Run(tt.name+"/conflict="+strconv.FormatBool(conflict), func(t *testing.T) {
				embed := &MessageEmbed{Title: "Reusable legacy embed"}
				var payload any
				if tt.edit {
					data := &MessageEdit{Channel: "channel", ID: "message", Embed: embed}
					if conflict {
						data.Embeds = &[]*MessageEmbed{embed}
					}
					payload = data
				} else {
					data := &MessageSend{Embed: embed}
					if conflict {
						data.Embeds = []*MessageEmbed{embed}
					}
					payload = data
				}
				before, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				s, err := New("")
				if err != nil {
					t.Fatal(err)
				}
				var requests atomic.Int32
				s.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					requests.Add(1)
					var body struct {
						MessageSend
						Message *MessageSend `json:"message"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					embeds := body.Embeds
					if tt.name == "forum" && body.Message != nil {
						embeds = body.Message.Embeds
					}
					if len(embeds) != 1 || embeds[0].Title != "Reusable legacy embed" {
						return nil, fmt.Errorf("unexpected request embeds: %+v", embeds)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"result"}`))}, nil
				})

				const workers = 16
				start := make(chan struct{})
				results := make(chan error, workers)
				for range workers {
					go func() {
						<-start
						results <- tt.call(s, payload)
					}()
				}
				close(start)
				check := func(err error) {
					t.Helper()
					if conflict {
						if err == nil || err.Error() != "cannot specify both Embed and Embeds" {
							t.Errorf("error = %v, want conflicting embed fields", err)
						}
					} else if err != nil {
						t.Error(err)
					}
				}
				for range workers {
					check(<-results)
				}
				// The same legacy request must remain valid for subsequent calls too.
				check(tt.call(s, payload))
				check(tt.call(s, payload))
				wantRequests := int32(workers + 2)
				if conflict {
					wantRequests = 0
				}
				if requests.Load() != wantRequests {
					t.Errorf("requests = %d, want %d", requests.Load(), wantRequests)
				}
				after, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Errorf("caller payload changed: before %s, after %s", before, after)
				}
			})
		}
	}
}

func scheduledEventExceptionTime(value time.Time) **time.Time {
	pointer := &value
	return &pointer
}

func scheduledEventExceptionBool(value bool) **bool {
	pointer := &value
	return &pointer
}

func scheduledEventExceptionSession(t *testing.T, handler http.HandlerFunc) *Session {
	t.Helper()

	server := httptest.NewServer(handler)
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
	return session
}

func TestGuildScheduledEventExceptionParams(t *testing.T) {
	original := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	start := original.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	var nullTime *time.Time
	var nullBool *bool

	tests := []struct {
		name   string
		params interface{}
		want   string
	}{
		{
			name: "create omits unset overrides",
			params: GuildScheduledEventExceptionCreateParams{
				OriginalScheduledStartTime: original,
			},
			want: `{"original_scheduled_start_time":"2026-07-20T12:00:00Z"}`,
		},
		{
			name: "create sets overrides including false",
			params: GuildScheduledEventExceptionCreateParams{
				OriginalScheduledStartTime: original,
				ScheduledStartTime:         scheduledEventExceptionTime(start),
				ScheduledEndTime:           scheduledEventExceptionTime(end),
				IsCanceled:                 scheduledEventExceptionBool(false),
			},
			want: `{"original_scheduled_start_time":"2026-07-20T12:00:00Z","scheduled_start_time":"2026-07-20T13:00:00Z","scheduled_end_time":"2026-07-20T15:00:00Z","is_canceled":false}`,
		},
		{
			name: "create clears overrides",
			params: GuildScheduledEventExceptionCreateParams{
				OriginalScheduledStartTime: original,
				ScheduledStartTime:         &nullTime,
				ScheduledEndTime:           &nullTime,
				IsCanceled:                 &nullBool,
			},
			want: `{"original_scheduled_start_time":"2026-07-20T12:00:00Z","scheduled_start_time":null,"scheduled_end_time":null,"is_canceled":null}`,
		},
		{
			name:   "edit omits unset fields",
			params: GuildScheduledEventExceptionEditParams{},
			want:   `{}`,
		},
		{
			name: "edit sets and clears fields",
			params: GuildScheduledEventExceptionEditParams{
				ScheduledStartTime: scheduledEventExceptionTime(start),
				ScheduledEndTime:   &nullTime,
				IsCanceled:         scheduledEventExceptionBool(true),
			},
			want: `{"scheduled_start_time":"2026-07-20T13:00:00Z","scheduled_end_time":null,"is_canceled":true}`,
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

func TestGuildScheduledEventExceptionEndpoints(t *testing.T) {
	original := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	start := original.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	request := 0

	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, r *http.Request) {
		request++

		switch request {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/guilds/guild/scheduled-events/event/exceptions" {
				t.Fatalf("request = %s %s, want POST exception collection", r.Method, r.URL.Path)
			}
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode create payload returned error: %v", err)
			}
			if got := string(payload["original_scheduled_start_time"]); got != `"2026-07-20T12:00:00Z"` {
				t.Fatalf("original_scheduled_start_time = %s", got)
			}
			if got := string(payload["scheduled_start_time"]); got != `"2026-07-20T13:00:00Z"` {
				t.Fatalf("scheduled_start_time = %s", got)
			}
			if got := string(payload["is_canceled"]); got != "false" {
				t.Fatalf("is_canceled = %s, want false", got)
			}
			_, _ = w.Write([]byte(`{"event_id":"event","event_exception_id":"exception","scheduled_start_time":"2026-07-20T13:00:00Z","scheduled_end_time":null,"is_canceled":false}`))
		case 2:
			if r.Method != http.MethodPatch || r.URL.Path != "/guilds/guild/scheduled-events/event/exceptions/exception" {
				t.Fatalf("request = %s %s, want PATCH exception", r.Method, r.URL.Path)
			}
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode edit payload returned error: %v", err)
			}
			if got := string(payload["scheduled_end_time"]); got != `"2026-07-20T15:00:00Z"` {
				t.Fatalf("scheduled_end_time = %s", got)
			}
			if got := string(payload["is_canceled"]); got != "true" {
				t.Fatalf("is_canceled = %s, want true", got)
			}
			_, _ = w.Write([]byte(`{"event_id":"event","event_exception_id":"exception","scheduled_start_time":"2026-07-20T13:00:00Z","scheduled_end_time":"2026-07-20T15:00:00Z","is_canceled":true}`))
		case 3:
			if r.Method != http.MethodGet || r.URL.Path != "/guilds/guild/scheduled-events/event/exception/users" {
				t.Fatalf("request = %s %s, want GET exception users", r.Method, r.URL.Path)
			}
			wantQuery := map[string][]string{
				"after":       {"after"},
				"before":      {"before"},
				"limit":       {"25"},
				"with_member": {"true"},
			}
			if got := map[string][]string(r.URL.Query()); !reflect.DeepEqual(got, wantQuery) {
				t.Fatalf("query = %#v, want %#v", got, wantQuery)
			}
			_, _ = w.Write([]byte(`[{"guild_scheduled_event_id":"event","guild_scheduled_event_exception_id":"exception","user_id":"user","user":{"id":"user","username":"Subscriber"},"member":{"roles":[]},"response":1}]`))
		case 4:
			if r.Method != http.MethodGet || r.URL.Path != "/guilds/guild/scheduled-events/event/users/counts" {
				t.Fatalf("request = %s %s, want GET user counts", r.Method, r.URL.Path)
			}
			wantIDs := []string{"exception", "other"}
			if got := r.URL.Query()["guild_scheduled_event_exception_ids"]; !reflect.DeepEqual(got, wantIDs) {
				t.Fatalf("exception ids = %#v, want %#v", got, wantIDs)
			}
			_, _ = w.Write([]byte(`{"guild_scheduled_event_count":12,"guild_scheduled_event_exception_counts":{"exception":4,"other":8}}`))
		case 5:
			if r.Method != http.MethodDelete || r.URL.Path != "/guilds/guild/scheduled-events/event/exceptions/exception" {
				t.Fatalf("request = %s %s, want DELETE exception", r.Method, r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %d: %s %s", request, r.Method, r.URL.Path)
		}
	})

	created, err := session.GuildScheduledEventExceptionCreate("guild", "event", &GuildScheduledEventExceptionCreateParams{
		OriginalScheduledStartTime: original,
		ScheduledStartTime:         scheduledEventExceptionTime(start),
		IsCanceled:                 scheduledEventExceptionBool(false),
	})
	if err != nil {
		t.Fatalf("GuildScheduledEventExceptionCreate returned error: %v", err)
	}
	if created.EventID != "event" || created.EventExceptionID != "exception" || created.ScheduledStartTime == nil || !created.ScheduledStartTime.Equal(start) || created.ScheduledEndTime != nil || created.IsCanceled {
		t.Fatalf("created exception = %#v", created)
	}

	edited, err := session.GuildScheduledEventExceptionEdit("guild", "event", "exception", &GuildScheduledEventExceptionEditParams{
		ScheduledEndTime: scheduledEventExceptionTime(end),
		IsCanceled:       scheduledEventExceptionBool(true),
	})
	if err != nil {
		t.Fatalf("GuildScheduledEventExceptionEdit returned error: %v", err)
	}
	if edited.ScheduledEndTime == nil || !edited.ScheduledEndTime.Equal(end) || !edited.IsCanceled {
		t.Fatalf("edited exception = %#v", edited)
	}

	users, err := session.GuildScheduledEventExceptionUsers("guild", "event", "exception", 25, true, "before", "after")
	if err != nil {
		t.Fatalf("GuildScheduledEventExceptionUsers returned error: %v", err)
	}
	if len(users) != 1 || users[0].GuildScheduledEventExceptionID == nil || *users[0].GuildScheduledEventExceptionID != "exception" || users[0].UserID != "user" || users[0].User == nil || users[0].User.ID != "user" || users[0].Member == nil || users[0].Response != GuildScheduledEventUserResponseInterested {
		t.Fatalf("exception users = %#v", users)
	}

	counts, err := session.GuildScheduledEventUserCounts("guild", "event", []string{"exception", "other"})
	if err != nil {
		t.Fatalf("GuildScheduledEventUserCounts returned error: %v", err)
	}
	if counts.GuildScheduledEventCount != 12 || counts.GuildScheduledEventExceptionCounts["exception"] != 4 || counts.GuildScheduledEventExceptionCounts["other"] != 8 {
		t.Fatalf("counts = %#v", counts)
	}

	if err = session.GuildScheduledEventExceptionDelete("guild", "event", "exception"); err != nil {
		t.Fatalf("GuildScheduledEventExceptionDelete returned error: %v", err)
	}
	if request != 5 {
		t.Fatalf("requests = %d, want 5", request)
	}
}

func TestGuildScheduledEventExceptionNullableResponses(t *testing.T) {
	request := 0
	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, r *http.Request) {
		request++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if rawQuery := r.URL.RawQuery; rawQuery != "" {
			t.Fatalf("query = %q, want empty", rawQuery)
		}

		switch r.URL.Path {
		case "/guilds/guild/scheduled-events/event/exception/users":
			_, _ = w.Write([]byte("null"))
		case "/guilds/guild/scheduled-events/event/users/counts":
			_, _ = w.Write([]byte(`{"guild_scheduled_event_count":0,"guild_scheduled_event_exception_counts":{}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	})

	users, err := session.GuildScheduledEventExceptionUsers("guild", "event", "exception", 0, false, "", "")
	if err != nil {
		t.Fatalf("GuildScheduledEventExceptionUsers returned error: %v", err)
	}
	if users != nil {
		t.Fatalf("users = %#v, want nil", users)
	}

	counts, err := session.GuildScheduledEventUserCounts("guild", "event", nil)
	if err != nil {
		t.Fatalf("GuildScheduledEventUserCounts returned error: %v", err)
	}
	if counts == nil || counts.GuildScheduledEventCount != 0 || counts.GuildScheduledEventExceptionCounts == nil || len(counts.GuildScheduledEventExceptionCounts) != 0 {
		t.Fatalf("counts = %#v", counts)
	}
	if request != 2 {
		t.Fatalf("requests = %d, want 2", request)
	}
}

func TestGuildScheduledEventExceptionValidation(t *testing.T) {
	requests := 0
	session := scheduledEventExceptionSession(t, func(_ http.ResponseWriter, r *http.Request) {
		requests++
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	if _, err := session.GuildScheduledEventExceptionCreate("guild", "event", nil); err == nil {
		t.Fatal("GuildScheduledEventExceptionCreate returned nil error for nil data")
	}
	if _, err := session.GuildScheduledEventExceptionEdit("guild", "event", "exception", nil); err == nil {
		t.Fatal("GuildScheduledEventExceptionEdit returned nil error for nil data")
	}
	if _, err := session.GuildScheduledEventUserCounts("guild", "event", make([]string, 11)); err == nil {
		t.Fatal("GuildScheduledEventUserCounts returned nil error for 11 exception ids")
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestGuildScheduledEventExceptionHTTPFailures(t *testing.T) {
	requests := 0
	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"code":180005,"message":"invalid exception"}`, http.StatusBadRequest)
	})

	original := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "create",
			call: func() error {
				_, err := session.GuildScheduledEventExceptionCreate("guild", "event", &GuildScheduledEventExceptionCreateParams{OriginalScheduledStartTime: original})
				return err
			},
		},
		{
			name: "edit",
			call: func() error {
				_, err := session.GuildScheduledEventExceptionEdit("guild", "event", "exception", &GuildScheduledEventExceptionEditParams{})
				return err
			},
		},
		{
			name: "delete",
			call: func() error {
				return session.GuildScheduledEventExceptionDelete("guild", "event", "exception")
			},
		},
		{
			name: "users",
			call: func() error {
				_, err := session.GuildScheduledEventExceptionUsers("guild", "event", "exception", 0, false, "", "")
				return err
			},
		},
		{
			name: "counts",
			call: func() error {
				_, err := session.GuildScheduledEventUserCounts("guild", "event", nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("call returned nil error")
			}
		})
	}
	if requests != len(tests) {
		t.Fatalf("requests = %d, want %d", requests, len(tests))
	}
}

func TestGuildScheduledEventExceptionMalformedResponses(t *testing.T) {
	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{"))
	})

	original := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "create",
			call: func() error {
				_, err := session.GuildScheduledEventExceptionCreate("guild", "event", &GuildScheduledEventExceptionCreateParams{OriginalScheduledStartTime: original})
				return err
			},
		},
		{
			name: "edit",
			call: func() error {
				_, err := session.GuildScheduledEventExceptionEdit("guild", "event", "exception", &GuildScheduledEventExceptionEditParams{})
				return err
			},
		},
		{
			name: "users",
			call: func() error {
				_, err := session.GuildScheduledEventExceptionUsers("guild", "event", "exception", 0, false, "", "")
				return err
			},
		},
		{
			name: "counts",
			call: func() error {
				_, err := session.GuildScheduledEventUserCounts("guild", "event", nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
				t.Fatalf("error = %v, want JSON error", err)
			}
		})
	}
}

func TestGuildScheduledEventExceptionRejectsNullObjectResponses(t *testing.T) {
	original := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		body string
		call func(*Session) error
	}{
		{
			name: "create exception",
			body: "null",
			call: func(session *Session) error {
				_, err := session.GuildScheduledEventExceptionCreate("guild", "event", &GuildScheduledEventExceptionCreateParams{OriginalScheduledStartTime: original})
				return err
			},
		},
		{
			name: "edit exception",
			body: "null",
			call: func(session *Session) error {
				_, err := session.GuildScheduledEventExceptionEdit("guild", "event", "exception", &GuildScheduledEventExceptionEditParams{})
				return err
			},
		},
		{
			name: "null counts",
			body: "null",
			call: func(session *Session) error {
				_, err := session.GuildScheduledEventUserCounts("guild", "event", nil)
				return err
			},
		},
		{
			name: "missing exception counts",
			body: `{"guild_scheduled_event_count":0,"guild_scheduled_event_exception_counts":null}`,
			call: func(session *Session) error {
				_, err := session.GuildScheduledEventUserCounts("guild", "event", nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			if err := tt.call(session); !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}
}

func TestGuildScheduledEventExceptionFields(t *testing.T) {
	var event GuildScheduledEvent
	if err := json.Unmarshal([]byte(`{
		"id":"event",
		"guild_id":"guild",
		"scheduled_start_time":"2026-07-20T12:00:00Z",
		"guild_scheduled_event_exceptions":[{
			"event_id":"event",
			"event_exception_id":"exception",
			"scheduled_start_time":null,
			"scheduled_end_time":"2026-07-20T14:00:00Z",
			"is_canceled":false
		}],
		"user_rsvp":{
			"guild_scheduled_event_id":"event",
			"guild_scheduled_event_exception_id":null,
			"user_id":"user",
			"response":0
		}
	}`), &event); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if len(event.Exceptions) != 1 || event.Exceptions[0].EventExceptionID != "exception" || event.Exceptions[0].ScheduledStartTime != nil || event.Exceptions[0].ScheduledEndTime == nil {
		t.Fatalf("exceptions = %#v", event.Exceptions)
	}
	if event.UserRSVP == nil || event.UserRSVP.UserID != "user" || event.UserRSVP.GuildScheduledEventExceptionID != nil || event.UserRSVP.Response != GuildScheduledEventUserResponseUninterested {
		t.Fatalf("user rsvp = %#v", event.UserRSVP)
	}
}

func voiceStateString(value string) *string {
	return &value
}

func voiceStateBool(value bool) *bool {
	return &value
}

func voiceStateTime(value time.Time) **time.Time {
	pointer := &value
	return &pointer
}

func newVoiceStateRESTSession(t *testing.T, handler http.Handler) *Session {
	t.Helper()

	server := httptest.NewServer(handler)
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
	return session
}

func TestUserVoiceStateCurrentUserRoute(t *testing.T) {
	session := newVoiceStateRESTSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/guilds/guild/voice-states/@me" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/guilds/guild/voice-states/@me")
		}
		if got := r.Header.Get("X-Voice-Test"); got != "current user" {
			t.Fatalf("X-Voice-Test = %q, want %q", got, "current user")
		}

		_, _ = w.Write([]byte(`{
			"guild_id":"guild",
			"channel_id":"stage",
			"user_id":"current",
			"session_id":"session",
			"deaf":false,
			"mute":false,
			"self_deaf":false,
			"self_mute":false,
			"self_video":false,
			"suppress":true,
			"request_to_speak_timestamp":null
		}`))
	}))

	state, err := session.UserVoiceState("guild", "@me", WithHeader("X-Voice-Test", "current user"))
	if err != nil {
		t.Fatalf("UserVoiceState returned error: %v", err)
	}
	if state == nil || state.GuildID != "guild" || state.ChannelID != "stage" || state.UserID != "current" || !state.Suppress {
		t.Fatalf("state = %#v", state)
	}
}

func TestVoiceStateEditRequests(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 34, 56, 0, time.UTC)
	var nullTime *time.Time

	tests := []struct {
		name string
		path string
		body string
		call func(*Session, ...RequestOption) error
	}{
		{
			name: "current user omits unset fields",
			path: "/guilds/guild/voice-states/@me",
			body: `{}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{}, options...)
			},
		},
		{
			name: "current user sends values including false",
			path: "/guilds/guild/voice-states/@me",
			body: `{"channel_id":"stage","suppress":false,"request_to_speak_timestamp":"2026-07-13T12:34:56Z"}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{
					ChannelID:               voiceStateString("stage"),
					Suppress:                voiceStateBool(false),
					RequestToSpeakTimestamp: voiceStateTime(timestamp),
				}, options...)
			},
		},
		{
			name: "current user clears request to speak",
			path: "/guilds/guild/voice-states/@me",
			body: `{"request_to_speak_timestamp":null}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{
					RequestToSpeakTimestamp: &nullTime,
				}, options...)
			},
		},
		{
			name: "user omits unset fields",
			path: "/guilds/guild/voice-states/user",
			body: `{}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.UserVoiceStateEdit("guild", "user", &UserVoiceStateEditParams{}, options...)
			},
		},
		{
			name: "user sends values including false",
			path: "/guilds/guild/voice-states/user",
			body: `{"channel_id":"stage","suppress":false}`,
			call: func(session *Session, options ...RequestOption) error {
				return session.UserVoiceStateEdit("guild", "user", &UserVoiceStateEditParams{
					ChannelID: voiceStateString("stage"),
					Suppress:  voiceStateBool(false),
				}, options...)
			},
		},
	}

	session := newVoiceStateRESTSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}

		name := r.Header.Get("X-Voice-Test")
		var expectedPath, expectedBody string
		for _, tt := range tests {
			if tt.name == name {
				expectedPath = tt.path
				expectedBody = tt.body
				break
			}
		}
		if expectedPath == "" {
			t.Fatalf("unexpected X-Voice-Test %q", name)
		}
		if r.URL.Path != expectedPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, expectedPath)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		assertJSONEqual(t, body, []byte(expectedBody))
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(session, WithHeader("X-Voice-Test", tt.name)); err != nil {
				t.Fatalf("voice state edit returned error: %v", err)
			}
		})
	}
}

func TestVoiceStateEditErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Session, ...RequestOption) error
	}{
		{
			name: "current user",
			path: "/guilds/guild/voice-states/@me",
			call: func(session *Session, options ...RequestOption) error {
				return session.CurrentUserVoiceStateEdit("guild", &CurrentUserVoiceStateEditParams{}, options...)
			},
		},
		{
			name: "user",
			path: "/guilds/guild/voice-states/user",
			call: func(session *Session, options ...RequestOption) error {
				return session.UserVoiceStateEdit("guild", "user", &UserVoiceStateEditParams{}, options...)
			},
		},
	}

	session := newVoiceStateRESTSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}

		name := r.Header.Get("X-Voice-Test")
		var expectedPath string
		for _, tt := range tests {
			if tt.name == name {
				expectedPath = tt.path
				break
			}
		}
		if expectedPath == "" {
			t.Fatalf("unexpected X-Voice-Test %q", name)
		}
		if r.URL.Path != expectedPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, expectedPath)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":50035,"message":"Invalid Form Body"}`))
	}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(session, WithHeader("X-Voice-Test", tt.name))
			if err == nil {
				t.Fatal("voice state edit returned nil error")
			}

			var restErr *RESTError
			if !errors.As(err, &restErr) {
				t.Fatalf("error = %T, want *RESTError", err)
			}
			if restErr.Response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", restErr.Response.StatusCode, http.StatusBadRequest)
			}
		})
	}
}
