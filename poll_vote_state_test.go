package discordgo

import (
	"errors"
	"sync"
	"testing"
)

func TestMessagePollVoteCreatesMissingZeroCount(t *testing.T) {
	for _, self := range []bool{false, true} {
		for _, sparse := range []bool{false, true} {
			results := &PollResults{}
			if sparse {
				results.AnswerCounts = []*PollAnswerCount{{ID: 2, Count: 4}}
			}
			state, session := newMessagePollVoteTestState(t, &Poll{
				Answers: []PollAnswer{{AnswerID: 1}, {AnswerID: 2}}, Results: results,
			})
			before, err := state.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			userID := "user"
			if self {
				userID = "bot"
			}
			if err := state.OnInterface(session, &MessagePollVoteAdd{
				UserID: userID, ChannelID: "channel", MessageID: "message", AnswerID: 1,
			}); err != nil {
				t.Fatal(err)
			}
			got, err := state.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			var added *PollAnswerCount
			for _, count := range got.Poll.Results.AnswerCounts {
				if count.ID == 1 {
					added = count
				}
			}
			if added == nil || added.Count != 1 || added.MeVoted != self {
				t.Fatalf("self=%v sparse=%v: first vote = %#v, want count 1 and me_voted %v", self, sparse, added, self)
			}
			wantOldCount := 0
			if sparse {
				wantOldCount = 1
			}
			if len(before.Poll.Results.AnswerCounts) != wantOldCount || before.Poll.Results != results {
				t.Fatal("first vote mutated old snapshot")
			}
			if sparse && got.Poll.Results.AnswerCounts[0].Count != 4 {
				t.Fatal("unrelated count changed")
			}
			if err := state.OnInterface(session, &MessagePollVoteRemove{
				UserID: userID, ChannelID: "channel", MessageID: "message", AnswerID: 1,
			}); err != nil {
				t.Fatal(err)
			}
			after, err := state.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			for _, count := range after.Poll.Results.AnswerCounts {
				if count.ID == 1 && (count.Count != 0 || count.MeVoted) {
					t.Fatalf("removed vote = %#v", count)
				}
			}
			if added.Count != 1 {
				t.Fatal("removal mutated preceding snapshot")
			}
		}
	}
}

func TestMessagePollVoteDoesNotInventUnknownCounts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		results  *PollResults
		answerID int
		adding   bool
	}{
		{"unknown results", nil, 1, true},
		{"unknown answer", &PollResults{}, 99, true},
		{"remove zero votes", &PollResults{}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, _ := newMessagePollVoteTestState(t, &Poll{
				Answers: []PollAnswer{{AnswerID: 1}}, Results: tc.results,
			})
			before, err := state.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			if err := state.messagePollVoteUpdate("channel", "message", "user", tc.answerID, tc.adding); err != nil {
				t.Fatal(err)
			}
			after, err := state.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("vote event replaced unchanged snapshot")
			}
			if tc.results != nil && len(after.Poll.Results.AnswerCounts) != 0 {
				t.Fatal("vote event invented a count")
			}
		})
	}
}

func TestMessagePollVoteStateLifecycle(t *testing.T) {
	state, session := newMessagePollVoteTestState(t, &Poll{Results: &PollResults{
		AnswerCounts: []*PollAnswerCount{{ID: 1, Count: 2}, {ID: 2, Count: 7}},
	}})

	guildSnapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	snapshot, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}

	add := &MessagePollVoteAdd{
		UserID: "user", ChannelID: "channel", MessageID: "message", GuildID: "guild", AnswerID: 1,
	}
	addBefore := *add
	if err = state.OnInterface(session, add); err != nil {
		t.Fatalf("OnInterface(add) returned error: %v", err)
	}
	assertMessagePollVoteState(t, state, 3, false)
	if *add != addBefore {
		t.Fatalf("add event was mutated: %#v", add)
	}

	selfAdd := &MessagePollVoteAdd{
		UserID: "bot", ChannelID: "channel", MessageID: "message", GuildID: "guild", AnswerID: 1,
	}
	if err = state.OnInterface(session, selfAdd); err != nil {
		t.Fatalf("OnInterface(self add) returned error: %v", err)
	}
	assertMessagePollVoteState(t, state, 4, true)

	remove := &MessagePollVoteRemove{
		UserID: "user", ChannelID: "channel", MessageID: "message", GuildID: "guild", AnswerID: 1,
	}
	removeBefore := *remove
	if err = state.OnInterface(session, remove); err != nil {
		t.Fatalf("OnInterface(remove) returned error: %v", err)
	}
	assertMessagePollVoteState(t, state, 3, true)
	if *remove != removeBefore {
		t.Fatalf("remove event was mutated: %#v", remove)
	}

	selfRemove := &MessagePollVoteRemove{
		UserID: "bot", ChannelID: "channel", MessageID: "message", GuildID: "guild", AnswerID: 1,
	}
	if err = state.OnInterface(session, selfRemove); err != nil {
		t.Fatalf("OnInterface(self remove) returned error: %v", err)
	}
	assertMessagePollVoteState(t, state, 2, false)

	answer := snapshot.Poll.Results.AnswerCounts[0]
	if answer.Count != 2 || answer.MeVoted {
		t.Fatalf("held message snapshot was mutated: %#v", answer)
	}
	current, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if current.Poll.Results.AnswerCounts[1].Count != 7 {
		t.Fatalf("unmatched answer count = %d, want 7", current.Poll.Results.AnswerCounts[1].Count)
	}
	currentGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if currentGuild == guildSnapshot || currentGuild.Channels[0] == guildSnapshot.Channels[0] {
		t.Fatal("poll vote update reused the guild channel snapshot")
	}
	if currentGuild.Channels[0] != state.channelMap["channel"] {
		t.Fatal("guild channel and channel map do not reference the replacement")
	}
}

