package discordgo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
