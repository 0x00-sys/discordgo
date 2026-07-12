package discordgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		t.Errorf(err.Error())
	}

	// TODO make sure the channel was added
}

func TestUserGuilds(t *testing.T) {
	if dg == nil {
		t.Skip("Cannot TestUserGuilds, dg not set.")
	}

	_, err := dg.UserGuilds(10, "", "", false)
	if err != nil {
		t.Errorf(err.Error())
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
		if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "raid cleanup" {
			t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "raid cleanup")
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
			if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "new sticker" {
				t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "new sticker")
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
			contents, err := ioutil.ReadAll(file)
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
			if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "community refresh" {
				t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "community refresh")
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
		contents, err := ioutil.ReadAll(file)
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
			contents, err := ioutil.ReadAll(file)
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
			_, _ = w.Write([]byte(`{"id":"app","name":"Application","event_webhooks_status":3,"team":{"id":"team","members":[{"role":"admin","permissions":["*"]}]}}`))
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
				"event_webhooks_types":      `[]`,
				"interactions_endpoint_url": `""`,
			} {
				if got := string(payload[key]); got != want {
					t.Fatalf("%s = %s, want %s", key, got, want)
				}
			}
			if _, ok := payload["custom_install_url"]; ok {
				t.Fatal("custom_install_url was included when unset")
			}
			_, _ = w.Write([]byte(`{"id":"app","name":"Application","description":"updated","event_webhooks_status":2}`))
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
	if application.EventWebhooksStatus != ApplicationEventWebhookStatusDisabledByDiscord {
		t.Fatalf("EventWebhooksStatus = %d", application.EventWebhooksStatus)
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
	eventTypes := []string{}
	status := ApplicationEventWebhookStatusEnabled
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
	if application.Description != description || application.EventWebhooksStatus != status {
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
		contents, err := ioutil.ReadAll(file)
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
				body, err := ioutil.ReadAll(r.Body)
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

func TestSessionAllowedMentionsDoesNotOverridePayload(t *testing.T) {
	content := "@everyone hello"

	session, err := New("Bot secret")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.AllowedMentions = &MessageAllowedMentions{Parse: []AllowedMentionType{}}

	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, err := ioutil.ReadAll(r.Body)
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
				requestBody, err := ioutil.ReadAll(r.Body)
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
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`server error`)),
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
			Body:    ioutil.NopCloser(strings.NewReader(`{"items":[],"has_more":false}`)),
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
	body, err := ioutil.ReadAll(originalBody)
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
	reader *strings.Reader
	closed chan struct{}
}

func (r *closeNotifyReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *closeNotifyReadCloser) Close() error {
	close(r.closed)
	return nil
}
