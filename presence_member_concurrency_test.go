package discordgo

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
)

func TestPresenceUpdatePreservesConcurrentMemberUpdates(t *testing.T) {
	for _, trackPresences := range []bool{false, true} {
		t.Run(fmt.Sprintf("presences=%t", trackPresences), func(t *testing.T) {
			state := NewState()
			state.TrackPresences = trackPresences
			if err := state.GuildAdd(&Guild{ID: "guild", Members: []*Member{{GuildID: "guild", User: &User{ID: "user"}, Nick: "0"}}}); err != nil {
				t.Fatal(err)
			}
			session := &Session{StateEnabled: true}
			start := make(chan struct{})
			results := make(chan error, 1)
			var workers sync.WaitGroup
			workers.Go(func() {
				<-start
				for range 2000 {
					if err := state.OnInterface(session, &PresenceUpdate{
						GuildID:  "guild",
						Presence: Presence{User: &User{ID: "user", Username: "online-user"}, Status: StatusOnline},
					}); err != nil {
						results <- err
						return
					}
				}
			})
			close(start)
			var firstErr error
			for i := 1; i <= 2000; i++ {
				nick := strconv.Itoa(i)
				if err := state.MemberAdd(&Member{GuildID: "guild", User: &User{ID: "user"}, Nick: nick}); err != nil {
					firstErr = err
					break
				}
				member, err := state.Member("guild", "user")
				if err != nil {
					firstErr = err
					break
				}
				// Presence updates do not contain or intentionally change a member's nickname.
				if member.Nick != nick && firstErr == nil {
					firstErr = fmt.Errorf("presence overwrote committed nickname %q with %q", nick, member.Nick)
				}
			}
			workers.Wait()
			close(results)
			for err := range results {
				t.Error(err)
			}
			if firstErr != nil {
				t.Error(firstErr)
			}
			member, err := state.Member("guild", "user")
			if err != nil {
				t.Fatal(err)
			}
			if member.Nick != "2000" {
				t.Errorf("final nickname = %q, want 2000", member.Nick)
			}
		})
	}
}
