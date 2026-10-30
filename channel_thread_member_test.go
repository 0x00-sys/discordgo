package discordgo

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const channelWithThreadMember = `{
	"id":"200", "guild_id":"100", "parent_id":"150", "type":12,
	"name":"private-thread", "owner_id":"300",
	"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false,"invitable":true},
	"member":{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1}
}`

func checkCurrentThreadMember(t *testing.T, channel *Channel) {
	t.Helper()
	if channel == nil || channel.Member == nil {
		t.Fatal("channel lost the current user's thread membership")
	}
	member := channel.Member
	if member.ID != "200" || member.UserID != "300" || member.Flags != 1 || !member.JoinTimestamp.Equal(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected thread member: %+v", member)
	}
}

func TestChannelIncludesCurrentThreadMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v"+APIVersion+"/channels/200" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(channelWithThreadMember)), Request: req}, nil
	})
	channel, err := session.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	checkCurrentThreadMember(t, channel)
}

func TestThreadCreateIncludesCurrentMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	var received *ThreadCreate
	session.AddHandler(func(_ *Session, event *ThreadCreate) { received = event })
	_, err = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"THREAD_CREATE","d":`+channelWithThreadMember+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if received == nil {
		t.Fatal("THREAD_CREATE handler was not called")
	}
	t.Run("callback", func(t *testing.T) { checkCurrentThreadMember(t, received.Channel) })
	cached, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("cache", func(t *testing.T) { checkCurrentThreadMember(t, cached) })
}
