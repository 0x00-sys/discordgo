package discordgo

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
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

func TestStateOnInterfaceRejectsMalformedMemberEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{
			name:  "member add missing member",
			event: &GuildMemberAdd{},
		},
		{
			name:  "nil member add",
			event: (*GuildMemberAdd)(nil),
		},
		{
			name: "member add missing user",
			event: &GuildMemberAdd{
				Member: &Member{GuildID: "guild"},
			},
		},
		{
			name: "member update missing user",
			event: &GuildMemberUpdate{
				Member: &Member{GuildID: "guild"},
			},
		},
		{
			name:  "nil member update",
			event: (*GuildMemberUpdate)(nil),
		},
		{
			name:  "member remove missing member",
			event: &GuildMemberRemove{},
		},
		{
			name:  "nil member remove",
			event: (*GuildMemberRemove)(nil),
		},
		{
			name: "member remove missing user",
			event: &GuildMemberRemove{
				Member: &Member{GuildID: "guild"},
			},
		},
		{
			name: "members chunk nil member",
			event: &GuildMembersChunk{
				GuildID: "guild",
				Members: []*Member{nil},
			},
		},
		{
			name:  "nil members chunk",
			event: (*GuildMembersChunk)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
				t.Fatalf("GuildAdd returned error: %v", err)
			}

			assertStateInvalidData(t, func() error {
				return state.OnInterface(&Session{StateEnabled: true}, tt.event)
			})
		})
	}
}

func TestMemberAddRejectsInvalidMember(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	tests := []struct {
		name   string
		member *Member
	}{
		{name: "nil member"},
		{name: "missing user", member: &Member{GuildID: "guild"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("MemberAdd panicked: %v", r)
				}
			}()
			if err := state.MemberAdd(tt.member); !errors.Is(err, ErrStateInvalidData) {
				t.Fatalf("MemberAdd returned error %v, want %v", err, ErrStateInvalidData)
			}
		})
	}
}

func TestStateOnInterfaceRejectsMalformedMessageEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{
			name:  "message create missing message",
			event: &MessageCreate{},
		},
		{
			name:  "message update missing message",
			event: &MessageUpdate{},
		},
		{
			name:  "message delete missing message",
			event: &MessageDelete{},
		},
		{
			name:  "nil message create",
			event: (*MessageCreate)(nil),
		},
		{
			name:  "nil message update",
			event: (*MessageUpdate)(nil),
		},
		{
			name:  "nil message delete",
			event: (*MessageDelete)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			state.MaxMessageCount = 1
			assertStateInvalidData(t, func() error {
				return state.OnInterface(&Session{StateEnabled: true}, tt.event)
			})
		})
	}
}

func TestMessageRemoveRejectsNilMessage(t *testing.T) {
	state := NewState()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MessageRemove panicked: %v", r)
		}
	}()
	if err := state.MessageRemove(nil); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("MessageRemove returned error %v, want %v", err, ErrStateInvalidData)
	}
}

func TestChannelHelpersRejectNilChannel(t *testing.T) {
	tests := []struct {
		name string
		call func(*State) error
	}{
		{name: "add", call: func(state *State) error { return state.ChannelAdd(nil) }},
		{name: "remove", call: func(state *State) error { return state.ChannelRemove(nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("channel helper panicked: %v", r)
				}
			}()
			if err := tt.call(NewState()); !errors.Is(err, ErrStateInvalidData) {
				t.Fatalf("channel helper returned error %v, want %v", err, ErrStateInvalidData)
			}
		})
	}
}

func TestStateOnInterfaceRejectsMalformedVoiceStateEvent(t *testing.T) {
	tests := []struct {
		name  string
		event *VoiceStateUpdate
	}{
		{name: "nil event", event: nil},
		{name: "missing voice state", event: &VoiceStateUpdate{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			assertStateInvalidData(t, func() error {
				return state.OnInterface(&Session{StateEnabled: true}, tt.event)
			})
		})
	}
}

func TestStateOnInterfaceIgnoresMalformedVoiceStateWhenTrackingDisabled(t *testing.T) {
	state := NewState()
	state.TrackVoice = false

	if err := state.OnInterface(&Session{StateEnabled: true}, &VoiceStateUpdate{}); err != nil {
		t.Fatalf("OnInterface returned error %v with voice tracking disabled", err)
	}
}

func TestStateOnInterfaceRejectsMalformedRoleChannelThreadEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{
			name:  "guild delete missing guild",
			event: &GuildDelete{},
		},
		{
			name: "role create missing role",
			event: &GuildRoleCreate{
				GuildRole: &GuildRole{GuildID: "guild"},
			},
		},
		{
			name:  "nil role create",
			event: (*GuildRoleCreate)(nil),
		},
		{
			name: "role update missing role",
			event: &GuildRoleUpdate{
				GuildRole: &GuildRole{GuildID: "guild"},
			},
		},
		{
			name:  "nil role update",
			event: (*GuildRoleUpdate)(nil),
		},
		{
			name:  "nil role delete",
			event: (*GuildRoleDelete)(nil),
		},
		{
			name:  "channel create missing channel",
			event: &ChannelCreate{},
		},
		{
			name:  "nil channel create",
			event: (*ChannelCreate)(nil),
		},
		{
			name:  "channel update missing channel",
			event: &ChannelUpdate{},
		},
		{
			name:  "nil channel update",
			event: (*ChannelUpdate)(nil),
		},
		{
			name:  "channel delete missing channel",
			event: &ChannelDelete{},
		},
		{
			name:  "nil channel delete",
			event: (*ChannelDelete)(nil),
		},
		{
			name:  "thread create missing thread",
			event: &ThreadCreate{},
		},
		{
			name:  "nil thread create",
			event: (*ThreadCreate)(nil),
		},
		{
			name:  "thread update missing thread",
			event: &ThreadUpdate{},
		},
		{
			name:  "nil thread update",
			event: (*ThreadUpdate)(nil),
		},
		{
			name:  "thread delete missing thread",
			event: &ThreadDelete{},
		},
		{
			name:  "nil thread delete",
			event: (*ThreadDelete)(nil),
		},
		{
			name:  "thread member update missing member",
			event: &ThreadMemberUpdate{},
		},
		{
			name: "thread list sync nil thread",
			event: &ThreadListSync{
				GuildID: "guild",
				Threads: []*Channel{nil},
			},
		},
		{
			name: "thread list sync nil member",
			event: &ThreadListSync{
				GuildID: "guild",
				Members: []*ThreadMember{
					nil,
				},
			},
		},
		{
			name: "thread members update missing user",
			event: &ThreadMembersUpdate{
				ID:      "thread",
				GuildID: "guild",
				AddedMembers: []AddedThreadMember{
					{
						ThreadMember: &ThreadMember{
							ID:     "thread",
							UserID: "user",
						},
						Member: &Member{GuildID: "guild"},
					},
				},
			},
		},
		{
			name: "thread members update nil thread member",
			event: &ThreadMembersUpdate{
				ID:      "thread",
				GuildID: "guild",
				AddedMembers: []AddedThreadMember{
					{
						Member: &Member{
							GuildID: "guild",
							User:    &User{ID: "user"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newStateForMalformedEventTests(t)
			assertStateInvalidData(t, func() error {
				return state.OnInterface(&Session{StateEnabled: true}, tt.event)
			})
		})
	}
}

func TestRoleHelpersHandleNilRoles(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			nil,
			{ID: "existing"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("role helper panicked: %v", r)
		}
	}()
	if err := state.RoleAdd("guild", nil); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("RoleAdd(nil) returned error %v, want %v", err, ErrStateInvalidData)
	}
	if _, err := state.Role("guild", "existing"); err != nil {
		t.Fatalf("Role returned error: %v", err)
	}
	if err := state.RoleAdd("guild", &Role{ID: "new"}); err != nil {
		t.Fatalf("RoleAdd returned error: %v", err)
	}
	if err := state.RoleRemove("guild", "existing"); err != nil {
		t.Fatalf("RoleRemove returned error: %v", err)
	}
}

func TestSessionOnInterfaceHandlesMalformedGuildPayloads(t *testing.T) {
	session := &Session{
		State:        NewState(),
		StateEnabled: true,
	}

	tests := []struct {
		name  string
		event interface{}
	}{
		{
			name: "ready nil guild",
			event: &Ready{
				Guilds: []*Guild{nil},
			},
		},
		{
			name:  "guild create missing guild",
			event: &GuildCreate{},
		},
		{
			name:  "guild update missing guild",
			event: &GuildUpdate{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("onInterface panicked: %v", r)
				}
			}()
			session.onInterface(tt.event)
		})
	}
}

func TestSessionOnEventHandlesNullStateDispatch(t *testing.T) {
	session, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.State = newStateForMalformedEventTests(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("onEvent panicked: %v", r)
		}
	}()

	_, err = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"CHANNEL_CREATE","d":null}`))
	if err != nil {
		t.Fatalf("onEvent returned error: %v", err)
	}
}

func TestSessionOnEventHandlesNullVoiceStateDispatch(t *testing.T) {
	session, err := New("Bot token")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("onEvent panicked: %v", r)
		}
	}()

	_, err = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"VOICE_STATE_UPDATE","d":null}`))
	if err != nil {
		t.Fatalf("onEvent returned error: %v", err)
	}
}

