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
