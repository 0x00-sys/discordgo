package discordgo

import "testing"

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
