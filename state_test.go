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
			name:  "member remove missing member",
			event: &GuildMemberRemove{},
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
			name: "role update missing role",
			event: &GuildRoleUpdate{
				GuildRole: &GuildRole{GuildID: "guild"},
			},
		},
		{
			name:  "channel create missing channel",
			event: &ChannelCreate{},
		},
		{
			name:  "channel update missing channel",
			event: &ChannelUpdate{},
		},
		{
			name:  "channel delete missing channel",
			event: &ChannelDelete{},
		},
		{
			name:  "thread create missing thread",
			event: &ThreadCreate{},
		},
		{
			name:  "thread update missing thread",
			event: &ThreadUpdate{},
		},
		{
			name:  "thread delete missing thread",
			event: &ThreadDelete{},
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
