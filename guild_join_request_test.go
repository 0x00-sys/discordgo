package discordgo

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	session := guildJoinRequestSession(t, func(w http.ResponseWriter, r *http.Request) {
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
