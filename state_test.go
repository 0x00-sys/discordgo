package discordgo

import (
	"errors"
	"testing"
)

func TestPresenceAddRequiresUser(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	if err := state.PresenceAdd("guild", &Presence{}); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("PresenceAdd returned error %v, want %v", err, ErrStateInvalidData)
	}
}

func TestPresenceAddSkipsMalformedCachedPresences(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Presences: []*Presence{
			nil,
			{},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.PresenceAdd("guild", &Presence{
		User:   &User{ID: "user"},
		Status: StatusOnline,
	})
	if err != nil {
		t.Fatalf("PresenceAdd returned error: %v", err)
	}

	presence, err := state.Presence("guild", "user")
	if err != nil {
		t.Fatalf("Presence returned error: %v", err)
	}
	if presence.Status != StatusOnline {
		t.Fatalf("Presence status = %q, want %q", presence.Status, StatusOnline)
	}
}

func TestPresenceReadUsesStateLock(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Presences: []*Presence{
			{
				User:   &User{ID: "user"},
				Status: StatusOnline,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < 10000; i++ {
			err := state.PresenceAdd("guild", &Presence{
				User:   &User{ID: "user"},
				Status: StatusIdle,
			})
			if err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()

	for i := 0; i < 10000; i++ {
		presence, err := state.Presence("guild", "user")
		if err != nil {
			t.Fatalf("Presence returned error: %v", err)
		}
		if presence == nil {
			t.Fatal("Presence returned nil presence")
		}
	}

	if err := <-errCh; err != nil {
		t.Fatalf("PresenceAdd returned error: %v", err)
	}
}

func TestPresenceUpdateRequiresUserForMemberTracking(t *testing.T) {
	state := NewState()
	state.TrackPresences = false
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.OnInterface(&Session{StateEnabled: true}, &PresenceUpdate{
		GuildID: "guild",
		Presence: Presence{
			Status: StatusOnline,
		},
	})
	if !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("OnInterface returned error %v, want %v", err, ErrStateInvalidData)
	}
}

func TestGuildMemberUpdateBeforeUpdateClonesUser(t *testing.T) {
	state := NewState()
	oldUser := &User{
		ID:       "user",
		Username: "old",
	}
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{
			{
				GuildID: "guild",
				User:    oldUser,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	update := &GuildMemberUpdate{
		Member: &Member{
			GuildID: "guild",
			User: &User{
				ID:       "user",
				Username: "new",
			},
		},
	}
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if update.BeforeUpdate == nil {
		t.Fatal("BeforeUpdate was not set")
	}
	if update.BeforeUpdate.User == nil {
		t.Fatal("BeforeUpdate.User was not set")
	}
	if update.BeforeUpdate.User == oldUser {
		t.Fatal("BeforeUpdate.User aliases the previously cached user")
	}

	oldUser.Username = "mutated"
	if update.BeforeUpdate.User.Username != "old" {
		t.Fatalf("BeforeUpdate.User.Username = %q, want %q", update.BeforeUpdate.User.Username, "old")
	}

	member, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}
	if member.User.Username != "new" {
		t.Fatalf("cached member username = %q, want %q", member.User.Username, "new")
	}
}
