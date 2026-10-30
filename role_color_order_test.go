package discordgo

import "testing"

func TestColorHelpersOrderEqualPositionRolesByID(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles [2]Role
		want  int
	}{
		{
			name: "equal positions",
			roles: [2]Role{
				{ID: "200000000000000000", Position: 1, Color: 0x123456},
				{ID: "300000000000000000", Position: 1, Color: 0x654321},
			},
			want: 0x123456,
		},
		{
			name: "different ID lengths",
			roles: [2]Role{
				{ID: "99000000000000000", Position: 1, Color: 0x123456},
				{ID: "100000000000000000", Position: 1, Color: 0x654321},
			},
			want: 0x123456,
		},
		{
			name: "position takes precedence",
			roles: [2]Role{
				{ID: "200000000000000000", Position: 1, Color: 0x123456},
				{ID: "300000000000000000", Position: 2, Color: 0x654321},
			},
			want: 0x654321,
		},
		{
			name: "uncolored higher role",
			roles: [2]Role{
				{ID: "200000000000000000", Position: 1},
				{ID: "300000000000000000", Position: 1, Color: 0x654321},
			},
			want: 0x654321,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, order := range []struct {
				name  string
				first int
			}{{"older first", 0}, {"newer first", 1}} {
				t.Run(order.name, func(t *testing.T) {
					roles := []*Role{&tc.roles[order.first], &tc.roles[1-order.first]}
					member := &Member{GuildID: "guild", User: &User{ID: "user"}, Roles: []string{tc.roles[0].ID, tc.roles[1].ID}}
					state := NewState()
					if err := state.GuildAdd(&Guild{
						ID: "guild", Roles: roles, Members: []*Member{member},
						Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
					}); err != nil {
						t.Fatal(err)
					}
					if got := state.UserColor("user", "channel"); got != tc.want {
						t.Errorf("UserColor = %#x, want %#x", got, tc.want)
					}
					if got := state.MessageColor(&Message{ChannelID: "channel", Member: member}); got != tc.want {
						t.Errorf("MessageColor = %#x, want %#x", got, tc.want)
					}
					guild, err := state.Guild("guild")
					if err != nil {
						t.Fatal(err)
					}
					if guild.Roles[0].ID != tc.roles[order.first].ID || guild.Roles[1].ID != tc.roles[1-order.first].ID {
						t.Error("color lookup reordered cached roles")
					}
				})
			}
		})
	}
}
