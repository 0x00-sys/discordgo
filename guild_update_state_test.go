package discordgo

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGuildUpdatePreservesCreateOnlyFields(t *testing.T) {
	state := NewState()
	session := &Session{StateEnabled: true}
	joined := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := state.OnInterface(session, &GuildCreate{Guild: &Guild{
		ID: "guild", Name: "before", JoinedAt: joined, Large: true, MemberCount: 300,
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := state.Guild("guild")
	if err != nil {
		t.Fatal(err)
	}
	var update GuildUpdate
	if err := json.Unmarshal([]byte(`{"id":"guild","name":"after"}`), &update); err != nil {
		t.Fatal(err)
	}
	if err := state.OnInterface(session, &update); err != nil {
		t.Fatal(err)
	}
	current, err := state.Guild("guild")
	if err != nil {
		t.Fatal(err)
	}
	if current.JoinedAt != joined || !current.Large || current.MemberCount != 300 {
		t.Errorf("guild update lost create fields: joined=%v, large=%v, members=%d", current.JoinedAt, current.Large, current.MemberCount)
	}
	if current.Name != "after" || before.Name != "before" || before.JoinedAt != joined || !before.Large {
		t.Error("guild update failed to replace the snapshot")
	}
	if !update.JoinedAt.IsZero() || update.Large {
		t.Error("cached fields were copied into the event payload")
	}

	// Only a new GUILD_CREATE signals recovery and refreshes these fields.
	if err := state.OnInterface(session, &GuildDelete{Guild: &Guild{ID: "guild", Unavailable: true}}); err != nil {
		t.Fatal(err)
	}
	if err := state.OnInterface(session, &update); err != nil {
		t.Fatal(err)
	}
	current, err = state.Guild("guild")
	if err != nil {
		t.Fatal(err)
	}
	if !current.Unavailable {
		t.Error("guild update cleared unavailable state before recovery")
	}
	rejoined := joined.Add(time.Hour)
	if err := state.OnInterface(session, &GuildCreate{Guild: &Guild{
		ID: "guild", Name: "recovered", JoinedAt: rejoined, Large: false, MemberCount: 10,
	}}); err != nil {
		t.Fatal(err)
	}
	current, err = state.Guild("guild")
	if err != nil {
		t.Fatal(err)
	}
	if current.JoinedAt != rejoined || current.Large || current.Unavailable || current.MemberCount != 10 {
		t.Errorf("guild create did not refresh metadata: %+v", current)
	}
}
