package discordgo

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

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
	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, r *http.Request) {
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
	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, r *http.Request) {
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
	session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, r *http.Request) {
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
			session := scheduledEventExceptionSession(t, func(w http.ResponseWriter, r *http.Request) {
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
