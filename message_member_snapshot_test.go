package discordgo

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestMessageUpdatePreservesMemberSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		author bool
	}{
		{name: "author still omitted", fields: `"content":"edited"`},
		{name: "author arrives", fields: `"author":{"id":"author","username":"Author"}`, author: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			session.State = newMessageUpdateState(t, nil)
			// An update for an uncached message can carry a partial member
			// before the cache knows the message's author.
			initial := `{"op":0,"t":"MESSAGE_UPDATE","s":1,"d":{"id":"message","channel_id":"channel","guild_id":"guild","member":{"nick":"Nick","roles":[],"joined_at":"2025-01-01T00:00:00Z"}}}`
			if _, err := session.onEvent(websocket.TextMessage, []byte(initial)); err != nil {
				t.Fatal(err)
			}
			before, err := session.State.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			if before.Member == nil || before.Member.User != nil || before.Member.GuildID != "guild" {
				t.Fatalf("unexpected initial member: %#v", before.Member)
			}
			var event *MessageUpdate
			session.AddHandler(func(_ *Session, update *MessageUpdate) { event = update })

			// Cached messages are snapshots. Reading an earlier member must
			// remain safe while another gateway update fills omitted fields.
			start := make(chan struct{})
			readDone := make(chan int, 1)
			go func() {
				<-start
				observed := 0
				for range 1000 {
					if before.Member.User != nil {
						observed++
					}
				}
				readDone <- observed
			}()
			close(start)
			payload := `{"op":0,"t":"MESSAGE_UPDATE","s":2,"d":{"id":"message","channel_id":"channel","guild_id":"guild",` + tc.fields + `}}`
			_, updateErr := session.onEvent(websocket.TextMessage, []byte(payload))
			observed := <-readDone
			if updateErr != nil {
				t.Fatal(updateErr)
			}
			after, err := session.State.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			if observed != 0 || before.Member.User != nil || event == nil || event.BeforeUpdate == nil || event.BeforeUpdate.Member == nil || event.BeforeUpdate.Member.User != nil {
				t.Fatal("message update mutated the member in an earlier snapshot")
			}
			if after.Member == nil || after.Member.Nick != "Nick" || after.Member.GuildID != "guild" {
				t.Fatalf("updated message lost member data: %#v", after.Member)
			}
			if tc.author && (after.Author == nil || after.Member.User != after.Author) {
				t.Fatal("updated member was not linked to the newly received author")
			}
			if !tc.author && (after.Author != nil || after.Member.User != nil) {
				t.Fatal("update invented an omitted author")
			}
		})
	}
}
