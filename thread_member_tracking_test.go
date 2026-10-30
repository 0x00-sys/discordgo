package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestThreadCreateHonorsMemberTracking(t *testing.T) {
	for _, track := range []bool{false, true} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("tracking=%t/cached=%t", track, existing), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.State.TrackThreadMembers = track
				guild := &Guild{ID: "100"}
				var before *Channel
				if existing {
					// THREAD_CREATE also arrives when joining a known thread.
					before = &Channel{ID: "200", GuildID: "100", ParentID: "150", Type: ChannelTypeGuildPublicThread, Name: "public-thread"}
					guild.Threads = []*Channel{before}
				}
				if err := session.State.GuildAdd(guild); err != nil {
					t.Fatal(err)
				}
				if existing {
					before, err = session.State.Channel("200")
					if err != nil {
						t.Fatal(err)
					}
				}
				callbacks := 0
				session.AddHandler(func(s *Session, event *ThreadCreate) {
					callbacks++
					checkCurrentThreadMember(t, event.Channel)
					cached, err := s.State.Channel(event.ID)
					if err != nil {
						t.Fatal(err)
					}
					if track {
						checkCurrentThreadMember(t, cached)
					} else if cached.Member != nil || cached.Members != nil {
						t.Error("callback sees cached thread membership with tracking disabled")
					}
				})
				thread := `{"id":"200","guild_id":"100","parent_id":"150","type":11,"name":"public-thread","thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false},"member":{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1}}`
				if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"THREAD_CREATE","d":`+thread+`}`)); err != nil {
					t.Fatal(err)
				}
				cached, err := session.State.Channel("200")
				if err != nil {
					t.Fatal(err)
				}
				cachedGuild, err := session.State.Guild("100")
				if err != nil || len(cachedGuild.Threads) != 1 || cachedGuild.Threads[0] != cached {
					t.Fatalf("guild thread cache disagrees: %v", err)
				}
				if before != nil && (cached == before || before.Member != nil || before.Members != nil) {
					t.Error("thread create changed the previous snapshot")
				}
				if callbacks != 1 {
					t.Fatalf("thread callbacks = %d, want 1", callbacks)
				}
			})
		}
	}
}