func TestMessagePollVoteStateClampsRemovalAtZero(t *testing.T) {
	state, session := newMessagePollVoteTestState(t, &Poll{Results: &PollResults{
		AnswerCounts: []*PollAnswerCount{{ID: 1, MeVoted: true}},
	}})

	remove := &MessagePollVoteRemove{
		UserID: "bot", ChannelID: "channel", MessageID: "message", AnswerID: 1,
	}
	if err := state.OnInterface(session, remove); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	assertMessagePollVoteState(t, state, 0, false)
}

func TestMessagePollVoteStateIgnoresMissingCacheData(t *testing.T) {
	tests := []struct {
		name  string
		poll  *Poll
		event interface{}
	}{
		{
			name: "unknown channel",
			poll: &Poll{Results: &PollResults{AnswerCounts: []*PollAnswerCount{{ID: 1, Count: 2}}}},
			event: &MessagePollVoteAdd{
				UserID: "user", ChannelID: "unknown", MessageID: "message", AnswerID: 1,
			},
		},
		{
			name: "unknown message",
			poll: &Poll{Results: &PollResults{AnswerCounts: []*PollAnswerCount{{ID: 1, Count: 2}}}},
			event: &MessagePollVoteRemove{
				UserID: "user", ChannelID: "channel", MessageID: "unknown", AnswerID: 1,
			},
		},
		{
			name: "nil poll",
			event: &MessagePollVoteAdd{
				UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
			},
		},
		{
			name: "nil results",
			poll: &Poll{},
			event: &MessagePollVoteAdd{
				UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
			},
		},
		{
			name: "nil answer counts",
			poll: &Poll{Results: &PollResults{}},
			event: &MessagePollVoteRemove{
				UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
			},
		},
		{
			name: "nil and unmatched answers",
			poll: &Poll{Results: &PollResults{AnswerCounts: []*PollAnswerCount{
				nil,
				{ID: 2, Count: 4},
			}}},
			event: &MessagePollVoteAdd{
				UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, session := newMessagePollVoteTestState(t, tt.poll)
			if err := state.OnInterface(session, tt.event); err != nil {
				t.Fatalf("OnInterface returned error: %v", err)
			}
		})
	}

	t.Run("nil cached message", func(t *testing.T) {
		state := NewState()
		state.MaxMessageCount = 10
		if err := state.GuildAdd(&Guild{
			ID: "guild",
			Channels: []*Channel{{
				ID: "channel", GuildID: "guild", Messages: []*Message{nil},
			}},
		}); err != nil {
			t.Fatalf("GuildAdd returned error: %v", err)
		}
		event := &MessagePollVoteAdd{
			UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
		}
		if err := state.OnInterface(&Session{StateEnabled: true, State: state}, event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	})
}

func TestMessagePollVoteStateRejectsMalformedEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{name: "nil add", event: (*MessagePollVoteAdd)(nil)},
		{name: "nil remove", event: (*MessagePollVoteRemove)(nil)},
		{name: "add missing user", event: &MessagePollVoteAdd{ChannelID: "channel", MessageID: "message", AnswerID: 1}},
		{name: "add missing channel", event: &MessagePollVoteAdd{UserID: "user", MessageID: "message", AnswerID: 1}},
		{name: "remove missing message", event: &MessagePollVoteRemove{UserID: "user", ChannelID: "channel", AnswerID: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, session := newMessagePollVoteTestState(t, &Poll{Results: &PollResults{}})
			if err := state.OnInterface(session, tt.event); !errors.Is(err, ErrStateInvalidData) {
				t.Fatalf("OnInterface returned error %v, want %v", err, ErrStateInvalidData)
			}
		})
	}
}

func TestMessagePollVoteStateMutatorsDoNotRaceSnapshots(t *testing.T) {
	state, session := newMessagePollVoteTestState(t, &Poll{Results: &PollResults{
		AnswerCounts: []*PollAnswerCount{{ID: 1}},
	}})

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}

			message, err := state.Message("channel", "message")
			if err == nil && message.Poll != nil && message.Poll.Results != nil {
				for _, answer := range message.Poll.Results.AnswerCounts {
					if answer != nil {
						_ = answer.Count
						_ = answer.MeVoted
					}
				}
			}
		}
	}()

	for i := 0; i < 500; i++ {
		add := &MessagePollVoteAdd{
			UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
		}
		if err := state.OnInterface(session, add); err != nil {
			t.Fatalf("OnInterface(add) returned error: %v", err)
		}
		remove := &MessagePollVoteRemove{
			UserID: "user", ChannelID: "channel", MessageID: "message", AnswerID: 1,
		}
		if err := state.OnInterface(session, remove); err != nil {
			t.Fatalf("OnInterface(remove) returned error: %v", err)
		}
	}

	close(done)
	wg.Wait()
}

func newMessagePollVoteTestState(t *testing.T, poll *Poll) (*State, *Session) {
	t.Helper()
	state := NewState()
	state.MaxMessageCount = 10
	state.User = &User{ID: "bot"}
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{{
			ID: "channel", GuildID: "guild", Type: ChannelTypeGuildText,
		}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{
		ID: "message", ChannelID: "channel", Poll: poll,
	}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}
	return state, &Session{StateEnabled: true, State: state}
}

func assertMessagePollVoteState(t *testing.T, state *State, count int, meVoted bool) {
	t.Helper()
	message, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	answer := message.Poll.Results.AnswerCounts[0]
	if answer.Count != count || answer.MeVoted != meVoted {
		t.Fatalf("answer state = (%d, %t), want (%d, %t)", answer.Count, answer.MeVoted, count, meVoted)
	}
}