func TestSessionOnEventHandlesNullMessageDispatch(t *testing.T) {
	tests := []string{
		"MESSAGE_CREATE",
		"MESSAGE_UPDATE",
		"MESSAGE_DELETE",
	}

	for _, eventType := range tests {
		t.Run(eventType, func(t *testing.T) {
			session, err := New("Bot token")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.State.MaxMessageCount = 1

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("onEvent panicked: %v", r)
				}
			}()

			_, err = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"`+eventType+`","d":null}`))
			if err != nil {
				t.Fatalf("onEvent returned error: %v", err)
			}
		})
	}
}

func newStateForMalformedEventTests(t *testing.T) *State {
	t.Helper()
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{ID: "role"},
		},
		Channels: []*Channel{
			{ID: "channel", GuildID: "guild"},
		},
		Threads: []*Channel{
			{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	return state
}

func assertStateInvalidData(t *testing.T, f func() error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OnInterface panicked: %v", r)
		}
	}()
	if err := f(); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("OnInterface returned error %v, want %v", err, ErrStateInvalidData)
	}
}

func TestGuildRemoveClearsGuildIndexes(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{ID: "channel", GuildID: "guild"},
		},
		Threads: []*Channel{
			{
				ID:             "thread",
				GuildID:        "guild",
				Type:           ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{},
			},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	if _, err := state.Channel("channel"); err != nil {
		t.Fatalf("Channel returned error before GuildRemove: %v", err)
	}
	if _, err := state.Channel("thread"); err != nil {
		t.Fatalf("Thread returned error before GuildRemove: %v", err)
	}
	if _, err := state.Member("guild", "user"); err != nil {
		t.Fatalf("Member returned error before GuildRemove: %v", err)
	}

	if err := state.GuildRemove(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildRemove returned error: %v", err)
	}

	if _, err := state.Channel("channel"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Channel returned error %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Channel("thread"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Thread returned error %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Member("guild", "user"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Member returned error %v, want %v", err, ErrStateNotFound)
	}
}

func TestGuildDeleteUnavailableKeepsGuildIndexes(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{ID: "channel", GuildID: "guild"},
		},
		Threads: []*Channel{
			{
				ID:             "thread",
				GuildID:        "guild",
				Type:           ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{},
			},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	event := &GuildDelete{Guild: &Guild{
		ID:          "guild",
		Unavailable: true,
	}}
	if err := state.OnInterface(&Session{StateEnabled: true}, event); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after unavailable delete: %v", err)
	}
	if !guild.Unavailable {
		t.Fatal("guild unavailable flag was not set")
	}
	if event.BeforeDelete == nil {
		t.Fatal("BeforeDelete was not populated")
	}
	if _, err := state.Channel("channel"); err != nil {
		t.Fatalf("Channel returned error after unavailable delete: %v", err)
	}
	if _, err := state.Channel("thread"); err != nil {
		t.Fatalf("Thread returned error after unavailable delete: %v", err)
	}
	if _, err := state.Member("guild", "user"); err != nil {
		t.Fatalf("Member returned error after unavailable delete: %v", err)
	}

	if err := state.OnInterface(&Session{StateEnabled: true}, &GuildCreate{Guild: &Guild{ID: "guild"}}); err != nil {
		t.Fatalf("GuildCreate returned error: %v", err)
	}
	guild, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after available create: %v", err)
	}
	if guild.Unavailable {
		t.Fatal("guild unavailable flag was not cleared")
	}
}

func TestGuildAddReplacesCachedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		Name:        "old",
		MemberCount: 1,
		Roles: []*Role{
			{ID: "role", Name: "old-role"},
		},
		Channels: []*Channel{
			{ID: "channel", GuildID: "guild"},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.GuildAdd(&Guild{
		ID:   "guild",
		Name: "new",
	})
	if err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after update: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("GuildAdd reused the previously cached guild pointer")
	}
	if oldGuild.Name != "old" {
		t.Fatalf("old guild name = %q, want old", oldGuild.Name)
	}
	if updatedGuild.Name != "new" {
		t.Fatalf("updated guild name = %q, want new", updatedGuild.Name)
	}
	if updatedGuild.MemberCount != 1 {
		t.Fatalf("updated guild member count = %d, want 1", updatedGuild.MemberCount)
	}
	if len(updatedGuild.Roles) != 1 || updatedGuild.Roles[0].Name != "old-role" {
		t.Fatalf("updated guild roles = %#v, want preserved old role", updatedGuild.Roles)
	}
	if len(updatedGuild.Channels) != 1 || updatedGuild.Channels[0].ID != "channel" {
		t.Fatalf("updated guild channels = %#v, want preserved channel", updatedGuild.Channels)
	}
	if len(updatedGuild.Members) != 1 || updatedGuild.Members[0].User.ID != "user" {
		t.Fatalf("updated guild members = %#v, want preserved member", updatedGuild.Members)
	}

	if err := state.RoleAdd("guild", &Role{ID: "role", Name: "new-role"}); err != nil {
		t.Fatalf("RoleAdd returned error: %v", err)
	}
	if oldGuild.Roles[0].Name != "old-role" {
		t.Fatalf("old guild role name = %q, want old-role", oldGuild.Roles[0].Name)
	}
}

func TestGuildAddDoesNotRaceReturnedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		Name:        "old",
		MemberCount: 1,
		Roles: []*Role{
			{ID: "role", Name: "old-role"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = guild.Name
			_ = guild.MemberCount
			if len(guild.Roles) != 0 {
				_ = guild.Roles[0].Name
			}
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.GuildAdd(&Guild{
			ID:          "guild",
			Name:        strconv.Itoa(i),
			MemberCount: i,
		})
		if err != nil {
			t.Fatalf("GuildAdd returned error: %v", err)
		}
	}
	<-done
}

func TestGuildMemberAddReplacesCachedGuildPointer(t *testing.T) {
	state := NewState()
	state.TrackMembers = false
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		Name:        "old",
		MemberCount: 1,
		Roles: []*Role{
			{ID: "role", Name: "old-role"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.OnInterface(&Session{StateEnabled: true}, &GuildMemberAdd{
		Member: &Member{
			GuildID: "guild",
			User:    &User{ID: "user"},
		},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after update: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("GuildMemberAdd reused the previously cached guild pointer")
	}
	if oldGuild.MemberCount != 1 {
		t.Fatalf("old guild member count = %d, want 1", oldGuild.MemberCount)
	}
	if updatedGuild.MemberCount != 2 {
		t.Fatalf("updated guild member count = %d, want 2", updatedGuild.MemberCount)
	}

	if err := state.RoleAdd("guild", &Role{ID: "role", Name: "new-role"}); err != nil {
		t.Fatalf("RoleAdd returned error: %v", err)
	}
	if oldGuild.Roles[0].Name != "old-role" {
		t.Fatalf("old guild role name = %q, want old-role", oldGuild.Roles[0].Name)
	}
}

func TestGuildMemberRemoveReplacesCachedGuildPointer(t *testing.T) {
	state := NewState()
	state.TrackMembers = false
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		MemberCount: 2,
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.OnInterface(&Session{StateEnabled: true}, &GuildMemberRemove{
		Member: &Member{
			GuildID: "guild",
			User:    &User{ID: "user"},
		},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after update: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("GuildMemberRemove reused the previously cached guild pointer")
	}
	if oldGuild.MemberCount != 2 {
		t.Fatalf("old guild member count = %d, want 2", oldGuild.MemberCount)
	}
	if updatedGuild.MemberCount != 1 {
		t.Fatalf("updated guild member count = %d, want 1", updatedGuild.MemberCount)
	}
}

func TestGuildMemberAddDoesNotRaceReturnedGuildPointer(t *testing.T) {
	state := NewState()
	state.TrackMembers = false
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		Name:        "old",
		MemberCount: 1,
		Roles: []*Role{
			{ID: "role", Name: "old-role"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = guild.Name
			_ = guild.MemberCount
			if len(guild.Roles) != 0 {
				_ = guild.Roles[0].Name
			}
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.OnInterface(&Session{StateEnabled: true}, &GuildMemberAdd{
			Member: &Member{
				GuildID: "guild",
				User:    &User{ID: strconv.Itoa(i)},
			},
		})
		if err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}
	<-done
}

func TestReadyClearsStaleIndexes(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "stale-guild",
		Channels: []*Channel{
			{ID: "stale-channel", GuildID: "stale-guild"},
		},
		Threads: []*Channel{
			{
				ID:             "stale-thread",
				GuildID:        "stale-guild",
				Type:           ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{},
			},
		},
		Members: []*Member{
			{
				GuildID: "stale-guild",
				User:    &User{ID: "stale-user"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.OnInterface(&Session{StateEnabled: true}, &Ready{
		Guilds: []*Guild{
			{
				ID: "ready-guild",
				Channels: []*Channel{
					{ID: "ready-channel", GuildID: "ready-guild"},
				},
				Threads: []*Channel{
					{
						ID:             "ready-thread",
						GuildID:        "ready-guild",
						Type:           ChannelTypeGuildPublicThread,
						ThreadMetadata: &ThreadMetadata{},
					},
				},
				Members: []*Member{
					{
						GuildID: "ready-guild",
						User:    &User{ID: "ready-user"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if _, err := state.Guild("ready-guild"); err != nil {
		t.Fatalf("ready guild missing after Ready: %v", err)
	}
	if _, err := state.Channel("ready-channel"); err != nil {
		t.Fatalf("ready channel missing after Ready: %v", err)
	}
	if _, err := state.Channel("ready-thread"); err != nil {
		t.Fatalf("ready thread missing after Ready: %v", err)
	}
	if _, err := state.Member("ready-guild", "ready-user"); err != nil {
		t.Fatalf("ready member missing after Ready: %v", err)
	}

	if _, err := state.Guild("stale-guild"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("stale guild error = %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Channel("stale-channel"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("stale channel error = %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Channel("stale-thread"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("stale thread error = %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Member("stale-guild", "stale-user"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("stale member error = %v, want %v", err, ErrStateNotFound)
	}
}

func TestThreadListSyncHandlesThreadWithoutMetadata(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:       "keep-thread",
				GuildID:  "guild",
				ParentID: "other-parent",
				Type:     ChannelTypeGuildPublicThread,
			},
			{
				ID:       "remove-thread",
				GuildID:  "guild",
				ParentID: "synced-parent",
				Type:     ChannelTypeGuildPublicThread,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.ThreadListSync(&ThreadListSync{
		GuildID:    "guild",
		ChannelIDs: []string{"synced-parent"},
		Threads: []*Channel{
			{
				ID:             "synced-thread",
				GuildID:        "guild",
				ParentID:       "synced-parent",
				Type:           ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{},
			},
		},
	})
	if err != nil {
		t.Fatalf("ThreadListSync returned error: %v", err)
	}

	if _, err := state.Channel("keep-thread"); err != nil {
		t.Fatalf("keep-thread missing after ThreadListSync: %v", err)
	}
	if _, err := state.Channel("remove-thread"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("remove-thread error = %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Channel("synced-thread"); err != nil {
		t.Fatalf("synced-thread missing after ThreadListSync: %v", err)
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

func TestPresenceAddReplacesCachedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Presences: []*Presence{
			{
				User:         &User{ID: "user", Username: "old", Avatar: "avatar"},
				Status:       StatusOnline,
				ClientStatus: ClientStatus{Desktop: StatusOnline},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldPresence, err := state.Presence("guild", "user")
	if err != nil {
		t.Fatalf("Presence returned error: %v", err)
	}

	err = state.PresenceAdd("guild", &Presence{
		User:         &User{ID: "user", Username: "new"},
		Status:       StatusIdle,
		ClientStatus: ClientStatus{Mobile: StatusIdle},
	})
	if err != nil {
		t.Fatalf("PresenceAdd returned error: %v", err)
	}

	updatedPresence, err := state.Presence("guild", "user")
	if err != nil {
		t.Fatalf("Presence returned error after update: %v", err)
	}
	if updatedPresence == oldPresence {
		t.Fatal("PresenceAdd reused the previously cached presence pointer")
	}
	if updatedPresence.User == oldPresence.User {
		t.Fatal("PresenceAdd reused the previously cached user pointer")
	}
	if oldPresence.Status != StatusOnline {
		t.Fatalf("old presence status = %q, want %q", oldPresence.Status, StatusOnline)
	}
	if oldPresence.User.Username != "old" {
		t.Fatalf("old presence username = %q, want old", oldPresence.User.Username)
	}
	if updatedPresence.Status != StatusIdle {
		t.Fatalf("updated presence status = %q, want %q", updatedPresence.Status, StatusIdle)
	}
	if updatedPresence.User.Username != "new" {
		t.Fatalf("updated presence username = %q, want new", updatedPresence.User.Username)
	}
	if updatedPresence.User.Avatar != "avatar" {
		t.Fatalf("updated presence avatar = %q, want avatar", updatedPresence.User.Avatar)
	}
	if updatedPresence.ClientStatus.Desktop != StatusOnline {
		t.Fatalf("updated desktop status = %q, want %q", updatedPresence.ClientStatus.Desktop, StatusOnline)
	}
	if updatedPresence.ClientStatus.Mobile != StatusIdle {
		t.Fatalf("updated mobile status = %q, want %q", updatedPresence.ClientStatus.Mobile, StatusIdle)
	}
}

func TestPresenceAddDoesNotRaceReturnedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Presences: []*Presence{
			{
				User:   &User{ID: "user", Username: "old"},
				Status: StatusOnline,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	presence, err := state.Presence("guild", "user")
	if err != nil {
		t.Fatalf("Presence returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = presence.Status
			_ = presence.User.Username
			_ = presence.ClientStatus.Desktop
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.PresenceAdd("guild", &Presence{
			User:         &User{ID: "user", Username: strconv.Itoa(i)},
			Status:       StatusIdle,
			ClientStatus: ClientStatus{Desktop: StatusIdle},
		})
		if err != nil {
			t.Fatalf("PresenceAdd returned error: %v", err)
		}
	}
	<-done
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

func TestUserChannelPermissionsUsesStateLock(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: PermissionViewChannel,
			},
			{
				ID:          "role",
				Permissions: PermissionSendMessages,
			},
		},
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
			},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
				Roles:   []string{"role"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < 10000; i++ {
			err := state.RoleAdd("guild", &Role{
				ID:          "role",
				Permissions: PermissionSendMessages,
			})
			if err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()

	for i := 0; i < 10000; i++ {
		_, err := state.UserChannelPermissions("user", "channel")
		if err != nil {
			t.Fatalf("UserChannelPermissions returned error: %v", err)
		}
	}

	if err := <-errCh; err != nil {
		t.Fatalf("RoleAdd returned error: %v", err)
	}
}

func TestMessagePermissionsUsesStateLock(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: PermissionViewChannel,
			},
			{
				ID:          "role",
				Permissions: PermissionSendMessages,
			},
		},
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	message := &Message{
		ChannelID: "channel",
		Author:    &User{ID: "user"},
		Member: &Member{
			Roles: []string{"role"},
		},
	}

	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < 10000; i++ {
			err := state.RoleAdd("guild", &Role{
				ID:          "role",
				Permissions: PermissionSendMessages,
			})
			if err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()

	for i := 0; i < 10000; i++ {
		_, err := state.MessagePermissions(message)
		if err != nil {
			t.Fatalf("MessagePermissions returned error: %v", err)
		}
	}

	if err := <-errCh; err != nil {
		t.Fatalf("RoleAdd returned error: %v", err)
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

func TestMemberAddReplacesCachedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user", Username: "old"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldMember, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}

	err = state.MemberAdd(&Member{
		GuildID: "guild",
		User:    &User{ID: "user", Username: "new"},
	})
	if err != nil {
		t.Fatalf("MemberAdd returned error: %v", err)
	}

	updatedMember, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error after update: %v", err)
	}
	if updatedMember == oldMember {
		t.Fatal("MemberAdd reused the previously cached member pointer")
	}
	if updatedMember.User == oldMember.User {
		t.Fatal("MemberAdd reused the previously cached user pointer")
	}
	if oldMember.User.Username != "old" {
		t.Fatalf("old member username = %q, want old", oldMember.User.Username)
	}
	if updatedMember.User.Username != "new" {
		t.Fatalf("updated member username = %q, want new", updatedMember.User.Username)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.Members) != 1 {
		t.Fatalf("len(guild.Members) = %d, want 1", len(guild.Members))
	}
	if guild.Members[0] != updatedMember {
		t.Fatal("guild member slice does not point at the updated cached member")
	}
}

func TestMemberAddDoesNotRaceReturnedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user", Username: "old"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	member, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = member.JoinedAt
			_ = member.User.Username
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.MemberAdd(&Member{
			GuildID: "guild",
			User:    &User{ID: "user", Username: strconv.Itoa(i)},
		})
		if err != nil {
			t.Fatalf("MemberAdd returned error: %v", err)
		}
	}
	<-done
}

func TestReadyIndexesThreads(t *testing.T) {
	state := NewState()
	session := &Session{
		State:        state,
		StateEnabled: true,
	}

	session.onInterface(&Ready{
		Guilds: []*Guild{
			{
				ID: "guild",
				Threads: []*Channel{
					{
						ID:       "thread",
						ParentID: "parent",
						Type:     ChannelTypeGuildPublicThread,
					},
				},
			},
		},
	})

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if thread.GuildID != "guild" {
		t.Fatalf("thread GuildID = %q, want guild", thread.GuildID)
	}
}

func TestSetGuildIDsSetsThreadGuildID(t *testing.T) {
	guild := &Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:   "thread",
				Type: ChannelTypeGuildPublicThread,
			},
		},
	}

	setGuildIds(guild)

	if guild.Threads[0].GuildID != "guild" {
		t.Fatalf("thread GuildID = %q, want guild", guild.Threads[0].GuildID)
	}
}

func TestThreadPermissionsUseParentChannelOverwrites(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: PermissionViewChannel | PermissionSendMessages | PermissionSendMessagesInThreads,
			},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
			},
		},
		Channels: []*Channel{
			{
				ID:      "parent",
				GuildID: "guild",
				Type:    ChannelTypeGuildText,
				PermissionOverwrites: []*PermissionOverwrite{
					{
						ID:   "guild",
						Type: PermissionOverwriteTypeRole,
						Deny: PermissionViewChannel,
					},
				},
			},
		},
		Threads: []*Channel{
			{
				ID:       "thread",
				ParentID: "parent",
				Type:     ChannelTypeGuildPublicThread,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	permissions, err := state.UserChannelPermissions("user", "thread")
	if err != nil {
		t.Fatalf("UserChannelPermissions returned error: %v", err)
	}
	if permissions&PermissionViewChannel != 0 {
		t.Fatalf("thread permissions include ViewChannel despite parent deny: %d", permissions)
	}
}

func TestThreadMessagePermissionsUseThreadSendPermission(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: PermissionViewChannel | PermissionSendMessages | PermissionSendMessagesInThreads,
			},
		},
		Channels: []*Channel{
			{
				ID:      "parent",
				GuildID: "guild",
				Type:    ChannelTypeGuildText,
			},
		},
		Threads: []*Channel{
			{
				ID:       "thread",
				ParentID: "parent",
				Type:     ChannelTypeGuildPublicThread,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	permissions, err := state.MessagePermissions(&Message{
		ChannelID: "thread",
		Author:    &User{ID: "user"},
		Member: &Member{
			Roles: []string{},
		},
	})
	if err != nil {
		t.Fatalf("MessagePermissions returned error: %v", err)
	}
	if permissions&PermissionSendMessages != 0 {
		t.Fatalf("thread permissions include SendMessages: %d", permissions)
	}
	if permissions&PermissionSendMessagesInThreads == 0 {
		t.Fatalf("thread permissions do not include SendMessagesInThreads: %d", permissions)
	}
}

func TestChannelAddReplacesCachedPointer(t *testing.T) {
	state := NewState()
	overwrite := &PermissionOverwrite{ID: "role", Type: PermissionOverwriteTypeRole}
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:                   "channel",
				GuildID:              "guild",
				Name:                 "old",
				Type:                 ChannelTypeGuildText,
				PermissionOverwrites: []*PermissionOverwrite{overwrite},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldChannel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	err = state.ChannelAdd(&Channel{
		ID:      "channel",
		GuildID: "guild",
		Name:    "new",
		Type:    ChannelTypeGuildText,
	})
	if err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	updatedChannel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error after update: %v", err)
	}
	if updatedChannel == oldChannel {
		t.Fatal("ChannelAdd reused the previously cached channel pointer")
	}
	if oldChannel.Name != "old" {
		t.Fatalf("old channel name = %q, want old", oldChannel.Name)
	}
	if updatedChannel.Name != "new" {
		t.Fatalf("updated channel name = %q, want new", updatedChannel.Name)
	}
	if len(updatedChannel.PermissionOverwrites) != 1 || updatedChannel.PermissionOverwrites[0] != overwrite {
		t.Fatal("updated channel did not preserve permission overwrites")
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.Channels) != 1 {
		t.Fatalf("len(guild.Channels) = %d, want 1", len(guild.Channels))
	}
	if guild.Channels[0] != updatedChannel {
		t.Fatal("guild channel slice does not point at the updated cached channel")
	}
}

func TestThreadChannelAddReplacesCachedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:       "thread",
				GuildID:  "guild",
				ParentID: "parent",
				Name:     "old",
				Type:     ChannelTypeGuildPublicThread,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	err = state.ChannelAdd(&Channel{
		ID:       "thread",
		GuildID:  "guild",
		ParentID: "parent",
		Name:     "new",
		Type:     ChannelTypeGuildPublicThread,
	})
	if err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	updatedThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error after update: %v", err)
	}
	if updatedThread == oldThread {
		t.Fatal("ChannelAdd reused the previously cached thread pointer")
	}
	if oldThread.Name != "old" {
		t.Fatalf("old thread name = %q, want old", oldThread.Name)
	}
	if updatedThread.Name != "new" {
		t.Fatalf("updated thread name = %q, want new", updatedThread.Name)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.Threads) != 1 {
		t.Fatalf("len(guild.Threads) = %d, want 1", len(guild.Threads))
	}
	if guild.Threads[0] != updatedThread {
		t.Fatal("guild thread slice does not point at the updated cached thread")
	}
}

func TestChannelAddDoesNotRaceReturnedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Name:    "old",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	channel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = channel.Name
			_ = channel.Topic
			_ = channel.PermissionOverwrites
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.ChannelAdd(&Channel{
			ID:      "channel",
			GuildID: "guild",
			Name:    strconv.Itoa(i),
			Topic:   strconv.Itoa(i),
			Type:    ChannelTypeGuildText,
		})
		if err != nil {
			t.Fatalf("ChannelAdd returned error: %v", err)
		}
	}
	<-done
}

func TestChannelAddReplacesParentGuildPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Name:    "old",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.ChannelAdd(&Channel{
		ID:      "new-channel",
		GuildID: "guild",
		Name:    "new",
		Type:    ChannelTypeGuildText,
	})
	if err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after ChannelAdd: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("ChannelAdd reused the previously cached parent guild pointer")
	}
	if len(oldGuild.Channels) != 1 {
		t.Fatalf("len(oldGuild.Channels) = %d, want 1", len(oldGuild.Channels))
	}
	if len(updatedGuild.Channels) != 2 {
		t.Fatalf("len(updatedGuild.Channels) = %d, want 2", len(updatedGuild.Channels))
	}
}

func TestChannelAddKeepsReturnedGuildCategoryCountSnapshot(t *testing.T) {
	state := NewState()
	channels := []*Channel{
		{
			ID:      "category",
			GuildID: "guild",
			Name:    "category",
			Type:    ChannelTypeGuildCategory,
		},
	}
	for i := 0; i < 49; i++ {
		channels = append(channels, &Channel{
			ID:       "channel-" + strconv.Itoa(i),
			GuildID:  "guild",
			ParentID: "category",
			Name:     strconv.Itoa(i),
			Type:     ChannelTypeGuildText,
		})
	}

	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: channels,
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if countChannelsInCategory(oldGuild, "category") != 49 {
		t.Fatalf("old category count before ChannelAdd = %d, want 49", countChannelsInCategory(oldGuild, "category"))
	}

	err = state.ChannelAdd(&Channel{
		ID:       "channel-49",
		GuildID:  "guild",
		ParentID: "category",
		Name:     "49",
		Type:     ChannelTypeGuildText,
	})
	if err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after ChannelAdd: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("ChannelAdd reused the previously cached parent guild pointer")
	}
	if countChannelsInCategory(oldGuild, "category") != 49 {
		t.Fatalf("old category count after ChannelAdd = %d, want 49", countChannelsInCategory(oldGuild, "category"))
	}
	if countChannelsInCategory(updatedGuild, "category") != 50 {
		t.Fatalf("updated category count after ChannelAdd = %d, want 50", countChannelsInCategory(updatedGuild, "category"))
	}
}

func TestChannelUpdateKeepsReturnedGuildCategoryCountSnapshot(t *testing.T) {
	state := NewState()
	channels := []*Channel{
		{
			ID:      "category",
			GuildID: "guild",
			Name:    "category",
			Type:    ChannelTypeGuildCategory,
		},
		{
			ID:      "ticket-channel",
			GuildID: "guild",
			Name:    "ticket",
			Type:    ChannelTypeGuildText,
		},
	}
	for i := 0; i < 49; i++ {
		channels = append(channels, &Channel{
			ID:       "channel-" + strconv.Itoa(i),
			GuildID:  "guild",
			ParentID: "category",
			Name:     strconv.Itoa(i),
			Type:     ChannelTypeGuildText,
		})
	}

	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: channels,
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if countChannelsInCategory(oldGuild, "category") != 49 {
		t.Fatalf("old category count before ChannelAdd = %d, want 49", countChannelsInCategory(oldGuild, "category"))
	}

	err = state.ChannelAdd(&Channel{
		ID:       "ticket-channel",
		GuildID:  "guild",
		ParentID: "category",
		Name:     "ticket",
		Type:     ChannelTypeGuildText,
	})
	if err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after ChannelAdd: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("ChannelAdd reused the previously cached parent guild pointer")
	}
	if countChannelsInCategory(oldGuild, "category") != 49 {
		t.Fatalf("old category count after ChannelAdd = %d, want 49", countChannelsInCategory(oldGuild, "category"))
	}
	if countChannelsInCategory(updatedGuild, "category") != 50 {
		t.Fatalf("updated category count after ChannelAdd = %d, want 50", countChannelsInCategory(updatedGuild, "category"))
	}
}

func TestChannelRemoveReplacesParentGuildPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Name:    "old",
				Type:    ChannelTypeGuildText,
			},
			{
				ID:      "removed-channel",
				GuildID: "guild",
				Name:    "remove me",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.ChannelRemove(&Channel{
		ID:      "removed-channel",
		GuildID: "guild",
		Type:    ChannelTypeGuildText,
	})
	if err != nil {
		t.Fatalf("ChannelRemove returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after ChannelRemove: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("ChannelRemove reused the previously cached parent guild pointer")
	}
	if len(oldGuild.Channels) != 2 {
		t.Fatalf("len(oldGuild.Channels) = %d, want 2", len(oldGuild.Channels))
	}
	if len(updatedGuild.Channels) != 1 {
		t.Fatalf("len(updatedGuild.Channels) = %d, want 1", len(updatedGuild.Channels))
	}
	if updatedGuild.Channels[0].ID != "channel" {
		t.Fatalf("remaining channel ID = %q, want channel", updatedGuild.Channels[0].ID)
	}
}

func TestChannelAddDoesNotRaceReturnedGuildChannels(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Name:    "old",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			for _, channel := range guild.Channels {
				if channel != nil {
					_ = channel.ID
					_ = channel.ParentID
				}
			}
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.ChannelAdd(&Channel{
			ID:       "new-channel-" + strconv.Itoa(i),
			GuildID:  "guild",
			ParentID: "category",
			Name:     strconv.Itoa(i),
			Type:     ChannelTypeGuildText,
		})
		if err != nil {
			t.Fatalf("ChannelAdd returned error: %v", err)
		}
	}
	<-done
}

func TestChannelUpdateDoesNotRaceReturnedGuildChannels(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "category",
				GuildID: "guild",
				Name:    "category",
				Type:    ChannelTypeGuildCategory,
			},
			{
				ID:      "ticket-channel",
				GuildID: "guild",
				Name:    "ticket",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = countChannelsInCategory(guild, "category")
		}
	}()

	for i := 0; i < 10000; i++ {
		parentID := ""
		if i%2 == 0 {
			parentID = "category"
		}
		err := state.ChannelAdd(&Channel{
			ID:       "ticket-channel",
			GuildID:  "guild",
			ParentID: parentID,
			Name:     strconv.Itoa(i),
			Type:     ChannelTypeGuildText,
		})
		if err != nil {
			t.Fatalf("ChannelAdd returned error: %v", err)
		}
	}
	<-done
}

func TestRoleUpdateKeepsReturnedGuildPermissionSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: 0,
			},
			{
				ID:          "staff",
				Permissions: 0,
			},
		},
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	channel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if permissions := memberPermissions(oldGuild, channel, "user", []string{"staff"}); permissions&PermissionManageChannels != 0 {
		t.Fatalf("old permissions before RoleAdd = %d, want no ManageChannels", permissions)
	}

	err = state.RoleAdd("guild", &Role{
		ID:          "staff",
		Permissions: PermissionManageChannels,
	})
	if err != nil {
		t.Fatalf("RoleAdd returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after RoleAdd: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("RoleAdd reused the previously cached parent guild pointer")
	}
	if permissions := memberPermissions(oldGuild, channel, "user", []string{"staff"}); permissions&PermissionManageChannels != 0 {
		t.Fatalf("old permissions after RoleAdd = %d, want no ManageChannels", permissions)
	}
	if permissions := memberPermissions(updatedGuild, channel, "user", []string{"staff"}); permissions&PermissionManageChannels == 0 {
		t.Fatalf("updated permissions after RoleAdd = %d, want ManageChannels", permissions)
	}
}

func TestRoleRemoveReplacesParentGuildPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: 0,
			},
			{
				ID:          "staff",
				Permissions: PermissionManageChannels,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.RoleRemove("guild", "staff")
	if err != nil {
		t.Fatalf("RoleRemove returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after RoleRemove: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("RoleRemove reused the previously cached parent guild pointer")
	}
	if len(oldGuild.Roles) != 2 {
		t.Fatalf("len(oldGuild.Roles) = %d, want 2", len(oldGuild.Roles))
	}
	if len(updatedGuild.Roles) != 1 {
		t.Fatalf("len(updatedGuild.Roles) = %d, want 1", len(updatedGuild.Roles))
	}
	if updatedGuild.Roles[0].ID != "guild" {
		t.Fatalf("remaining role ID = %q, want guild", updatedGuild.Roles[0].ID)
	}
}

func TestRoleUpdateDoesNotRaceReturnedGuildPermissions(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:          "guild",
				Permissions: 0,
			},
			{
				ID:          "staff",
				Permissions: 0,
			},
		},
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Type:    ChannelTypeGuildText,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	channel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = memberPermissions(guild, channel, "user", []string{"staff"})
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.RoleAdd("guild", &Role{
			ID:          "staff",
			Permissions: int64(i),
		})
		if err != nil {
			t.Fatalf("RoleAdd returned error: %v", err)
		}
	}
	<-done
}

func countChannelsInCategory(guild *Guild, categoryID string) int {
	count := 0
	for _, channel := range guild.Channels {
		if channel != nil && channel.ParentID == categoryID {
			count++
		}
	}
	return count
}

func TestMessageEventsFillMissingGuildIDFromChannelState(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	tests := []struct {
		name  string
		event func(*Message) interface{}
	}{
		{
			name:  "create",
			event: func(message *Message) interface{} { return &MessageCreate{Message: message} },
		},
		{
			name:  "update",
			event: func(message *Message) interface{} { return &MessageUpdate{Message: message} },
		},
		{
			name:  "delete",
			event: func(message *Message) interface{} { return &MessageDelete{Message: message} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := &Message{
				ID:        "message-" + tt.name,
				ChannelID: "channel",
			}
			if err := state.OnInterface(&Session{StateEnabled: true}, tt.event(message)); err != nil {
				t.Fatalf("OnInterface returned error: %v", err)
			}
			if message.GuildID != "guild" {
				t.Fatalf("GuildID = %q, want guild", message.GuildID)
			}
		})
	}
}

func TestMessageEventKeepsExistingGuildID(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	message := &Message{
		ID:        "message",
		ChannelID: "channel",
		GuildID:   "existing-guild",
	}
	if err := state.OnInterface(&Session{StateEnabled: true}, &MessageCreate{Message: message}); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	if message.GuildID != "existing-guild" {
		t.Fatalf("GuildID = %q, want existing-guild", message.GuildID)
	}
}

func TestUserColorDoesNotReorderCachedRoles(t *testing.T) {
	state := newColorTestState(t)

	if color := state.UserColor("user", "channel"); color != 0x123456 {
		t.Fatalf("UserColor = %d, want %d", color, 0x123456)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	got := []string{guild.Roles[0].ID, guild.Roles[1].ID, guild.Roles[2].ID}
	want := []string{"guild", "role-high", "role-low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cached role order = %#v, want %#v", got, want)
		}
	}
}

func TestMessageColorDoesNotReorderCachedRoles(t *testing.T) {
	state := newColorTestState(t)
	message := &Message{
		ChannelID: "channel",
		Member: &Member{
			Roles: []string{"role-high"},
		},
	}

	if color := state.MessageColor(message); color != 0x123456 {
		t.Fatalf("MessageColor = %d, want %d", color, 0x123456)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	got := []string{guild.Roles[0].ID, guild.Roles[1].ID, guild.Roles[2].ID}
	want := []string{"guild", "role-high", "role-low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cached role order = %#v, want %#v", got, want)
		}
	}
}

func TestUserColorUsesStateLock(t *testing.T) {
	state := newColorTestState(t)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			if err := state.RoleAdd("guild", &Role{
				ID:       "role-high",
				Color:    0x123456,
				Position: i + 1,
			}); err != nil {
				t.Errorf("RoleAdd returned error: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		if color := state.UserColor("user", "channel"); color != 0x123456 {
			t.Fatalf("UserColor = %d, want %d", color, 0x123456)
		}
	}

	<-done
}

func newColorTestState(t *testing.T) *State {
	t.Helper()

	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			{
				ID:       "guild",
				Color:    0x654321,
				Position: 0,
			},
			{
				ID:       "role-high",
				Color:    0x123456,
				Position: 10,
			},
			{
				ID:       "role-low",
				Color:    0,
				Position: 1,
			},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
				Roles:   []string{"role-high"},
			},
		},
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	return state
}

func TestVoiceStateUsesStateLock(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		VoiceStates: []*VoiceState{
			{
				GuildID:   "guild",
				ChannelID: "channel",
				UserID:    "user",
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			update := &VoiceStateUpdate{
				VoiceState: &VoiceState{
					GuildID:   "guild",
					ChannelID: "channel-" + strconv.Itoa(i),
					UserID:    "user",
				},
			}
			if err := state.voiceStateUpdate(update); err != nil {
				t.Errorf("voiceStateUpdate returned error: %v", err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if _, err := state.VoiceState("guild", "user"); err != nil {
				t.Errorf("VoiceState returned error: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

func TestThreadMemberUpdateUsesStateLock(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread, ThreadMetadata: &ThreadMetadata{}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			err := state.ThreadListSync(&ThreadListSync{
				GuildID: "guild",
				Threads: []*Channel{
					{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread, ThreadMetadata: &ThreadMetadata{}},
				},
				Members: []*ThreadMember{
					{ID: "thread", UserID: "sync-" + strconv.Itoa(i)},
				},
			})
			if err != nil {
				t.Errorf("ThreadListSync returned error: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		err := state.ThreadMemberUpdate(&ThreadMemberUpdate{
			GuildID: "guild",
			ThreadMember: &ThreadMember{
				ID:     "thread",
				UserID: "update-" + strconv.Itoa(i),
			},
		})
		if err != nil {
			t.Fatalf("ThreadMemberUpdate returned error: %v", err)
		}
	}

	<-done
}

func TestThreadMembersUpdateRemovesByUserID(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
				Members: []*ThreadMember{
					{
						ID:     "thread",
						UserID: "remove",
					},
					{
						ID:     "thread",
						UserID: "keep",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.ThreadMembersUpdate(&ThreadMembersUpdate{
		ID:             "thread",
		GuildID:        "guild",
		MemberCount:    1,
		RemovedMembers: []string{"remove"},
	})
	if err != nil {
		t.Fatalf("ThreadMembersUpdate returned error: %v", err)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(thread.Members) != 1 {
		t.Fatalf("len(thread.Members) = %d, want 1", len(thread.Members))
	}
	if thread.Members[0].UserID != "keep" {
		t.Fatalf("remaining member user ID = %q, want keep", thread.Members[0].UserID)
	}
	if thread.MemberCount != 1 {
		t.Fatalf("MemberCount = %d, want 1", thread.MemberCount)
	}
}

func TestThreadMembersUpdateRejectsNilThreadMember(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
				Members: []*ThreadMember{
					{
						ID:     "thread",
						UserID: "existing",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.ThreadMembersUpdate(&ThreadMembersUpdate{
		ID:          "thread",
		GuildID:     "guild",
		MemberCount: 2,
		AddedMembers: []AddedThreadMember{
			{
				Member: &Member{
					GuildID: "guild",
					User:    &User{ID: "user"},
				},
			},
		},
	})
	if !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("ThreadMembersUpdate returned error %v, want %v", err, ErrStateInvalidData)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(thread.Members) != 1 {
		t.Fatalf("len(thread.Members) = %d, want 1", len(thread.Members))
	}
	for i, member := range thread.Members {
		if member == nil {
			t.Fatalf("thread.Members[%d] is nil", i)
		}
	}
}

func TestThreadMembersUpdateAddsMemberWithEventGuildID(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	update := &ThreadMembersUpdate{
		ID:          "thread",
		GuildID:     "guild",
		MemberCount: 1,
		AddedMembers: []AddedThreadMember{
			{
				ThreadMember: &ThreadMember{
					ID:     "thread",
					UserID: "user",
				},
				Member: &Member{
					User: &User{ID: "user"},
				},
			},
		},
	}
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	member, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}
	if member.GuildID != "guild" {
		t.Fatalf("member.GuildID = %q, want guild", member.GuildID)
	}
	if update.AddedMembers[0].Member.GuildID != "" {
		t.Fatalf("event member GuildID = %q, want empty", update.AddedMembers[0].Member.GuildID)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(thread.Members) != 1 {
		t.Fatalf("len(thread.Members) = %d, want 1", len(thread.Members))
	}
	if thread.MemberCount != 1 {
		t.Fatalf("MemberCount = %d, want 1", thread.MemberCount)
	}
}

func TestThreadMembersUpdateRespectsMemberPresenceTracking(t *testing.T) {
	state := NewState()
	state.TrackMembers = false
	state.TrackPresences = false
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.OnInterface(&Session{StateEnabled: true}, &ThreadMembersUpdate{
		ID:          "thread",
		GuildID:     "guild",
		MemberCount: 1,
		AddedMembers: []AddedThreadMember{
			{
				ThreadMember: &ThreadMember{
					ID:     "thread",
					UserID: "user",
				},
				Member: &Member{
					GuildID: "guild",
					User:    &User{ID: "user"},
				},
				Presence: &Presence{
					User: &User{ID: "user"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(thread.Members) != 1 {
		t.Fatalf("len(thread.Members) = %d, want 1", len(thread.Members))
	}
	if _, err := state.Member("guild", "user"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Member returned error %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Presence("guild", "user"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Presence returned error %v, want %v", err, ErrStateNotFound)
	}
}

func TestThreadListSyncRespectsThreadMemberTracking(t *testing.T) {
	state := NewState()
	state.TrackThreadMembers = false
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.OnInterface(&Session{StateEnabled: true}, &ThreadListSync{
		GuildID: "guild",
		Threads: []*Channel{{
			ID:      "thread",
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
			Member: &ThreadMember{
				ID:     "thread",
				UserID: "user",
			},
			Members: []*ThreadMember{{
				ID:     "thread",
				UserID: "user",
			}},
		}},
		Members: []*ThreadMember{{
			ID:     "thread",
			UserID: "user",
		}},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if thread.Member != nil {
		t.Fatalf("thread.Member = %#v, want nil", thread.Member)
	}
	if len(thread.Members) != 0 {
		t.Fatalf("len(thread.Members) = %d, want 0", len(thread.Members))
	}
}

func TestThreadListSyncReplacesParentGuildPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:       "old-thread",
				GuildID:  "guild",
				ParentID: "parent",
				Type:     ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{
					Archived: false,
				},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.ThreadListSync(&ThreadListSync{
		GuildID:    "guild",
		ChannelIDs: []string{"parent"},
		Threads: []*Channel{
			{
				ID:       "new-thread",
				GuildID:  "guild",
				ParentID: "parent",
				Type:     ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{
					Archived: false,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ThreadListSync returned error: %v", err)
	}

	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after sync: %v", err)
	}
	if updatedGuild == oldGuild {
		t.Fatal("ThreadListSync reused the previously cached parent guild pointer")
	}
	if len(oldGuild.Threads) != 1 {
		t.Fatalf("len(oldGuild.Threads) = %d, want 1", len(oldGuild.Threads))
	}
	if oldGuild.Threads[0].ID != "old-thread" {
		t.Fatalf("old guild thread ID = %q, want old-thread", oldGuild.Threads[0].ID)
	}
	if len(updatedGuild.Threads) != 1 {
		t.Fatalf("len(updatedGuild.Threads) = %d, want 1", len(updatedGuild.Threads))
	}
	if updatedGuild.Threads[0].ID != "new-thread" {
		t.Fatalf("updated guild thread ID = %q, want new-thread", updatedGuild.Threads[0].ID)
	}
	if _, err := state.Channel("old-thread"); err == nil {
		t.Fatal("old thread remained in channel cache")
	}
	if _, err := state.Channel("new-thread"); err != nil {
		t.Fatalf("new thread missing from channel cache: %v", err)
	}
}

func TestThreadListSyncDoesNotRaceReturnedGuildThreads(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:       "thread",
				GuildID:  "guild",
				ParentID: "parent",
				Type:     ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{
					Archived: false,
				},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			for _, thread := range guild.Threads {
				if thread != nil {
					_ = thread.ID
					_ = thread.ParentID
				}
			}
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.ThreadListSync(&ThreadListSync{
			GuildID:    "guild",
			ChannelIDs: []string{"parent"},
			Threads: []*Channel{
				{
					ID:       "thread-" + strconv.Itoa(i),
					GuildID:  "guild",
					ParentID: "parent",
					Type:     ChannelTypeGuildPublicThread,
					ThreadMetadata: &ThreadMetadata{
						Archived: false,
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("ThreadListSync returned error: %v", err)
		}
	}
	<-done
}

func TestThreadMembersUpdateReplacesCachedThreadPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
				Members: []*ThreadMember{
					{ID: "thread", UserID: "remove"},
					{ID: "thread", UserID: "keep"},
				},
				MemberCount: 2,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	err = state.ThreadMembersUpdate(&ThreadMembersUpdate{
		ID:             "thread",
		GuildID:        "guild",
		MemberCount:    1,
		RemovedMembers: []string{"remove"},
	})
	if err != nil {
		t.Fatalf("ThreadMembersUpdate returned error: %v", err)
	}

	updatedThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error after update: %v", err)
	}
	if updatedThread == oldThread {
		t.Fatal("ThreadMembersUpdate reused the previously cached thread pointer")
	}
	if len(oldThread.Members) != 2 {
		t.Fatalf("len(oldThread.Members) = %d, want 2", len(oldThread.Members))
	}
	if oldThread.MemberCount != 2 {
		t.Fatalf("oldThread.MemberCount = %d, want 2", oldThread.MemberCount)
	}
	if len(updatedThread.Members) != 1 {
		t.Fatalf("len(updatedThread.Members) = %d, want 1", len(updatedThread.Members))
	}
	if updatedThread.Members[0].UserID != "keep" {
		t.Fatalf("updated member user ID = %q, want keep", updatedThread.Members[0].UserID)
	}
	if updatedThread.MemberCount != 1 {
		t.Fatalf("updatedThread.MemberCount = %d, want 1", updatedThread.MemberCount)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if guild.Threads[0] != updatedThread {
		t.Fatal("guild thread slice does not point at the updated cached thread")
	}
}

func TestThreadMembersUpdateDoesNotRaceReturnedThreadMembers(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
				Members: []*ThreadMember{
					{ID: "thread", UserID: "member"},
				},
				MemberCount: 1,
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			_ = thread.MemberCount
			for _, member := range thread.Members {
				if member != nil {
					_ = member.UserID
				}
			}
		}
	}()

	for i := 0; i < 10000; i++ {
		err := state.ThreadMembersUpdate(&ThreadMembersUpdate{
			ID:             "thread",
			GuildID:        "guild",
			MemberCount:    1,
			RemovedMembers: []string{"member-" + strconv.Itoa(i-1)},
			AddedMembers: []AddedThreadMember{
				{
					ThreadMember: &ThreadMember{
						ID:     "thread",
						UserID: "member-" + strconv.Itoa(i),
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("ThreadMembersUpdate returned error: %v", err)
		}
	}
	<-done
}

func TestThreadMemberUpdateReplacesCachedThreadPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{
				ID:      "thread",
				GuildID: "guild",
				Type:    ChannelTypeGuildPublicThread,
				Member:  &ThreadMember{ID: "thread", UserID: "old"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	err = state.ThreadMemberUpdate(&ThreadMemberUpdate{
		GuildID: "guild",
		ThreadMember: &ThreadMember{
			ID:     "thread",
			UserID: "new",
		},
	})
	if err != nil {
		t.Fatalf("ThreadMemberUpdate returned error: %v", err)
	}

	updatedThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error after update: %v", err)
	}
	if updatedThread == oldThread {
		t.Fatal("ThreadMemberUpdate reused the previously cached thread pointer")
	}
	if oldThread.Member == nil || oldThread.Member.UserID != "old" {
		t.Fatalf("old thread member = %#v, want old", oldThread.Member)
	}
	if updatedThread.Member == nil || updatedThread.Member.UserID != "new" {
		t.Fatalf("updated thread member = %#v, want new", updatedThread.Member)
	}
}

func TestGuildMemberAddMemberCountUsesStateLock(t *testing.T) {
	state := NewState()
	state.TrackMembers = false
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	const goroutines = 4
	const iterations = 1000

	start := make(chan struct{})
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			for j := 0; j < iterations; j++ {
				err := state.OnInterface(&Session{StateEnabled: true}, &GuildMemberAdd{
					Member: &Member{
						GuildID: "guild",
						User:    &User{ID: "user"},
					},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
	}
	close(start)

	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if guild.MemberCount != goroutines*iterations {
		t.Fatalf("MemberCount = %d, want %d", guild.MemberCount, goroutines*iterations)
	}
}

func TestGuildMemberRemoveMemberCountUsesStateLock(t *testing.T) {
	state := NewState()
	state.TrackMembers = false
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		MemberCount: 4000,
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	const goroutines = 4
	const iterations = 1000

	start := make(chan struct{})
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			for j := 0; j < iterations; j++ {
				err := state.OnInterface(&Session{StateEnabled: true}, &GuildMemberRemove{
					Member: &Member{
						GuildID: "guild",
						User:    &User{ID: "user"},
					},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
	}
	close(start)

	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if guild.MemberCount != 0 {
		t.Fatalf("MemberCount = %d, want 0", guild.MemberCount)
	}
}

func TestGuildCreateRespectsTrackingFlags(t *testing.T) {
	state := NewState()
	state.TrackChannels = false
	state.TrackThreads = false
	state.TrackEmojis = false
	state.TrackStickers = false
	state.TrackMembers = false
	state.TrackRoles = false
	state.TrackVoice = false
	state.TrackPresences = false

	guild := &Guild{
		ID:          "guild",
		MemberCount: 1,
		Roles:       []*Role{{ID: "role"}},
		Emojis:      []*Emoji{{ID: "emoji"}},
		Stickers:    []*Sticker{{ID: "sticker"}},
		Members: []*Member{{
			GuildID: "guild",
			User:    &User{ID: "user"},
		}},
		Presences: []*Presence{{
			User: &User{ID: "user"},
		}},
		Channels: []*Channel{{
			ID:      "channel",
			GuildID: "guild",
		}},
		Threads: []*Channel{{
			ID:      "thread",
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
			Member: &ThreadMember{
				UserID: "user",
			},
			Members: []*ThreadMember{{
				UserID: "user",
			}},
		}},
		VoiceStates: []*VoiceState{{
			GuildID: "guild",
			UserID:  "user",
		}},
	}

	err := state.OnInterface(&Session{StateEnabled: true}, &GuildCreate{Guild: guild})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	stored, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if stored.MemberCount != 1 {
		t.Fatalf("MemberCount = %d, want 1", stored.MemberCount)
	}
	if len(stored.Roles) != 0 || len(stored.Emojis) != 0 || len(stored.Stickers) != 0 ||
		len(stored.Members) != 0 || len(stored.Presences) != 0 ||
		len(stored.Channels) != 0 || len(stored.Threads) != 0 || len(stored.VoiceStates) != 0 {
		t.Fatalf("tracked guild kept disabled state: %#v", stored)
	}
	if _, err := state.Member("guild", "user"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Member returned error %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Channel("channel"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Channel returned error %v, want %v", err, ErrStateNotFound)
	}
	if len(guild.Members) != 1 || len(guild.Channels) != 1 || len(guild.Threads) != 1 {
		t.Fatal("GuildCreate event payload was mutated")
	}
}

func TestReadyRespectsTrackingFlags(t *testing.T) {
	state := NewState()
	state.TrackChannels = false
	state.TrackMembers = false
	state.TrackPresences = false
	state.TrackVoice = false

	err := state.OnInterface(&Session{StateEnabled: true}, &Ready{
		Version:   10,
		User:      &User{ID: "bot"},
		SessionID: "session",
		Guilds: []*Guild{{
			ID: "guild",
			Members: []*Member{{
				GuildID: "guild",
				User:    &User{ID: "user"},
			}},
			Presences: []*Presence{{
				User: &User{ID: "user"},
			}},
			Channels: []*Channel{{
				ID:      "channel",
				GuildID: "guild",
			}},
			VoiceStates: []*VoiceState{{
				GuildID: "guild",
				UserID:  "user",
			}},
		}},
		PrivateChannels: []*Channel{{
			ID:   "dm",
			Type: ChannelTypeDM,
		}},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.Members) != 0 || len(guild.Presences) != 0 || len(guild.Channels) != 0 || len(guild.VoiceStates) != 0 {
		t.Fatalf("Ready kept disabled state: %#v", guild)
	}
	if _, err := state.Channel("channel"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Channel returned error %v, want %v", err, ErrStateNotFound)
	}
	if _, err := state.Channel("dm"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("DM channel returned error %v, want %v", err, ErrStateNotFound)
	}
}

func TestThreadMemberTrackingFlagKeepsThread(t *testing.T) {
	state := NewState()
	state.TrackThreads = true
	state.TrackThreadMembers = false
	err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{{
			ID:      "thread",
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
			Members: []*ThreadMember{{
				UserID: "user",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	thread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(thread.Members) != 0 {
		t.Fatalf("thread members = %d, want 0", len(thread.Members))
	}
	if thread.Member != nil {
		t.Fatalf("thread.Member = %#v, want nil", thread.Member)
	}
}

func TestMemberAddDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{
			{GuildID: "guild", User: &User{ID: "user-0", Username: "zero"}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	initial := len(snapshot.Members)

	for i := 0; i < 50; i++ {
		err := state.MemberAdd(&Member{
			GuildID: "guild",
			User:    &User{ID: "user-" + strconv.Itoa(i+1), Username: strconv.Itoa(i + 1)},
		})
		if err != nil {
			t.Fatalf("MemberAdd returned error: %v", err)
		}
	}

	if len(snapshot.Members) != initial {
		t.Fatalf("snapshot.Members len = %d, want %d (snapshot mutated in place)", len(snapshot.Members), initial)
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.Members) != initial+50 {
		t.Fatalf("current.Members len = %d, want %d", len(current.Members), initial+50)
	}
}

func TestPresenceAddDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	for i := 0; i < 50; i++ {
		err := state.PresenceAdd("guild", &Presence{
			User:   &User{ID: "user-" + strconv.Itoa(i)},
			Status: StatusOnline,
		})
		if err != nil {
			t.Fatalf("PresenceAdd returned error: %v", err)
		}
	}

	if len(snapshot.Presences) != 0 {
		t.Fatalf("snapshot.Presences len = %d, want 0 (snapshot mutated in place)", len(snapshot.Presences))
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.Presences) != 50 {
		t.Fatalf("current.Presences len = %d, want 50", len(current.Presences))
	}
}

func TestPresenceUpdateDoesNotMutateMemberSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{
			{GuildID: "guild", User: &User{ID: "user", Username: "old"}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}

	err = state.OnInterface(&Session{StateEnabled: true}, &PresenceUpdate{
		GuildID: "guild",
		Presence: Presence{
			User:   &User{ID: "user", Username: "new"},
			Status: StatusOnline,
		},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if snapshot.User.Username != "old" {
		t.Fatalf("snapshot username = %q, want old (snapshot mutated in place)", snapshot.User.Username)
	}

	current, err := state.Member("guild", "user")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}
	if current.User.Username != "new" {
		t.Fatalf("current username = %q, want new", current.User.Username)
	}
}

func TestMemberRemoveSurvivesConcurrentGuildReplace(t *testing.T) {
	for iter := 0; iter < 500; iter++ {
		state := NewState()
		if err := state.GuildAdd(&Guild{
			ID: "guild",
			Members: []*Member{
				{GuildID: "guild", User: &User{ID: "user"}},
			},
		}); err != nil {
			t.Fatalf("GuildAdd returned error: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := state.MemberRemove(&Member{GuildID: "guild", User: &User{ID: "user"}}); err != nil {
				t.Errorf("MemberRemove returned error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := state.RoleAdd("guild", &Role{ID: "role-" + strconv.Itoa(iter)}); err != nil {
				t.Errorf("RoleAdd returned error: %v", err)
			}
		}()
		wg.Wait()

		if _, err := state.Member("guild", "user"); !errors.Is(err, ErrStateNotFound) {
			t.Fatalf("iteration %d: Member returned error %v, want %v", iter, err, ErrStateNotFound)
		}

		guild, err := state.Guild("guild")
		if err != nil {
			t.Fatalf("Guild returned error: %v", err)
		}
		for _, m := range guild.Members {
			if m != nil && m.User != nil && m.User.ID == "user" {
				t.Fatalf("iteration %d: removed member still present in guild.Members", iter)
			}
		}
	}
}

func TestMemberPresenceMutatorsDoNotRaceGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{
			{GuildID: "guild", User: &User{ID: "keep", Username: "keep"}},
		},
		Presences: []*Presence{
			{User: &User{ID: "keep"}, Status: StatusOnline},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			current, err := state.Guild("guild")
			if err != nil {
				return
			}
			for _, m := range current.Members {
				if m != nil && m.User != nil {
					_ = m.User.Username
				}
			}
			for _, p := range current.Presences {
				if p != nil && p.User != nil {
					_ = p.User.ID
				}
			}
			for _, m := range guild.Members {
				if m != nil && m.User != nil {
					_ = m.User.Username
				}
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		id := "user-" + strconv.Itoa(i)
		member := &Member{GuildID: "guild", User: &User{ID: id, Username: id}}
		if err := state.MemberAdd(member); err != nil {
			t.Fatalf("MemberAdd returned error: %v", err)
		}
		if err := state.PresenceAdd("guild", &Presence{User: &User{ID: id}, Status: StatusOnline}); err != nil {
			t.Fatalf("PresenceAdd returned error: %v", err)
		}
		if err := state.PresenceRemove("guild", &Presence{User: &User{ID: id}}); err != nil {
			t.Fatalf("PresenceRemove returned error: %v", err)
		}
		if err := state.MemberRemove(member); err != nil {
			t.Fatalf("MemberRemove returned error: %v", err)
		}
	}
	<-done
}

func TestGuildMembersChunkBatchUpdatesState(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	chunk := &GuildMembersChunk{
		GuildID: "guild",
		Members: []*Member{
			{User: &User{ID: "user-a", Username: "a"}},
			{User: &User{ID: "user-b", Username: "b"}},
			{User: &User{ID: "user-a", Username: "a2"}},
		},
		Presences: []*Presence{
			{User: &User{ID: "user-a"}, Status: StatusOnline},
		},
	}
	if err := state.OnInterface(&Session{StateEnabled: true}, chunk); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if len(snapshot.Members) != 0 || len(snapshot.Presences) != 0 {
		t.Fatalf("snapshot mutated in place: members=%d presences=%d", len(snapshot.Members), len(snapshot.Presences))
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.Members) != 2 {
		t.Fatalf("current.Members len = %d, want 2", len(current.Members))
	}
	if len(current.Presences) != 1 {
		t.Fatalf("current.Presences len = %d, want 1", len(current.Presences))
	}

	member, err := state.Member("guild", "user-a")
	if err != nil {
		t.Fatalf("Member returned error: %v", err)
	}
	if member.User.Username != "a2" {
		t.Fatalf("member username = %q, want a2 (duplicate entry should win)", member.User.Username)
	}
	if member.GuildID != "guild" {
		t.Fatalf("member guild ID = %q, want guild", member.GuildID)
	}
}

func TestVoiceStateUpdateDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	join := &VoiceStateUpdate{
		VoiceState: &VoiceState{GuildID: "guild", ChannelID: "channel", UserID: "user"},
	}
	if err := state.OnInterface(&Session{StateEnabled: true}, join); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if len(snapshot.VoiceStates) != 0 {
		t.Fatalf("snapshot.VoiceStates len = %d, want 0 (snapshot mutated in place)", len(snapshot.VoiceStates))
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.VoiceStates) != 1 {
		t.Fatalf("current.VoiceStates len = %d, want 1", len(current.VoiceStates))
	}

	joined := current
	leave := &VoiceStateUpdate{
		VoiceState: &VoiceState{GuildID: "guild", ChannelID: "", UserID: "user"},
	}
	if err := state.OnInterface(&Session{StateEnabled: true}, leave); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if len(joined.VoiceStates) != 1 {
		t.Fatalf("joined snapshot VoiceStates len = %d, want 1 (snapshot mutated in place)", len(joined.VoiceStates))
	}
	current, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.VoiceStates) != 0 {
		t.Fatalf("current.VoiceStates len = %d, want 0 after leave", len(current.VoiceStates))
	}
}

func TestEmojiAddDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:     "guild",
		Emojis: []*Emoji{{ID: "emoji-0", Name: "zero"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	if err := state.EmojiAdd("guild", &Emoji{ID: "emoji-1", Name: "one"}); err != nil {
		t.Fatalf("EmojiAdd returned error: %v", err)
	}
	if err := state.EmojiAdd("guild", &Emoji{ID: "emoji-0", Name: "replaced"}); err != nil {
		t.Fatalf("EmojiAdd returned error: %v", err)
	}

	if len(snapshot.Emojis) != 1 || snapshot.Emojis[0].Name != "zero" {
		t.Fatalf("snapshot.Emojis = %v, want untouched single zero emoji", snapshot.Emojis)
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.Emojis) != 2 {
		t.Fatalf("current.Emojis len = %d, want 2", len(current.Emojis))
	}

	emoji, err := state.Emoji("guild", "emoji-0")
	if err != nil {
		t.Fatalf("Emoji returned error: %v", err)
	}
	if emoji.Name != "replaced" {
		t.Fatalf("emoji name = %q, want replaced", emoji.Name)
	}

	if err := state.EmojiAdd("guild", nil); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("EmojiAdd(nil) returned error %v, want %v", err, ErrStateInvalidData)
	}
}

func TestEmojisAddBatchDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.EmojisAdd("guild", []*Emoji{
		{ID: "emoji-0", Name: "zero"},
		{ID: "emoji-1", Name: "one"},
		{ID: "emoji-0", Name: "zero-replaced"},
	})
	if err != nil {
		t.Fatalf("EmojisAdd returned error: %v", err)
	}

	if len(snapshot.Emojis) != 0 {
		t.Fatalf("snapshot.Emojis len = %d, want 0 (snapshot mutated in place)", len(snapshot.Emojis))
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.Emojis) != 2 {
		t.Fatalf("current.Emojis len = %d, want 2", len(current.Emojis))
	}

	emoji, err := state.Emoji("guild", "emoji-0")
	if err != nil {
		t.Fatalf("Emoji returned error: %v", err)
	}
	if emoji.Name != "zero-replaced" {
		t.Fatalf("emoji name = %q, want zero-replaced", emoji.Name)
	}
}

func TestGuildEmojisStickersUpdateDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Emojis:   []*Emoji{{ID: "emoji-old"}},
		Stickers: []*Sticker{{ID: "sticker-old"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.OnInterface(&Session{StateEnabled: true}, &GuildEmojisUpdate{
		GuildID: "guild",
		Emojis:  []*Emoji{{ID: "emoji-new-a"}, {ID: "emoji-new-b"}},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	err = state.OnInterface(&Session{StateEnabled: true}, &GuildStickersUpdate{
		GuildID:  "guild",
		Stickers: []*Sticker{{ID: "sticker-new"}},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if len(snapshot.Emojis) != 1 || snapshot.Emojis[0].ID != "emoji-old" {
		t.Fatalf("snapshot.Emojis = %v, want untouched emoji-old", snapshot.Emojis)
	}
	if len(snapshot.Stickers) != 1 || snapshot.Stickers[0].ID != "sticker-old" {
		t.Fatalf("snapshot.Stickers = %v, want untouched sticker-old", snapshot.Stickers)
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.Emojis) != 2 {
		t.Fatalf("current.Emojis len = %d, want 2", len(current.Emojis))
	}
	if len(current.Stickers) != 1 || current.Stickers[0].ID != "sticker-new" {
		t.Fatalf("current.Stickers = %v, want sticker-new", current.Stickers)
	}
}

func TestVoiceEmojiStickerMutatorsDoNotRaceGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Emojis:   []*Emoji{{ID: "emoji-keep", Name: "keep"}},
		Stickers: []*Sticker{{ID: "sticker-keep"}},
		VoiceStates: []*VoiceState{
			{GuildID: "guild", ChannelID: "channel", UserID: "keep"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			current, err := state.Guild("guild")
			if err != nil {
				return
			}
			for _, g := range []*Guild{guild, current} {
				for _, vs := range g.VoiceStates {
					if vs != nil {
						_ = vs.ChannelID
					}
				}
				for _, e := range g.Emojis {
					if e != nil {
						_ = e.Name
					}
				}
				for _, st := range g.Stickers {
					if st != nil {
						_ = st.ID
					}
				}
			}
		}
	}()

	session := &Session{StateEnabled: true}
	for i := 0; i < 2000; i++ {
		id := strconv.Itoa(i)
		join := &VoiceStateUpdate{
			VoiceState: &VoiceState{GuildID: "guild", ChannelID: "channel", UserID: "user-" + id},
		}
		if err := state.OnInterface(session, join); err != nil {
			t.Fatalf("OnInterface(join) returned error: %v", err)
		}
		if err := state.EmojiAdd("guild", &Emoji{ID: "emoji-" + id, Name: id}); err != nil {
			t.Fatalf("EmojiAdd returned error: %v", err)
		}
		err := state.OnInterface(session, &GuildStickersUpdate{
			GuildID:  "guild",
			Stickers: []*Sticker{{ID: "sticker-" + id}},
		})
		if err != nil {
			t.Fatalf("OnInterface(stickers) returned error: %v", err)
		}
		leave := &VoiceStateUpdate{
			VoiceState: &VoiceState{GuildID: "guild", ChannelID: "", UserID: "user-" + id},
		}
		if err := state.OnInterface(session, leave); err != nil {
			t.Fatalf("OnInterface(leave) returned error: %v", err)
		}
		err = state.OnInterface(session, &GuildEmojisUpdate{
			GuildID: "guild",
			Emojis:  []*Emoji{{ID: "emoji-keep", Name: "keep"}},
		})
		if err != nil {
			t.Fatalf("OnInterface(emojis) returned error: %v", err)
		}
	}
	<-done
}

func TestMessageAddDoesNotMutateChannelSnapshot(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel", Content: "old"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	if len(snapshot.Messages) != 0 {
		t.Fatalf("snapshot.Messages len = %d, want 0 (snapshot mutated in place)", len(snapshot.Messages))
	}

	current, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(current.Messages) != 1 {
		t.Fatalf("current.Messages len = %d, want 1", len(current.Messages))
	}
}

func TestMessageAddMergeDoesNotMutateMessageSnapshot(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel", Content: "old"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	snapshot, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}

	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel", Content: "new"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	if snapshot.Content != "old" {
		t.Fatalf("snapshot content = %q, want old (snapshot mutated in place)", snapshot.Content)
	}

	current, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if current.Content != "new" {
		t.Fatalf("current content = %q, want new", current.Content)
	}
}

func TestMessageRemoveDoesNotMutateChannelSnapshot(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel", Content: "old"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	snapshot, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	if err := state.MessageRemove(&Message{ID: "message", ChannelID: "channel"}); err != nil {
		t.Fatalf("MessageRemove returned error: %v", err)
	}

	if len(snapshot.Messages) != 1 {
		t.Fatalf("snapshot.Messages len = %d, want 1 (snapshot mutated in place)", len(snapshot.Messages))
	}

	current, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(current.Messages) != 0 {
		t.Fatalf("current.Messages len = %d, want 0", len(current.Messages))
	}

	if _, err := state.Message("channel", "message"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Message returned error %v, want %v", err, ErrStateNotFound)
	}
}

func TestMessageAddKeepsMaxMessageCount(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 2
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	for i := 0; i < 3; i++ {
		err := state.MessageAdd(&Message{ID: "message-" + strconv.Itoa(i), ChannelID: "channel"})
		if err != nil {
			t.Fatalf("MessageAdd returned error: %v", err)
		}
	}

	current, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if len(current.Messages) != 2 {
		t.Fatalf("current.Messages len = %d, want 2", len(current.Messages))
	}
	if current.Messages[0].ID != "message-1" || current.Messages[1].ID != "message-2" {
		t.Fatalf("kept messages = %q, %q; want message-1, message-2", current.Messages[0].ID, current.Messages[1].ID)
	}
}

func TestMessageMutatorsDoNotRaceChannelSnapshot(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 5
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{ID: "keep", ChannelID: "channel", Content: "keep"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	channel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			current, err := state.Channel("channel")
			if err != nil {
				return
			}
			for _, c := range []*Channel{channel, current} {
				for _, m := range c.Messages {
					if m != nil {
						_ = m.Content
					}
				}
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		id := "message-" + strconv.Itoa(i)
		if err := state.MessageAdd(&Message{ID: id, ChannelID: "channel", Content: "a"}); err != nil {
			t.Fatalf("MessageAdd returned error: %v", err)
		}
		if err := state.MessageAdd(&Message{ID: id, ChannelID: "channel", Content: "b"}); err != nil {
			t.Fatalf("MessageAdd (merge) returned error: %v", err)
		}
		if err := state.MessageRemove(&Message{ID: id, ChannelID: "channel"}); err != nil {
			t.Fatalf("MessageRemove returned error: %v", err)
		}
	}
	<-done
}

func TestGuildRemoveCleansChannelsAddedConcurrently(t *testing.T) {
	for iter := 0; iter < 500; iter++ {
		state := NewState()
		if err := state.GuildAdd(&Guild{
			ID:       "guild",
			Channels: []*Channel{{ID: "channel-a", GuildID: "guild"}},
		}); err != nil {
			t.Fatalf("GuildAdd returned error: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := state.GuildRemove(&Guild{ID: "guild"}); err != nil {
				t.Errorf("GuildRemove returned error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			// The guild may already be gone; only the leak matters.
			_ = state.ChannelAdd(&Channel{ID: "channel-b", GuildID: "guild", Type: ChannelTypeGuildText})
		}()
		wg.Wait()

		for _, channelID := range []string{"channel-a", "channel-b"} {
			if _, err := state.Channel(channelID); !errors.Is(err, ErrStateNotFound) {
				t.Fatalf("iteration %d: Channel(%q) returned error %v, want %v (channelMap leak)", iter, channelID, err, ErrStateNotFound)
			}
		}
	}
}

func TestGuildRemoveRejectsNilGuild(t *testing.T) {
	state := NewState()
	if err := state.GuildRemove(nil); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("GuildRemove(nil) returned error %v, want %v", err, ErrStateInvalidData)
	}
}

func TestGuildDeleteUnavailablePreservesGuildData(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		Name:        "guild-name",
		Icon:        "guild-icon",
		OwnerID:     "owner",
		MemberCount: 42,
		Channels: []*Channel{
			{ID: "channel", GuildID: "guild"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	err = state.OnInterface(&Session{StateEnabled: true}, &GuildDelete{Guild: &Guild{
		ID:          "guild",
		Unavailable: true,
	}})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	if snapshot.Unavailable {
		t.Fatal("held snapshot was mutated in place")
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after unavailable delete: %v", err)
	}
	if !guild.Unavailable {
		t.Fatal("guild unavailable flag was not set")
	}
	if guild.Name != "guild-name" || guild.Icon != "guild-icon" || guild.OwnerID != "owner" {
		t.Fatalf("guild data lost during outage: name=%q icon=%q owner=%q", guild.Name, guild.Icon, guild.OwnerID)
	}
	if guild.MemberCount != 42 {
		t.Fatalf("guild member count = %d, want 42", guild.MemberCount)
	}
	if len(guild.Channels) != 1 {
		t.Fatalf("guild channels len = %d, want 1", len(guild.Channels))
	}
}

func TestGuildDeleteUnavailableCachesUnknownGuildStub(t *testing.T) {
	state := NewState()

	err := state.OnInterface(&Session{StateEnabled: true}, &GuildDelete{Guild: &Guild{
		ID:          "guild",
		Unavailable: true,
	}})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if !guild.Unavailable {
		t.Fatal("guild unavailable flag was not set on stub")
	}
}
