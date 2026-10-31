package discordgo

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestScheduledEventUpdatePreservesOmittedUserCount(t *testing.T) {
	for _, tc := range []struct {
		name         string
		countJSON    string
		wantCount    int
		incoming     int
		disableState bool
		programmatic bool
		uncached     bool
	}{
		{name: "omitted", wantCount: 2},
		{name: "explicit_zero", countJSON: `,"user_count":0`},
		{name: "explicit_count", countJSON: `,"user_count":5`, incoming: 5, wantCount: 5},
		{name: "malformed_count", countJSON: `,"user_count":"invalid"`},
		{name: "state_disabled", wantCount: 2, disableState: true},
		{name: "programmatic_zero", programmatic: true},
		{name: "uncached", uncached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			guild := &Guild{ID: "100"}
			if !tc.uncached {
				guild.GuildScheduledEvents = []*GuildScheduledEvent{{ID: "200", GuildID: "100", Name: "before", UserCount: 1}}
			}
			if err := session.State.GuildAdd(guild); err != nil {
				t.Fatal(err)
			}
			// Establish the known count through the subscriber event path.
			_, _ = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"t":"GUILD_SCHEDULED_EVENT_USER_ADD","d":{"guild_id":"100","guild_scheduled_event_id":"200","user_id":"300"}}`))
			before, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			if !tc.uncached && before.GuildScheduledEvents[0].UserCount != 2 {
				t.Fatal("subscriber event did not establish the initial count")
			}
			session.StateEnabled = !tc.disableState
			called := false
			var retained *GuildScheduledEvent
			session.AddHandler(func(s *Session, update *GuildScheduledEventUpdate) {
				called = true
				if update.GuildScheduledEvent == nil || update.ID != "200" || update.GuildID != "100" || update.Name != "updated" || update.UserCount != tc.incoming {
					t.Error("decoding or cache merge changed the incoming event")
				}
				if tc.disableState || tc.uncached {
					if update.BeforeUpdate != nil {
						t.Error("unexpected BeforeUpdate")
					}
				} else if update.BeforeUpdate == nil || update.BeforeUpdate.UserCount != 2 || update.BeforeUpdate.Name != "before" {
					t.Error("BeforeUpdate did not retain the previous event")
				}
				current, err := s.State.Guild("100")
				if err != nil || len(current.GuildScheduledEvents) != 1 {
					t.Errorf("cached event missing: %v", err)
					return
				}
				retained = current.GuildScheduledEvents[0]
				wantName := "updated"
				if tc.disableState {
					wantName = "before"
				}
				if retained.UserCount != tc.wantCount || retained.Name != wantName {
					t.Errorf("cached count/name = %d/%q, want %d/%q", retained.UserCount, retained.Name, tc.wantCount, wantName)
				}
			})
			if tc.programmatic {
				session.handleEvent(guildScheduledEventUpdateEventType, &GuildScheduledEventUpdate{GuildScheduledEvent: &GuildScheduledEvent{ID: "200", GuildID: "100", Name: "updated"}})
			} else {
				payload := fmt.Sprintf(`{"op":0,"t":"GUILD_SCHEDULED_EVENT_UPDATE","d":{"id":"200","guild_id":"100","channel_id":null,"name":"updated","scheduled_start_time":"2030-01-01T20:00:00Z","scheduled_end_time":"2030-01-01T21:00:00Z","privacy_level":2,"status":1,"entity_type":3,"entity_id":null,"entity_metadata":{"location":"somewhere"},"recurrence_rule":null%s}}`, tc.countJSON)
				_, _ = session.onEvent(websocket.TextMessage, []byte(payload))
			}
			if !called || retained == nil {
				t.Fatal("scheduled event callback did not observe the cache")
			}
			retainedCount := retained.UserCount
			_, _ = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"t":"GUILD_SCHEDULED_EVENT_USER_ADD","d":{"guild_id":"100","guild_scheduled_event_id":"200","user_id":"400"}}`))
			after, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			wantAfter := tc.wantCount + 1
			if tc.disableState {
				wantAfter = tc.wantCount
			}
			if after.GuildScheduledEvents[0].UserCount != wantAfter {
				t.Errorf("count after subscription = %d, want %d", after.GuildScheduledEvents[0].UserCount, wantAfter)
			}
			if retained.UserCount != retainedCount || (!tc.uncached && before.GuildScheduledEvents[0].UserCount != 2) {
				t.Error("later cache updates changed a retained snapshot")
			}
		})
	}
}
