package discordgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

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

func TestStateOnInterfaceUserUpdate(t *testing.T) {
	state := NewState()
	old := &User{ID: "user", Username: "before"}
	state.User = old

	updated := &User{ID: "user", Username: "after", GlobalName: "After"}
	if err := state.OnInterface(&Session{StateEnabled: true}, &UserUpdate{User: updated}); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	if state.User == updated {
		t.Fatal("State.User aliases the event user")
	}
	if state.User.ID != "user" || state.User.Username != "after" || state.User.GlobalName != "After" {
		t.Fatalf("State.User = %#v", state.User)
	}
	if old.Username != "before" {
		t.Fatalf("old user was mutated: %#v", old)
	}

	updated.Username = "mutated"
	if state.User.Username != "after" {
		t.Fatalf("State.User changed with event user: %#v", state.User)
	}
}

func TestStateOnInterfaceRejectsMalformedUserUpdate(t *testing.T) {
	for _, event := range []*UserUpdate{
		nil,
		{},
		{User: &User{}},
	} {
		state := NewState()
		state.User = &User{ID: "user", Username: "before"}
		if err := state.OnInterface(&Session{StateEnabled: true}, event); !errors.Is(err, ErrStateInvalidData) {
			t.Fatalf("OnInterface returned error %v, want %v", err, ErrStateInvalidData)
		}
		if state.User.Username != "before" {
			t.Fatalf("State.User changed after malformed event: %#v", state.User)
		}
	}
}

func TestStateStageInstanceLifecycle(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	initialGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	createdInstance := &StageInstance{ID: "stage", GuildID: "guild", ChannelID: "channel", Topic: "before"}
	if err := state.OnInterface(session, &StageInstanceEventCreate{StageInstance: createdInstance}); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	createdInstance.Topic = "mutated"
	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.StageInstances) != 1 || guild.StageInstances[0].Topic != "before" || guild.StageInstances[0] == createdInstance {
		t.Fatalf("created StageInstances = %#v", guild.StageInstances)
	}
	if &guild.Channels[0] != &initialGuild.Channels[0] {
		t.Fatal("stage instance create copied the unrelated channels backing array")
	}

	updatedInstance := &StageInstance{ID: "stage", GuildID: "guild", ChannelID: "channel", Topic: "after"}
	update := &StageInstanceEventUpdate{StageInstance: updatedInstance}
	if err = state.OnInterface(session, update); err != nil {
		t.Fatalf("update returned error: %v", err)
	}
	if update.BeforeUpdate == nil || update.BeforeUpdate.Topic != "before" || update.BeforeUpdate == guild.StageInstances[0] {
		t.Fatalf("BeforeUpdate = %#v", update.BeforeUpdate)
	}
	beforeDeleteGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(beforeDeleteGuild.StageInstances) != 1 || beforeDeleteGuild.StageInstances[0].Topic != "after" || beforeDeleteGuild.StageInstances[0] == updatedInstance {
		t.Fatalf("updated StageInstances = %#v", beforeDeleteGuild.StageInstances)
	}
	if &beforeDeleteGuild.Channels[0] != &guild.Channels[0] {
		t.Fatal("stage instance update copied the unrelated channels backing array")
	}
	cachedInstance := beforeDeleteGuild.StageInstances[0]
	if err = state.OnInterface(session, &GuildUpdate{Guild: &Guild{ID: "guild", Name: "updated"}}); err != nil {
		t.Fatalf("guild update returned error: %v", err)
	}
	beforeDeleteGuild, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after guild update: %v", err)
	}
	if len(beforeDeleteGuild.StageInstances) != 1 || beforeDeleteGuild.StageInstances[0] != cachedInstance {
		t.Fatalf("StageInstances after guild update = %#v, want preserved instance", beforeDeleteGuild.StageInstances)
	}

	deleted := &StageInstanceEventDelete{StageInstance: &StageInstance{ID: "stage", GuildID: "guild"}}
	if err = state.OnInterface(session, deleted); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	if deleted.BeforeDelete == nil || deleted.BeforeDelete.Topic != "after" || deleted.BeforeDelete == beforeDeleteGuild.StageInstances[0] {
		t.Fatalf("BeforeDelete = %#v", deleted.BeforeDelete)
	}
	guild, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.StageInstances) != 0 {
		t.Fatalf("StageInstances after delete = %#v", guild.StageInstances)
	}
	if &guild.Channels[0] != &beforeDeleteGuild.Channels[0] {
		t.Fatal("stage instance delete copied the unrelated channels backing array")
	}
	for i, instance := range guild.StageInstances[len(guild.StageInstances):cap(guild.StageInstances)] {
		if instance != nil {
			t.Fatalf("StageInstances backing array entry %d still retains deleted instance %q", len(guild.StageInstances)+i, instance.ID)
		}
	}
}

func TestStateRejectsMalformedStageInstanceEvents(t *testing.T) {
	tests := []interface{}{
		(*StageInstanceEventCreate)(nil),
		&StageInstanceEventCreate{},
		&StageInstanceEventCreate{StageInstance: &StageInstance{}},
		(*StageInstanceEventUpdate)(nil),
		&StageInstanceEventUpdate{},
		&StageInstanceEventUpdate{StageInstance: &StageInstance{}},
		(*StageInstanceEventDelete)(nil),
		&StageInstanceEventDelete{},
		&StageInstanceEventDelete{StageInstance: &StageInstance{}},
	}

	for _, event := range tests {
		state := NewState()
		if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
			t.Fatalf("GuildAdd returned error: %v", err)
		}
		if err := state.OnInterface(&Session{StateEnabled: true}, event); !errors.Is(err, ErrStateInvalidData) {
			t.Fatalf("OnInterface(%T) returned error %v, want %v", event, err, ErrStateInvalidData)
		}
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

func TestStateOnInterfaceRejectsRemainingNilInputs(t *testing.T) {
	tests := []struct {
		name      string
		event     interface{}
		configure func(*State)
	}{
		{name: "untyped nil event"},
		{name: "emoji update", event: (*GuildEmojisUpdate)(nil)},
		{name: "sticker update", event: (*GuildStickersUpdate)(nil)},
		{name: "thread member update", event: (*ThreadMemberUpdate)(nil)},
		{name: "thread members update", event: (*ThreadMembersUpdate)(nil)},
		{name: "thread list sync", event: (*ThreadListSync)(nil)},
		{
			name:  "message delete bulk",
			event: (*MessageDeleteBulk)(nil),
			configure: func(state *State) {
				state.MaxMessageCount = 1
			},
		},
		{name: "presence update", event: (*PresenceUpdate)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			if tt.configure != nil {
				tt.configure(state)
			}
			assertStateInvalidData(t, func() error {
				return state.OnInterface(&Session{StateEnabled: true}, tt.event)
			})
		})
	}

	t.Run("nil session", func(t *testing.T) {
		assertStateInvalidData(t, func() error {
			return NewState().OnInterface(nil, &Ready{})
		})
	})
}

func TestStateOnInterfaceIgnoresNilEventsWhenTrackingDisabled(t *testing.T) {
	tests := []struct {
		name      string
		event     interface{}
		configure func(*State)
	}{
		{
			name:  "emoji update",
			event: (*GuildEmojisUpdate)(nil),
			configure: func(state *State) {
				state.TrackEmojis = false
			},
		},
		{
			name:  "sticker update",
			event: (*GuildStickersUpdate)(nil),
			configure: func(state *State) {
				state.TrackStickers = false
			},
		},
		{
			name:  "thread member update",
			event: (*ThreadMemberUpdate)(nil),
			configure: func(state *State) {
				state.TrackThreadMembers = false
			},
		},
		{
			name:  "thread members update",
			event: (*ThreadMembersUpdate)(nil),
			configure: func(state *State) {
				state.TrackThreadMembers = false
			},
		},
		{
			name:  "thread list sync",
			event: (*ThreadListSync)(nil),
			configure: func(state *State) {
				state.TrackThreads = false
			},
		},
		{
			name:  "message delete bulk",
			event: (*MessageDeleteBulk)(nil),
			configure: func(state *State) {
				state.MaxMessageCount = 0
			},
		},
		{
			name:  "presence update",
			event: (*PresenceUpdate)(nil),
			configure: func(state *State) {
				state.TrackPresences = false
				state.TrackMembers = false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			tt.configure(state)
			if err := state.OnInterface(&Session{StateEnabled: true}, tt.event); err != nil {
				t.Fatalf("OnInterface returned error %v with tracking disabled", err)
			}
		})
	}
}

func TestThreadStateHelpersRejectNilPayloads(t *testing.T) {
	tests := []struct {
		name string
		call func(*State) error
	}{
		{name: "list sync", call: func(state *State) error { return state.ThreadListSync(nil) }},
		{name: "members update", call: func(state *State) error { return state.ThreadMembersUpdate(nil) }},
		{name: "member update", call: func(state *State) error { return state.ThreadMemberUpdate(nil) }},
		{name: "member update missing member", call: func(state *State) error {
			return state.ThreadMemberUpdate(&ThreadMemberUpdate{})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("thread state helper panicked: %v", r)
				}
			}()
			if err := tt.call(NewState()); !errors.Is(err, ErrStateInvalidData) {
				t.Fatalf("thread state helper returned error %v, want %v", err, ErrStateInvalidData)
			}
		})
	}
}

func TestThreadStateHelpersHandleNilState(t *testing.T) {
	tests := []struct {
		name string
		call func(*State) error
	}{
		{name: "list sync", call: func(state *State) error { return state.ThreadListSync(&ThreadListSync{}) }},
		{name: "members update", call: func(state *State) error { return state.ThreadMembersUpdate(&ThreadMembersUpdate{}) }},
		{name: "member update", call: func(state *State) error {
			return state.ThreadMemberUpdate(&ThreadMemberUpdate{ThreadMember: &ThreadMember{}})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("thread state helper panicked: %v", r)
				}
			}()
			var state *State
			if err := tt.call(state); !errors.Is(err, ErrNilState) {
				t.Fatalf("thread state helper returned error %v, want %v", err, ErrNilState)
			}
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

func TestStateReadHelpersHandleNilEntries(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Roles: []*Role{
			nil,
			{ID: "guild"},
			{ID: "colored", Color: 0x123456, Position: 1},
		},
		Emojis: []*Emoji{
			nil,
			{ID: "emoji"},
		},
		VoiceStates: []*VoiceState{
			nil,
			{GuildID: "guild", UserID: "user"},
		},
		Members: []*Member{
			{GuildID: "guild", User: &User{ID: "user"}, Roles: []string{"colored"}},
		},
		Channels: []*Channel{
			{
				ID:      "channel",
				GuildID: "guild",
				Messages: []*Message{
					nil,
					{ID: "message", ChannelID: "channel"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	tests := []struct {
		name  string
		check func(*testing.T)
	}{
		{name: "emoji", check: func(t *testing.T) {
			emoji, err := state.Emoji("guild", "emoji")
			if err != nil || emoji.ID != "emoji" {
				t.Fatalf("Emoji = %#v, %v; want emoji", emoji, err)
			}
		}},
		{name: "voice state", check: func(t *testing.T) {
			voiceState, err := state.VoiceState("guild", "user")
			if err != nil || voiceState.UserID != "user" {
				t.Fatalf("VoiceState = %#v, %v; want user", voiceState, err)
			}
		}},
		{name: "message", check: func(t *testing.T) {
			message, err := state.Message("channel", "message")
			if err != nil || message.ID != "message" {
				t.Fatalf("Message = %#v, %v; want message", message, err)
			}
		}},
		{name: "user color", check: func(t *testing.T) {
			if color := state.UserColor("user", "channel"); color != 0x123456 {
				t.Fatalf("UserColor = %d, want %d", color, 0x123456)
			}
		}},
		{name: "message color", check: func(t *testing.T) {
			message := &Message{ChannelID: "channel", Member: &Member{Roles: []string{"colored"}}}
			if color := state.MessageColor(message); color != 0x123456 {
				t.Fatalf("MessageColor = %d, want %d", color, 0x123456)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("state read helper panicked: %v", r)
				}
			}()
			tt.check(t)
		})
	}
}

func TestStateOnInterfaceRejectsNilGuildEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{name: "ready", event: (*Ready)(nil)},
		{name: "guild create", event: (*GuildCreate)(nil)},
		{name: "guild update", event: (*GuildUpdate)(nil)},
		{name: "guild delete", event: (*GuildDelete)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStateInvalidData(t, func() error {
				return NewState().OnInterface(&Session{StateEnabled: true}, tt.event)
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
			name:  "nil ready",
			event: (*Ready)(nil),
		},
		{
			name:  "guild create missing guild",
			event: &GuildCreate{},
		},
		{
			name:  "nil guild create",
			event: (*GuildCreate)(nil),
		},
		{
			name:  "guild update missing guild",
			event: &GuildUpdate{},
		},
		{
			name:  "nil guild update",
			event: (*GuildUpdate)(nil),
		},
		{
			name:  "nil guild delete",
			event: (*GuildDelete)(nil),
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
	for i, guild := range state.Guilds[len(state.Guilds):cap(state.Guilds)] {
		if guild != nil {
			t.Fatalf("Guilds backing array entry %d still retains the removed guild", len(state.Guilds)+i)
		}
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
	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
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
	if guild == oldGuild {
		t.Fatal("unavailable delete reused the previously cached guild pointer")
	}
	if &guild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("unavailable delete copied the unrelated channels backing array")
	}
	if &guild.Threads[0] != &oldGuild.Threads[0] {
		t.Fatal("unavailable delete copied the unrelated threads backing array")
	}
	if &guild.Members[0] != &oldGuild.Members[0] {
		t.Fatal("unavailable delete copied the unrelated members backing array")
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

	if err := state.OnInterface(&Session{StateEnabled: true}, &GuildCreate{Guild: &Guild{
		ID: "guild",
		Channels: []*Channel{
			{ID: "replacement-channel", GuildID: "guild"},
		},
		Threads: []*Channel{
			{
				ID:             "replacement-thread",
				GuildID:        "guild",
				Type:           ChannelTypeGuildPublicThread,
				ThreadMetadata: &ThreadMetadata{},
			},
		},
	}}); err != nil {
		t.Fatalf("GuildCreate returned error: %v", err)
	}
	guild, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after available create: %v", err)
	}
	if guild.Unavailable {
		t.Fatal("guild unavailable flag was not cleared")
	}
	for _, id := range []string{"channel", "thread"} {
		if _, err = state.Channel(id); !errors.Is(err, ErrStateNotFound) {
			t.Fatalf("Channel(%q) returned error %v, want %v after replacement", id, err, ErrStateNotFound)
		}
	}
	for _, id := range []string{"replacement-channel", "replacement-thread"} {
		if _, err = state.Channel(id); err != nil {
			t.Fatalf("Channel(%q) returned error after replacement: %v", id, err)
		}
	}

	if err = state.OnInterface(&Session{StateEnabled: true}, &GuildUpdate{Guild: &Guild{ID: "guild", Name: "updated"}}); err != nil {
		t.Fatalf("GuildUpdate returned error: %v", err)
	}
	for _, id := range []string{"replacement-channel", "replacement-thread"} {
		if _, err = state.Channel(id); err != nil {
			t.Fatalf("Channel(%q) returned error after partial update: %v", id, err)
		}
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
		Emojis: []*Emoji{
			{ID: "emoji", Name: "old-emoji"},
		},
		Stickers: []*Sticker{
			{ID: "sticker", Name: "old-sticker"},
		},
		Channels: []*Channel{
			{ID: "channel", GuildID: "guild"},
		},
		Threads: []*Channel{
			{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
		},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user"},
			},
		},
		Presences: []*Presence{
			{User: &User{ID: "user"}},
		},
		VoiceStates: []*VoiceState{
			{GuildID: "guild", UserID: "user"},
		},
		GuildScheduledEvents: []*GuildScheduledEvent{
			{ID: "event", GuildID: "guild", Name: "old-event"},
		},
		SoundboardSounds: []*SoundboardSound{
			{SoundID: "sound", GuildID: "guild", Name: "old-sound"},
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
	if len(updatedGuild.GuildScheduledEvents) != 1 || updatedGuild.GuildScheduledEvents[0].Name != "old-event" {
		t.Fatalf("updated guild scheduled events = %#v, want preserved old-event", updatedGuild.GuildScheduledEvents)
	}
	if len(updatedGuild.SoundboardSounds) != 1 || updatedGuild.SoundboardSounds[0].Name != "old-sound" {
		t.Fatalf("updated guild soundboard sounds = %#v, want preserved old-sound", updatedGuild.SoundboardSounds)
	}
	if len(updatedGuild.Roles) != len(oldGuild.Roles) || &updatedGuild.Roles[0] != &oldGuild.Roles[0] {
		t.Fatal("GuildAdd copied the preserved roles backing array")
	}
	if len(updatedGuild.Emojis) != len(oldGuild.Emojis) || &updatedGuild.Emojis[0] != &oldGuild.Emojis[0] {
		t.Fatal("GuildAdd copied the preserved emojis backing array")
	}
	if len(updatedGuild.Stickers) != len(oldGuild.Stickers) || &updatedGuild.Stickers[0] != &oldGuild.Stickers[0] {
		t.Fatal("GuildAdd copied the preserved stickers backing array")
	}
	if len(updatedGuild.Members) != len(oldGuild.Members) || &updatedGuild.Members[0] != &oldGuild.Members[0] {
		t.Fatal("GuildAdd copied the preserved members backing array")
	}
	if len(updatedGuild.Presences) != len(oldGuild.Presences) || &updatedGuild.Presences[0] != &oldGuild.Presences[0] {
		t.Fatal("GuildAdd copied the preserved presences backing array")
	}
	if len(updatedGuild.Channels) != len(oldGuild.Channels) || &updatedGuild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("GuildAdd copied the preserved channels backing array")
	}
	if len(updatedGuild.Threads) != len(oldGuild.Threads) || &updatedGuild.Threads[0] != &oldGuild.Threads[0] {
		t.Fatal("GuildAdd copied the preserved threads backing array")
	}
	if len(updatedGuild.VoiceStates) != len(oldGuild.VoiceStates) || &updatedGuild.VoiceStates[0] != &oldGuild.VoiceStates[0] {
		t.Fatal("GuildAdd copied the preserved voice states backing array")
	}
	if len(updatedGuild.GuildScheduledEvents) != len(oldGuild.GuildScheduledEvents) || &updatedGuild.GuildScheduledEvents[0] != &oldGuild.GuildScheduledEvents[0] {
		t.Fatal("GuildAdd copied the preserved scheduled events backing array")
	}
	if len(updatedGuild.SoundboardSounds) != len(oldGuild.SoundboardSounds) || &updatedGuild.SoundboardSounds[0] != &oldGuild.SoundboardSounds[0] {
		t.Fatal("GuildAdd copied the preserved soundboard sounds backing array")
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
	if &updatedGuild.Roles[0] != &oldGuild.Roles[0] {
		t.Fatal("GuildMemberAdd copied the unrelated roles backing array")
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

func TestGuildMemberRemoveClearsTrackedPresence(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:          "guild",
		MemberCount: 2,
		Members: []*Member{
			{GuildID: "guild", User: &User{ID: "keep"}},
			{GuildID: "guild", User: &User{ID: "remove"}},
		},
		Presences: []*Presence{
			{User: &User{ID: "keep"}},
			{User: &User{ID: "remove"}, Activities: []*Activity{{Name: "retained"}}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	err := state.OnInterface(&Session{StateEnabled: true}, &GuildMemberRemove{
		Member: &Member{GuildID: "guild", User: &User{ID: "remove"}},
	})
	if err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	if _, err = state.Presence("guild", "remove"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Presence returned error %v, want %v", err, ErrStateNotFound)
	}
	if _, err = state.Presence("guild", "keep"); err != nil {
		t.Fatalf("kept presence returned error: %v", err)
	}
}

func TestMemberRemoveReleasesRemovedMemberReference(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
		Members: []*Member{
			{GuildID: "guild", User: &User{ID: "keep"}},
			{GuildID: "guild", User: &User{ID: "remove"}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	if err := state.MemberRemove(&Member{GuildID: "guild", User: &User{ID: "remove"}}); err != nil {
		t.Fatalf("MemberRemove returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if &guild.Members[0] == &oldGuild.Members[0] {
		t.Fatal("MemberRemove reused the members backing array")
	}
	if &guild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("MemberRemove copied the unrelated channels backing array")
	}
	for i, member := range guild.Members[len(guild.Members):cap(guild.Members)] {
		if member != nil {
			t.Fatalf("Members backing array entry %d still retains removed member %q", len(guild.Members)+i, member.User.ID)
		}
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

func TestThreadListSyncReleasesRemovedThreadReferences(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{
			{ID: "old-thread-1", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
			{ID: "old-thread-2", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
			{ID: "old-thread-3", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	if err := state.ThreadListSync(&ThreadListSync{
		GuildID: "guild",
		Threads: []*Channel{
			{ID: "new-thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
		},
	}); err != nil {
		t.Fatalf("ThreadListSync returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after sync: %v", err)
	}
	for i, thread := range guild.Threads[len(guild.Threads):cap(guild.Threads)] {
		if thread != nil {
			t.Fatalf("Threads backing array entry %d still retains removed thread %q", len(guild.Threads)+i, thread.ID)
		}
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
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
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
	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
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
	if updatedPresence.ClientStatus.Desktop != "" {
		t.Fatalf("updated desktop status = %q, want empty for an inactive client", updatedPresence.ClientStatus.Desktop)
	}
	if updatedPresence.ClientStatus.Mobile != StatusIdle {
		t.Fatalf("updated mobile status = %q, want %q", updatedPresence.ClientStatus.Mobile, StatusIdle)
	}
	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after update: %v", err)
	}
	if &updatedGuild.Presences[0] == &oldGuild.Presences[0] {
		t.Fatal("PresenceAdd reused the presences backing array")
	}
	if &updatedGuild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("PresenceAdd copied the unrelated channels backing array")
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

func TestPresenceRemoveReleasesRemovedPresenceReference(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
		Presences: []*Presence{
			{User: &User{ID: "keep"}},
			{User: &User{ID: "remove"}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	if err := state.PresenceRemove("guild", &Presence{User: &User{ID: "remove"}}); err != nil {
		t.Fatalf("PresenceRemove returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if &guild.Presences[0] == &oldGuild.Presences[0] {
		t.Fatal("PresenceRemove reused the presences backing array")
	}
	if &guild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("PresenceRemove copied the unrelated channels backing array")
	}
	for i, presence := range guild.Presences[len(guild.Presences):cap(guild.Presences)] {
		if presence != nil {
			t.Fatalf("Presences backing array entry %d still retains removed presence %q", len(guild.Presences)+i, presence.User.ID)
		}
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

func TestStateGuildSoundboardSoundLifecycle(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	initialGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	created := &SoundboardSound{
		SoundID: "sound",
		GuildID: "guild",
		Name:    "created",
		User: &User{
			ID:                   "user",
			Username:             "creator",
			AvatarDecorationData: &AvatarDecorationData{Asset: "created-decoration"},
			Collectibles:         &Collectibles{Nameplate: &Nameplate{Asset: "created-nameplate"}},
		},
	}
	if err := state.OnInterface(session, &GuildSoundboardSoundCreate{SoundboardSound: created}); err != nil {
		t.Fatalf("OnInterface(create) returned error: %v", err)
	}
	created.Name = "mutated"
	created.User.Username = "mutated"
	created.User.AvatarDecorationData.Asset = "mutated"

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.SoundboardSounds) != 1 || guild.SoundboardSounds[0].Name != "created" {
		t.Fatalf("cached sounds = %#v, want one created sound", guild.SoundboardSounds)
	}
	if &guild.Channels[0] != &initialGuild.Channels[0] {
		t.Fatal("soundboard create copied the unrelated channels backing array")
	}
	if guild.SoundboardSounds[0].User.Username != "creator" ||
		guild.SoundboardSounds[0].User.AvatarDecorationData.Asset != "created-decoration" {
		t.Fatalf("cached creator = %#v, want an immutable copy", guild.SoundboardSounds[0].User)
	}

	replacement := &SoundboardSound{
		SoundID: "sound",
		GuildID: "guild",
		Name:    "replacement",
		User: &User{
			ID:                   "user",
			Username:             "old-creator",
			AvatarDecorationData: &AvatarDecorationData{Asset: "old-decoration"},
			Collectibles:         &Collectibles{Nameplate: &Nameplate{Asset: "old-nameplate"}},
		},
	}
	if err := state.OnInterface(session, &GuildSoundboardSoundCreate{SoundboardSound: replacement}); err != nil {
		t.Fatalf("OnInterface(replacement create) returned error: %v", err)
	}

	beforeUpdateGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(beforeUpdateGuild.SoundboardSounds) != 1 {
		t.Fatalf("len(SoundboardSounds) = %d, want 1 after replacement", len(beforeUpdateGuild.SoundboardSounds))
	}
	if &beforeUpdateGuild.Channels[0] != &guild.Channels[0] {
		t.Fatal("soundboard replacement copied the unrelated channels backing array")
	}
	old := beforeUpdateGuild.SoundboardSounds[0]
	update := &GuildSoundboardSoundUpdate{SoundboardSound: &SoundboardSound{
		SoundID: "sound",
		GuildID: "guild",
		Name:    "updated",
		User: &User{
			ID:                   "user",
			Username:             "new-creator",
			AvatarDecorationData: &AvatarDecorationData{Asset: "new-decoration"},
			Collectibles:         &Collectibles{Nameplate: &Nameplate{Asset: "new-nameplate"}},
		},
	}}
	if err := state.OnInterface(session, update); err != nil {
		t.Fatalf("OnInterface(update) returned error: %v", err)
	}
	if update.BeforeUpdate == nil || update.BeforeUpdate.Name != "replacement" {
		t.Fatalf("BeforeUpdate = %#v, want replacement", update.BeforeUpdate)
	}
	aliasesOld := update.BeforeUpdate == old ||
		update.BeforeUpdate.User == old.User ||
		update.BeforeUpdate.User.AvatarDecorationData == old.User.AvatarDecorationData ||
		update.BeforeUpdate.User.Collectibles == old.User.Collectibles ||
		update.BeforeUpdate.User.Collectibles.Nameplate == old.User.Collectibles.Nameplate
	if aliasesOld {
		t.Fatal("BeforeUpdate aliases the previous cached sound")
	}

	old.Name = "mutated-old"
	old.User.Username = "mutated-old"
	old.User.AvatarDecorationData.Asset = "mutated-old"
	old.User.Collectibles.Nameplate.Asset = "mutated-old"
	if update.BeforeUpdate.Name != "replacement" || update.BeforeUpdate.User.Username != "old-creator" ||
		update.BeforeUpdate.User.AvatarDecorationData.Asset != "old-decoration" ||
		update.BeforeUpdate.User.Collectibles.Nameplate.Asset != "old-nameplate" {
		t.Fatalf("BeforeUpdate was mutated through the old snapshot: %#v", update.BeforeUpdate)
	}

	beforeDeleteGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	updated := beforeDeleteGuild.SoundboardSounds[0]
	update.SoundboardSound.Name = "mutated-update"
	update.SoundboardSound.User.Username = "mutated-update"
	update.BeforeUpdate.Name = "mutated-before"
	if updated.Name != "updated" || updated.User.Username != "new-creator" {
		t.Fatalf("cached updated sound = %#v, want an immutable copy", updated)
	}
	if &beforeDeleteGuild.Channels[0] != &beforeUpdateGuild.Channels[0] {
		t.Fatal("soundboard update copied the unrelated channels backing array")
	}

	deleted := &GuildSoundboardSoundDelete{GuildID: "guild", SoundID: "sound"}
	if err := state.OnInterface(session, deleted); err != nil {
		t.Fatalf("OnInterface(delete) returned error: %v", err)
	}
	if deleted.BeforeDelete == nil || deleted.BeforeDelete.Name != "updated" {
		t.Fatalf("BeforeDelete = %#v, want updated", deleted.BeforeDelete)
	}
	aliasesUpdated := deleted.BeforeDelete == updated ||
		deleted.BeforeDelete.User == updated.User ||
		deleted.BeforeDelete.User.AvatarDecorationData == updated.User.AvatarDecorationData ||
		deleted.BeforeDelete.User.Collectibles == updated.User.Collectibles ||
		deleted.BeforeDelete.User.Collectibles.Nameplate == updated.User.Collectibles.Nameplate
	if aliasesUpdated {
		t.Fatal("BeforeDelete aliases the removed cached sound")
	}
	updated.Name = "mutated-deleted"
	updated.User.Username = "mutated-deleted"
	if deleted.BeforeDelete.Name != "updated" || deleted.BeforeDelete.User.Username != "new-creator" {
		t.Fatalf("BeforeDelete was mutated through the removed snapshot: %#v", deleted.BeforeDelete)
	}

	guild, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.SoundboardSounds) != 0 {
		t.Fatalf("len(SoundboardSounds) = %d, want 0 after delete", len(guild.SoundboardSounds))
	}
	if &guild.Channels[0] != &beforeDeleteGuild.Channels[0] {
		t.Fatal("soundboard delete copied the unrelated channels backing array")
	}
	for i, sound := range guild.SoundboardSounds[len(guild.SoundboardSounds):cap(guild.SoundboardSounds)] {
		if sound != nil {
			t.Fatalf("SoundboardSounds backing array entry %d still retains deleted sound %q", len(guild.SoundboardSounds)+i, sound.SoundID)
		}
	}
}

func TestStateGuildSoundboardSoundsBulkReplace(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
		SoundboardSounds: []*SoundboardSound{
			{SoundID: "old", GuildID: "guild", Name: "old"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	sounds := []*SoundboardSound{
		{
			SoundID: "new-a",
			GuildID: "guild",
			Name:    "new-a",
			User: &User{
				ID:                   "user",
				Username:             "creator",
				AvatarDecorationData: &AvatarDecorationData{Asset: "decoration"},
				Collectibles:         &Collectibles{Nameplate: &Nameplate{Asset: "nameplate"}},
			},
		},
		{SoundID: "new-b", GuildID: "guild", Name: "new-b"},
	}
	bulk := &GuildSoundboardSoundsUpdate{GuildID: "guild", SoundboardSounds: sounds}
	if err := state.OnInterface(&Session{StateEnabled: true}, bulk); err != nil {
		t.Fatalf("OnInterface(bulk) returned error: %v", err)
	}
	sounds[0].Name = "mutated"
	sounds[0].User.Username = "mutated"
	sounds[0].User.AvatarDecorationData.Asset = "mutated"
	sounds[0].User.Collectibles.Nameplate.Asset = "mutated"
	sounds[1] = &SoundboardSound{SoundID: "mutated"}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.SoundboardSounds) != 2 || current.SoundboardSounds[0].Name != "new-a" ||
		current.SoundboardSounds[1].Name != "new-b" {
		t.Fatalf("current sounds = %#v, want new-a and new-b", current.SoundboardSounds)
	}
	user := current.SoundboardSounds[0].User
	if user.Username != "creator" || user.AvatarDecorationData.Asset != "decoration" ||
		user.Collectibles.Nameplate.Asset != "nameplate" {
		t.Fatalf("current creator = %#v, want an immutable copy", user)
	}
	if len(snapshot.SoundboardSounds) != 1 || snapshot.SoundboardSounds[0].Name != "old" {
		t.Fatalf("old snapshot sounds = %#v, want old", snapshot.SoundboardSounds)
	}
	if &current.Channels[0] != &snapshot.Channels[0] {
		t.Fatal("soundboard bulk update copied the unrelated channels backing array")
	}

	bulkSnapshot := current
	if err := state.OnInterface(&Session{StateEnabled: true}, &GuildSoundboardSoundsUpdate{
		GuildID:          "guild",
		SoundboardSounds: []*SoundboardSound{},
	}); err != nil {
		t.Fatalf("OnInterface(empty bulk) returned error: %v", err)
	}
	current, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if current.SoundboardSounds == nil || len(current.SoundboardSounds) != 0 {
		t.Fatalf("current sounds = %#v, want a non-nil empty slice", current.SoundboardSounds)
	}
	if len(bulkSnapshot.SoundboardSounds) != 2 || bulkSnapshot.SoundboardSounds[0].Name != "new-a" {
		t.Fatalf("bulk snapshot sounds = %#v, want untouched new sounds", bulkSnapshot.SoundboardSounds)
	}
}

func TestStateSoundboardSoundsReplace(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		SoundboardSounds: []*SoundboardSound{
			{SoundID: "old", GuildID: "guild", Name: "old"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	if err := state.OnInterface(session, &SoundboardSounds{
		GuildID: "guild",
		SoundboardSounds: []*SoundboardSound{
			{SoundID: "new-a", GuildID: "guild", Name: "new-a"},
			{SoundID: "new-b", GuildID: "guild", Name: "new-b"},
		},
	}); err != nil {
		t.Fatalf("OnInterface(response) returned error: %v", err)
	}
	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.SoundboardSounds) != 2 || current.SoundboardSounds[0].Name != "new-a" ||
		current.SoundboardSounds[1].Name != "new-b" {
		t.Fatalf("current sounds = %#v, want new-a and new-b", current.SoundboardSounds)
	}
	if len(snapshot.SoundboardSounds) != 1 || snapshot.SoundboardSounds[0].Name != "old" {
		t.Fatalf("old snapshot sounds = %#v, want untouched old sound", snapshot.SoundboardSounds)
	}

	responseSnapshot := current
	if err := state.OnInterface(session, &SoundboardSounds{
		GuildID:          "guild",
		SoundboardSounds: []*SoundboardSound{},
	}); err != nil {
		t.Fatalf("OnInterface(empty response) returned error: %v", err)
	}
	current, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if current.SoundboardSounds == nil || len(current.SoundboardSounds) != 0 {
		t.Fatalf("current sounds = %#v, want a non-nil empty slice", current.SoundboardSounds)
	}
	if len(responseSnapshot.SoundboardSounds) != 2 || responseSnapshot.SoundboardSounds[0].Name != "new-a" {
		t.Fatalf("response snapshot sounds = %#v, want untouched new sounds", responseSnapshot.SoundboardSounds)
	}
}

func TestStateOnInterfaceRejectsMalformedGuildSoundboardEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{name: "nil create", event: (*GuildSoundboardSoundCreate)(nil)},
		{name: "create missing sound", event: &GuildSoundboardSoundCreate{}},
		{
			name:  "create missing sound id",
			event: &GuildSoundboardSoundCreate{SoundboardSound: &SoundboardSound{GuildID: "guild"}},
		},
		{
			name:  "create missing guild id",
			event: &GuildSoundboardSoundCreate{SoundboardSound: &SoundboardSound{SoundID: "sound"}},
		},
		{name: "nil update", event: (*GuildSoundboardSoundUpdate)(nil)},
		{name: "update missing sound", event: &GuildSoundboardSoundUpdate{}},
		{
			name:  "update missing sound id",
			event: &GuildSoundboardSoundUpdate{SoundboardSound: &SoundboardSound{GuildID: "guild"}},
		},
		{
			name:  "update missing guild id",
			event: &GuildSoundboardSoundUpdate{SoundboardSound: &SoundboardSound{SoundID: "sound"}},
		},
		{name: "nil delete", event: (*GuildSoundboardSoundDelete)(nil)},
		{name: "delete missing sound id", event: &GuildSoundboardSoundDelete{GuildID: "guild"}},
		{name: "delete missing guild id", event: &GuildSoundboardSoundDelete{SoundID: "sound"}},
		{name: "nil bulk update", event: (*GuildSoundboardSoundsUpdate)(nil)},
		{name: "bulk update missing guild id", event: &GuildSoundboardSoundsUpdate{SoundboardSounds: []*SoundboardSound{}}},
		{name: "bulk update missing sounds", event: &GuildSoundboardSoundsUpdate{GuildID: "guild"}},
		{
			name: "bulk update nil sound",
			event: &GuildSoundboardSoundsUpdate{
				GuildID:          "guild",
				SoundboardSounds: []*SoundboardSound{nil},
			},
		},
		{
			name: "bulk update missing sound id",
			event: &GuildSoundboardSoundsUpdate{
				GuildID:          "guild",
				SoundboardSounds: []*SoundboardSound{{GuildID: "guild"}},
			},
		},
		{name: "nil response", event: (*SoundboardSounds)(nil)},
		{name: "response missing guild id", event: &SoundboardSounds{SoundboardSounds: []*SoundboardSound{}}},
		{name: "response missing sounds", event: &SoundboardSounds{GuildID: "guild"}},
		{
			name: "response nil sound",
			event: &SoundboardSounds{
				GuildID:          "guild",
				SoundboardSounds: []*SoundboardSound{nil},
			},
		},
		{
			name: "response missing sound id",
			event: &SoundboardSounds{
				GuildID:          "guild",
				SoundboardSounds: []*SoundboardSound{{GuildID: "guild"}},
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

func TestStateGuildSoundboardEventsIgnoreUnknownIDs(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		SoundboardSounds: []*SoundboardSound{
			{SoundID: "known", GuildID: "guild", Name: "known"},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	unknownDelete := &GuildSoundboardSoundDelete{GuildID: "guild", SoundID: "unknown"}
	if err := state.OnInterface(session, unknownDelete); err != nil {
		t.Fatalf("OnInterface(unknown delete) returned error: %v", err)
	}
	if unknownDelete.BeforeDelete != nil {
		t.Fatalf("BeforeDelete = %#v, want nil for unknown sound", unknownDelete.BeforeDelete)
	}
	unknownGuildEvents := []interface{}{
		&GuildSoundboardSoundCreate{SoundboardSound: &SoundboardSound{GuildID: "unknown", SoundID: "sound"}},
		&GuildSoundboardSoundUpdate{SoundboardSound: &SoundboardSound{GuildID: "unknown", SoundID: "sound"}},
		&GuildSoundboardSoundDelete{GuildID: "unknown", SoundID: "sound"},
		&GuildSoundboardSoundsUpdate{
			GuildID:          "unknown",
			SoundboardSounds: []*SoundboardSound{{GuildID: "unknown", SoundID: "sound"}},
		},
		&SoundboardSounds{
			GuildID:          "unknown",
			SoundboardSounds: []*SoundboardSound{{GuildID: "unknown", SoundID: "sound"}},
		},
	}
	for _, event := range unknownGuildEvents {
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface(%T) returned error for unknown guild: %v", event, err)
		}
	}

	unknownUpdate := &GuildSoundboardSoundUpdate{SoundboardSound: &SoundboardSound{
		GuildID: "guild",
		SoundID: "new",
		Name:    "new",
	}}
	if err := state.OnInterface(session, unknownUpdate); err != nil {
		t.Fatalf("OnInterface(unknown update) returned error: %v", err)
	}
	if unknownUpdate.BeforeUpdate != nil {
		t.Fatalf("BeforeUpdate = %#v, want nil for uncached sound", unknownUpdate.BeforeUpdate)
	}

	current, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(current.SoundboardSounds) != 2 || current.SoundboardSounds[0].Name != "known" ||
		current.SoundboardSounds[1].Name != "new" {
		t.Fatalf("current sounds = %#v, want known and new", current.SoundboardSounds)
	}
	if len(snapshot.SoundboardSounds) != 1 || snapshot.SoundboardSounds[0].Name != "known" {
		t.Fatalf("old snapshot sounds = %#v, want untouched known sound", snapshot.SoundboardSounds)
	}
	if _, err := state.Guild("unknown"); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Guild(unknown) returned error %v, want %v", err, ErrStateNotFound)
	}
}

func TestStateGuildSoundboardSoundsDoNotRaceGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		SoundboardSounds: []*SoundboardSound{
			{SoundID: "sound", GuildID: "guild", Name: "initial", User: &User{ID: "user", Username: "initial"}},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < 1000; i++ {
			guild, err := state.Guild("guild")
			if err != nil {
				errCh <- err
				return
			}
			for _, current := range []*Guild{snapshot, guild} {
				for _, sound := range current.SoundboardSounds {
					if sound != nil {
						_ = sound.Name
						if sound.User != nil {
							_ = sound.User.Username
						}
					}
				}
			}
		}
		errCh <- nil
	}()

	session := &Session{StateEnabled: true}
	for i := 0; i < 1000; i++ {
		name := strconv.Itoa(i)
		if err := state.OnInterface(session, &GuildSoundboardSoundUpdate{SoundboardSound: &SoundboardSound{
			SoundID: "sound",
			GuildID: "guild",
			Name:    "updated-" + name,
			User:    &User{ID: "user", Username: name},
		}}); err != nil {
			t.Fatalf("OnInterface(update) returned error: %v", err)
		}
		if err := state.OnInterface(session, &GuildSoundboardSoundCreate{SoundboardSound: &SoundboardSound{
			SoundID: "temporary",
			GuildID: "guild",
			Name:    name,
		}}); err != nil {
			t.Fatalf("OnInterface(create) returned error: %v", err)
		}
		deleteTemporary := &GuildSoundboardSoundDelete{GuildID: "guild", SoundID: "temporary"}
		if err := state.OnInterface(session, deleteTemporary); err != nil {
			t.Fatalf("OnInterface(delete) returned error: %v", err)
		}
		if err := state.OnInterface(session, &SoundboardSounds{
			GuildID: "guild",
			SoundboardSounds: []*SoundboardSound{
				{SoundID: "sound", GuildID: "guild", Name: "bulk-" + name, User: &User{ID: "user", Username: name}},
			},
		}); err != nil {
			t.Fatalf("OnInterface(bulk) returned error: %v", err)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("concurrent Guild returned error: %v", err)
	}

	if snapshot.SoundboardSounds[0].Name != "initial" || snapshot.SoundboardSounds[0].User.Username != "initial" {
		t.Fatalf("old snapshot sound = %#v, want untouched initial sound", snapshot.SoundboardSounds[0])
	}
}

func TestStateGuildScheduledEventLifecycle(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	initialGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	created := &GuildScheduledEvent{
		ID:        "event",
		GuildID:   "guild",
		Name:      "created",
		Creator:   &User{ID: "user", Username: "creator"},
		UserCount: 1,
		RecurrenceRule: &GuildScheduledEventRecurrenceRule{
			ByMonthDay: []int{12},
		},
	}
	if err := state.OnInterface(session, &GuildScheduledEventCreate{GuildScheduledEvent: created}); err != nil {
		t.Fatalf("OnInterface(create) returned error: %v", err)
	}
	created.Name = "mutated"
	created.RecurrenceRule.ByMonthDay[0] = 31

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.GuildScheduledEvents) != 1 || guild.GuildScheduledEvents[0].Name != "created" {
		t.Fatalf("cached events = %#v, want one created event", guild.GuildScheduledEvents)
	}
	if &guild.Channels[0] != &initialGuild.Channels[0] {
		t.Fatal("scheduled event create copied the unrelated channels backing array")
	}
	if guild.GuildScheduledEvents[0].RecurrenceRule.ByMonthDay[0] != 12 {
		t.Fatalf("cached recurrence day = %d, want 12", guild.GuildScheduledEvents[0].RecurrenceRule.ByMonthDay[0])
	}

	replacement := &GuildScheduledEvent{
		ID:      "event",
		GuildID: "guild",
		Name:    "replacement",
		Creator: &User{
			ID:                   "user",
			Username:             "old-creator",
			AvatarDecorationData: &AvatarDecorationData{Asset: "old-decoration"},
			Collectibles:         &Collectibles{Nameplate: &Nameplate{Asset: "old-nameplate"}},
		},
		RecurrenceRule: &GuildScheduledEventRecurrenceRule{
			ByMonthDay: []int{15},
		},
	}
	if err := state.OnInterface(session, &GuildScheduledEventCreate{GuildScheduledEvent: replacement}); err != nil {
		t.Fatalf("OnInterface(replacement create) returned error: %v", err)
	}

	beforeUpdateGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(beforeUpdateGuild.GuildScheduledEvents) != 1 {
		t.Fatalf("len(GuildScheduledEvents) = %d, want 1 after replacement", len(beforeUpdateGuild.GuildScheduledEvents))
	}
	if &beforeUpdateGuild.Channels[0] != &guild.Channels[0] {
		t.Fatal("scheduled event replacement copied the unrelated channels backing array")
	}
	old := beforeUpdateGuild.GuildScheduledEvents[0]
	update := &GuildScheduledEventUpdate{GuildScheduledEvent: &GuildScheduledEvent{
		ID:      "event",
		GuildID: "guild",
		Name:    "updated",
	}}
	if err := state.OnInterface(session, update); err != nil {
		t.Fatalf("OnInterface(update) returned error: %v", err)
	}
	if update.BeforeUpdate == nil || update.BeforeUpdate.Name != "replacement" {
		t.Fatalf("BeforeUpdate = %#v, want replacement", update.BeforeUpdate)
	}
	aliasesOld := update.BeforeUpdate == old ||
		update.BeforeUpdate.Creator == old.Creator ||
		update.BeforeUpdate.RecurrenceRule == old.RecurrenceRule
	if aliasesOld {
		t.Fatal("BeforeUpdate aliases the previous cached event")
	}

	old.Name = "mutated-old"
	old.Creator.Username = "mutated-creator"
	old.Creator.AvatarDecorationData.Asset = "mutated-decoration"
	old.Creator.Collectibles.Nameplate.Asset = "mutated-nameplate"
	old.RecurrenceRule.ByMonthDay[0] = 30
	if update.BeforeUpdate.Name != "replacement" || update.BeforeUpdate.Creator.Username != "old-creator" {
		t.Fatalf("BeforeUpdate was mutated through old snapshot: %#v", update.BeforeUpdate)
	}
	creator := update.BeforeUpdate.Creator
	if creator.AvatarDecorationData.Asset != "old-decoration" ||
		creator.Collectibles.Nameplate.Asset != "old-nameplate" {
		t.Fatalf("BeforeUpdate creator data was mutated through old snapshot: %#v", update.BeforeUpdate.Creator)
	}
	if update.BeforeUpdate.RecurrenceRule.ByMonthDay[0] != 15 {
		t.Fatalf("BeforeUpdate recurrence day = %d, want 15", update.BeforeUpdate.RecurrenceRule.ByMonthDay[0])
	}

	beforeDeleteGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	updated := beforeDeleteGuild.GuildScheduledEvents[0]
	update.GuildScheduledEvent.Name = "mutated-update"
	update.BeforeUpdate.Name = "mutated-before"
	if updated.Name != "updated" {
		t.Fatalf("cached updated event name = %q, want updated", updated.Name)
	}
	if &beforeDeleteGuild.Channels[0] != &beforeUpdateGuild.Channels[0] {
		t.Fatal("scheduled event update copied the unrelated channels backing array")
	}

	deleted := &GuildScheduledEventDelete{GuildScheduledEvent: &GuildScheduledEvent{ID: "event", GuildID: "guild"}}
	if err := state.OnInterface(session, deleted); err != nil {
		t.Fatalf("OnInterface(delete) returned error: %v", err)
	}
	if deleted.BeforeDelete == nil || deleted.BeforeDelete.Name != "updated" {
		t.Fatalf("BeforeDelete = %#v, want updated", deleted.BeforeDelete)
	}
	if deleted.BeforeDelete == updated {
		t.Fatal("BeforeDelete aliases the removed cached event")
	}
	updated.Name = "mutated-deleted"
	if deleted.BeforeDelete.Name != "updated" {
		t.Fatalf("BeforeDelete name = %q, want updated", deleted.BeforeDelete.Name)
	}

	guild, err = state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.GuildScheduledEvents) != 0 {
		t.Fatalf("len(GuildScheduledEvents) = %d, want 0 after delete", len(guild.GuildScheduledEvents))
	}
	if &guild.Channels[0] != &beforeDeleteGuild.Channels[0] {
		t.Fatal("scheduled event delete copied the unrelated channels backing array")
	}
	for i, event := range guild.GuildScheduledEvents[len(guild.GuildScheduledEvents):cap(guild.GuildScheduledEvents)] {
		if event != nil {
			t.Fatalf("GuildScheduledEvents backing array entry %d still retains deleted event %q", len(guild.GuildScheduledEvents)+i, event.ID)
		}
	}
}

func TestStateGuildScheduledEventUserCount(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
		GuildScheduledEvents: []*GuildScheduledEvent{
			{ID: "event", GuildID: "guild", UserCount: 1},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	session := &Session{StateEnabled: true}
	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	unknown := []interface{}{
		&GuildScheduledEventUserAdd{GuildScheduledEventID: "unknown", GuildID: "guild", UserID: "user"},
		&GuildScheduledEventUserRemove{GuildScheduledEventID: "unknown", GuildID: "guild", UserID: "user"},
		&GuildScheduledEventUserAdd{GuildScheduledEventID: "event", GuildID: "unknown", UserID: "user"},
		&GuildScheduledEventUserRemove{GuildScheduledEventID: "event", GuildID: "unknown", UserID: "user"},
	}
	for _, event := range unknown {
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface(%T) returned error for unknown ID: %v", event, err)
		}
	}

	events := []interface{}{
		&GuildScheduledEventUserAdd{GuildScheduledEventID: "event", GuildID: "guild", UserID: "user"},
		&GuildScheduledEventUserRemove{GuildScheduledEventID: "event", GuildID: "guild", UserID: "user"},
		&GuildScheduledEventUserRemove{GuildScheduledEventID: "event", GuildID: "guild", UserID: "user"},
		&GuildScheduledEventUserRemove{GuildScheduledEventID: "event", GuildID: "guild", UserID: "user"},
	}
	want := []int{2, 1, 0, 0}
	for i, event := range events {
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface(%T) returned error: %v", event, err)
		}
		guild, err := state.Guild("guild")
		if err != nil {
			t.Fatalf("Guild returned error: %v", err)
		}
		if len(guild.GuildScheduledEvents) != 1 || guild.GuildScheduledEvents[0].UserCount != want[i] {
			t.Fatalf("events after step %d = %#v, want count %d", i, guild.GuildScheduledEvents, want[i])
		}
		if i == 0 {
			if &guild.GuildScheduledEvents[0] == &snapshot.GuildScheduledEvents[0] {
				t.Fatal("scheduled event user update reused the events backing array")
			}
			if &guild.Channels[0] != &snapshot.Channels[0] {
				t.Fatal("scheduled event user update copied the unrelated channels backing array")
			}
		}
	}

	if snapshot.GuildScheduledEvents[0].UserCount != 1 {
		t.Fatalf("old snapshot count = %d, want 1", snapshot.GuildScheduledEvents[0].UserCount)
	}
}

func TestStateOnInterfaceRejectsMalformedGuildScheduledEvents(t *testing.T) {
	tests := []struct {
		name  string
		event interface{}
	}{
		{name: "nil create", event: (*GuildScheduledEventCreate)(nil)},
		{name: "create missing event", event: &GuildScheduledEventCreate{}},
		{
			name:  "create missing ID",
			event: &GuildScheduledEventCreate{GuildScheduledEvent: &GuildScheduledEvent{GuildID: "guild"}},
		},
		{name: "nil update", event: (*GuildScheduledEventUpdate)(nil)},
		{
			name:  "update missing guild",
			event: &GuildScheduledEventUpdate{GuildScheduledEvent: &GuildScheduledEvent{ID: "event"}},
		},
		{name: "nil delete", event: (*GuildScheduledEventDelete)(nil)},
		{name: "delete missing event", event: &GuildScheduledEventDelete{}},
		{name: "nil user add", event: (*GuildScheduledEventUserAdd)(nil)},
		{name: "user add missing user", event: &GuildScheduledEventUserAdd{GuildScheduledEventID: "event", GuildID: "guild"}},
		{name: "user add missing event", event: &GuildScheduledEventUserAdd{GuildID: "guild", UserID: "user"}},
		{name: "nil user remove", event: (*GuildScheduledEventUserRemove)(nil)},
		{
			name:  "user remove missing guild",
			event: &GuildScheduledEventUserRemove{GuildScheduledEventID: "event", UserID: "user"},
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

func TestStateGuildScheduledEventCountDoesNotRaceGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		GuildScheduledEvents: []*GuildScheduledEvent{
			{ID: "event", GuildID: "guild", UserCount: 1},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	snapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for i := 0; i < 2000; i++ {
			guild, err := state.Guild("guild")
			if err != nil {
				errCh <- err
				return
			}
			if len(guild.GuildScheduledEvents) != 1 {
				errCh <- ErrStateNotFound
				return
			}
			_ = guild.GuildScheduledEvents[0].UserCount
			_ = snapshot.GuildScheduledEvents[0].UserCount
		}
		errCh <- nil
	}()

	session := &Session{StateEnabled: true}
	for i := 0; i < 2000; i++ {
		add := &GuildScheduledEventUserAdd{
			GuildScheduledEventID: "event",
			GuildID:               "guild",
			UserID:                "user",
		}
		if err := state.OnInterface(session, add); err != nil {
			t.Fatalf("OnInterface(add) returned error: %v", err)
		}
		remove := &GuildScheduledEventUserRemove{
			GuildScheduledEventID: "event",
			GuildID:               "guild",
			UserID:                "user",
		}
		if err := state.OnInterface(session, remove); err != nil {
			t.Fatalf("OnInterface(remove) returned error: %v", err)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("concurrent Guild returned error: %v", err)
	}

	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if snapshot.GuildScheduledEvents[0].UserCount != 1 || guild.GuildScheduledEvents[0].UserCount != 1 {
		t.Fatalf(
			"snapshot count = %d, current count = %d, want both 1",
			snapshot.GuildScheduledEvents[0].UserCount,
			guild.GuildScheduledEvents[0].UserCount,
		)
	}
}

func TestMemberAddReplacesCachedPointer(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
		Members: []*Member{
			{
				GuildID: "guild",
				User:    &User{ID: "user", Username: "old"},
			},
		},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
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
	if &guild.Members[0] == &oldGuild.Members[0] {
		t.Fatal("MemberAdd reused the members backing array")
	}
	if &guild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("MemberAdd copied the unrelated channels backing array")
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

func TestChannelCollectionMutationsShareUnrelatedGuildSlices(t *testing.T) {
	for _, channel := range []*Channel{
		{ID: "channel", GuildID: "guild", Type: ChannelTypeGuildText},
		{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread},
	} {
		t.Run(channel.ID, func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{
				ID:      "guild",
				Members: []*Member{{User: &User{ID: "member"}}},
			}); err != nil {
				t.Fatalf("GuildAdd returned error: %v", err)
			}
			beforeAdd, err := state.Guild("guild")
			if err != nil {
				t.Fatalf("Guild returned error: %v", err)
			}

			if err := state.ChannelAdd(channel); err != nil {
				t.Fatalf("ChannelAdd returned error: %v", err)
			}
			afterAdd, err := state.Guild("guild")
			if err != nil {
				t.Fatalf("Guild returned error after ChannelAdd: %v", err)
			}
			if &afterAdd.Members[0] != &beforeAdd.Members[0] {
				t.Fatal("ChannelAdd copied the unrelated members backing array")
			}

			if err := state.ChannelRemove(channel); err != nil {
				t.Fatalf("ChannelRemove returned error: %v", err)
			}
			afterRemove, err := state.Guild("guild")
			if err != nil {
				t.Fatalf("Guild returned error after ChannelRemove: %v", err)
			}
			if &afterRemove.Members[0] != &afterAdd.Members[0] {
				t.Fatal("ChannelRemove copied the unrelated members backing array")
			}
			if channel.IsThread() {
				if len(afterAdd.Threads) != 1 || len(afterRemove.Threads) != 0 {
					t.Fatalf("thread lengths after add/remove = %d/%d, want 1/0", len(afterAdd.Threads), len(afterRemove.Threads))
				}
			} else if len(afterAdd.Channels) != 1 || len(afterRemove.Channels) != 0 {
				t.Fatalf("channel lengths after add/remove = %d/%d, want 1/0", len(afterAdd.Channels), len(afterRemove.Channels))
			}
		})
	}
}

func TestChannelRemoveSkipsNilCachedChannels(t *testing.T) {
	dm := &Channel{ID: "dm", Type: ChannelTypeDM}
	channel := &Channel{ID: "channel", GuildID: "guild", Type: ChannelTypeGuildText}
	thread := &Channel{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread}

	state := NewState()
	if err := state.OnInterface(&Session{StateEnabled: true}, &Ready{
		Guilds: []*Guild{{
			ID:       "guild",
			Channels: []*Channel{nil, channel},
			Threads:  []*Channel{nil, thread},
		}},
		PrivateChannels: []*Channel{nil, dm},
	}); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ChannelRemove panicked: %v", r)
		}
	}()
	for _, cached := range []*Channel{dm, channel, thread} {
		if err := state.ChannelRemove(cached); err != nil {
			t.Fatalf("ChannelRemove(%q) returned error: %v", cached.ID, err)
		}
		if _, err := state.Channel(cached.ID); !errors.Is(err, ErrStateNotFound) {
			t.Fatalf("Channel(%q) returned error %v, want %v", cached.ID, err, ErrStateNotFound)
		}
	}

	if len(state.PrivateChannels) != 1 || state.PrivateChannels[0] != nil {
		t.Fatalf("PrivateChannels = %#v, want one nil entry", state.PrivateChannels)
	}
	guild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if len(guild.Channels) != 1 || guild.Channels[0] != nil {
		t.Fatalf("Channels = %#v, want one nil entry", guild.Channels)
	}
	if len(guild.Threads) != 1 || guild.Threads[0] != nil {
		t.Fatalf("Threads = %#v, want one nil entry", guild.Threads)
	}

	for _, tt := range []struct {
		name     string
		channels []*Channel
	}{
		{name: "private channels", channels: state.PrivateChannels},
		{name: "guild channels", channels: guild.Channels},
		{name: "guild threads", channels: guild.Threads},
	} {
		for i, channel := range tt.channels[len(tt.channels):cap(tt.channels)] {
			if channel != nil {
				t.Fatalf("%s backing array entry %d still retains removed channel %q", tt.name, len(tt.channels)+i, channel.ID)
			}
		}
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
	if &updatedGuild.Roles[0] == &oldGuild.Roles[0] {
		t.Fatal("RoleAdd reused the roles backing array")
	}
	if &updatedGuild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("RoleAdd copied the unrelated channels backing array")
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
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
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
	if &updatedGuild.Roles[0] == &oldGuild.Roles[0] {
		t.Fatal("RoleRemove reused the roles backing array")
	}
	if &updatedGuild.Channels[0] != &oldGuild.Channels[0] {
		t.Fatal("RoleRemove copied the unrelated channels backing array")
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
	for i, role := range updatedGuild.Roles[len(updatedGuild.Roles):cap(updatedGuild.Roles)] {
		if role != nil {
			t.Fatalf("Roles backing array entry %d still retains removed role %q", len(updatedGuild.Roles)+i, role.ID)
		}
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
						UserID: "keep",
					},
					{
						ID:     "thread",
						UserID: "remove",
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
	for i, member := range thread.Members[len(thread.Members):cap(thread.Members)] {
		if member != nil {
			t.Fatalf("Members backing array entry %d still retains removed member %q", len(thread.Members)+i, member.UserID)
		}
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
		ID:      "guild",
		Members: []*Member{{User: &User{ID: "member"}}},
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
	if &updatedGuild.Members[0] != &oldGuild.Members[0] {
		t.Fatal("ThreadListSync copied the unrelated members backing array")
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
				ID:                   "thread",
				GuildID:              "guild",
				Type:                 ChannelTypeGuildPublicThread,
				Messages:             []*Message{{ID: "message"}},
				PermissionOverwrites: []*PermissionOverwrite{{ID: "overwrite"}},
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
	if &updatedThread.Members[0] == &oldThread.Members[0] {
		t.Fatal("ThreadMembersUpdate reused the thread members backing array")
	}
	if &updatedThread.Messages[0] != &oldThread.Messages[0] {
		t.Fatal("ThreadMembersUpdate copied the unrelated messages backing array")
	}
	if &updatedThread.PermissionOverwrites[0] != &oldThread.PermissionOverwrites[0] {
		t.Fatal("ThreadMembersUpdate copied the unrelated permission overwrites backing array")
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

func TestGuildAddReusesFilteredThreads(t *testing.T) {
	state := NewState()
	state.TrackThreadMembers = false
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{{
			ID:      "thread",
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
			Member:  &ThreadMember{ID: "thread", UserID: "user"},
			Members: []*ThreadMember{{ID: "thread", UserID: "user"}},
		}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	oldGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if err := state.GuildAdd(&Guild{ID: "guild", Name: "updated"}); err != nil {
		t.Fatalf("GuildAdd update returned error: %v", err)
	}
	updatedGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after update: %v", err)
	}
	if len(updatedGuild.Threads) != 1 || &updatedGuild.Threads[0] != &oldGuild.Threads[0] {
		t.Fatal("GuildAdd copied threads that were already filtered")
	}
	if updatedGuild.Threads[0].Member != nil || updatedGuild.Threads[0].Members != nil {
		t.Fatalf("updated thread retained member data: %#v", updatedGuild.Threads[0])
	}

	toggled := NewState()
	if err := toggled.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{{
			ID:      "thread",
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
			Member:  &ThreadMember{ID: "thread", UserID: "user"},
			Members: []*ThreadMember{},
		}},
	}); err != nil {
		t.Fatalf("GuildAdd before toggle returned error: %v", err)
	}
	toggleSnapshot, err := toggled.Guild("guild")
	if err != nil {
		t.Fatalf("Guild before toggle returned error: %v", err)
	}
	toggled.TrackThreadMembers = false
	if err := toggled.GuildAdd(&Guild{ID: "guild", Name: "updated"}); err != nil {
		t.Fatalf("GuildAdd after toggle returned error: %v", err)
	}
	toggledGuild, err := toggled.Guild("guild")
	if err != nil {
		t.Fatalf("Guild after toggle returned error: %v", err)
	}
	if len(toggledGuild.Threads) != 1 || toggledGuild.Threads[0].Member != nil || toggledGuild.Threads[0].Members != nil {
		t.Fatalf("threads retained member data after tracking was disabled: %#v", toggledGuild.Threads)
	}
	cachedThread, err := toggled.Channel("thread")
	if err != nil {
		t.Fatalf("Channel after toggle returned error: %v", err)
	}
	if cachedThread != toggledGuild.Threads[0] {
		t.Fatal("channel map was not updated to the filtered thread")
	}
	if len(toggleSnapshot.Threads) != 1 || toggleSnapshot.Threads[0].Member == nil || toggleSnapshot.Threads[0].Members == nil {
		t.Fatalf("GuildAdd mutated the pre-toggle snapshot: %#v", toggleSnapshot.Threads)
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
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
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
	if &current.Channels[0] != &snapshot.Channels[0] {
		t.Fatal("GuildMembersChunk copied the unrelated channels backing array")
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
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
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
	if &current.Channels[0] != &snapshot.Channels[0] {
		t.Fatal("voice state join copied the unrelated channels backing array")
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
	if &current.Channels[0] != &joined.Channels[0] {
		t.Fatal("voice state leave copied the unrelated channels backing array")
	}
	for i, voiceState := range current.VoiceStates[len(current.VoiceStates):cap(current.VoiceStates)] {
		if voiceState != nil {
			t.Fatalf("VoiceStates backing array entry %d still retains removed user %q", len(current.VoiceStates)+i, voiceState.UserID)
		}
	}
}

func TestEmojiAddDoesNotMutateGuildSnapshot(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
		Emojis:   []*Emoji{{ID: "emoji-0", Name: "zero"}},
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
	if &current.Channels[0] != &snapshot.Channels[0] {
		t.Fatal("EmojiAdd copied the unrelated channels backing array")
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
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
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
	if &current.Channels[0] != &snapshot.Channels[0] {
		t.Fatal("EmojisAdd copied the unrelated channels backing array")
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
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
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
	afterEmojis, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after emoji update: %v", err)
	}
	if &afterEmojis.Channels[0] != &snapshot.Channels[0] {
		t.Fatal("GuildEmojisUpdate copied the unrelated channels backing array")
	}
	if &afterEmojis.Stickers[0] != &snapshot.Stickers[0] {
		t.Fatal("GuildEmojisUpdate copied the unrelated stickers backing array")
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
	if &current.Channels[0] != &afterEmojis.Channels[0] {
		t.Fatal("GuildStickersUpdate copied the unrelated channels backing array")
	}
	if &current.Emojis[0] != &afterEmojis.Emojis[0] {
		t.Fatal("GuildStickersUpdate copied the unrelated emojis backing array")
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

func TestMessageRemoveReleasesRemovedMessageReference(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	for _, id := range []string{"keep", "remove"} {
		if err := state.MessageAdd(&Message{ID: id, ChannelID: "channel"}); err != nil {
			t.Fatalf("MessageAdd returned error: %v", err)
		}
	}

	if err := state.MessageRemove(&Message{ID: "remove", ChannelID: "channel"}); err != nil {
		t.Fatalf("MessageRemove returned error: %v", err)
	}

	channel, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	for i, message := range channel.Messages[len(channel.Messages):cap(channel.Messages)] {
		if message != nil {
			t.Fatalf("Messages backing array entry %d still retains removed message %q", len(channel.Messages)+i, message.ID)
		}
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

func TestMessageCacheEvictionReleasesOldestMessage(t *testing.T) {
	tests := []struct {
		name string
		add  func(*State) error
	}{
		{
			name: "message create",
			add: func(state *State) error {
				return state.MessageAdd(&Message{ID: "replacement", ChannelID: "channel"})
			},
		},
		{
			name: "uncached message update",
			add: func(state *State) error {
				return state.messageUpdate(&MessageUpdate{Message: &Message{ID: "replacement", ChannelID: "channel"}})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := NewState()
			state.MaxMessageCount = 1
			if err := state.GuildAdd(&Guild{
				ID:       "guild",
				Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
			}); err != nil {
				t.Fatalf("GuildAdd returned error: %v", err)
			}

			evicted := &Message{ID: "evicted", ChannelID: "channel"}
			if err := state.MessageAdd(evicted); err != nil {
				t.Fatalf("MessageAdd returned error: %v", err)
			}

			if err := test.add(state); err != nil {
				t.Fatalf("adding replacement returned error: %v", err)
			}

			current, err := state.Channel("channel")
			if err != nil {
				t.Fatalf("Channel returned error: %v", err)
			}
			discardedSlot := unsafe.Add(
				unsafe.Pointer(unsafe.SliceData(current.Messages)),
				-int(unsafe.Sizeof(current.Messages[0])),
			)
			if retained := *(**Message)(discardedSlot); retained != nil {
				t.Fatalf("discarded backing array slot still retains message %q", retained.ID)
			}
		})
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

func TestMessageReactionStateLifecycle(t *testing.T) {
	state, session := newMessageReactionTestState(t)
	wave := Emoji{Name: "wave"}

	events := []struct {
		name  string
		event interface{}
		check func(*testing.T)
	}{
		{
			name: "add normal",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
			}},
			check: func(t *testing.T) {
				assertMessageReactionState(t, state, wave, 1, 1, 0, false, false, nil)
			},
		},
		{
			name: "add burst for bot",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
				Burst: true, Type: ReactionTypeBurst, BurstColors: []string{"#ff00ff"},
			}},
			check: func(t *testing.T) {
				assertMessageReactionState(t, state, wave, 2, 1, 1, false, true, []string{"#ff00ff"})
			},
		},
		{
			name: "add normal for bot",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
			}},
			check: func(t *testing.T) {
				assertMessageReactionState(t, state, wave, 3, 2, 1, true, true, []string{"#ff00ff"})
			},
		},
		{
			name: "remove burst for bot",
			event: &MessageReactionRemove{MessageReaction: &MessageReaction{
				UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
				Burst: true, Type: ReactionTypeBurst,
			}},
			check: func(t *testing.T) {
				assertMessageReactionState(t, state, wave, 2, 2, 0, true, false, nil)
			},
		},
		{
			name: "remove normal for bot",
			event: &MessageReactionRemove{MessageReaction: &MessageReaction{
				UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
			}},
			check: func(t *testing.T) {
				assertMessageReactionState(t, state, wave, 1, 1, 0, false, false, nil)
			},
		},
		{
			name: "add custom emoji",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message",
				Emoji: Emoji{ID: "emoji", Name: "old-name"},
			}},
			check: func(t *testing.T) {
				assertMessageReactionState(t, state, Emoji{ID: "emoji", Name: "new-name"}, 1, 1, 0, false, false, nil)
			},
		},
		{
			name: "remove custom emoji by id",
			event: &MessageReactionRemoveEmoji{MessageReaction: &MessageReaction{
				ChannelID: "channel", MessageID: "message", Emoji: Emoji{ID: "emoji", Name: "new-name"},
			}},
			check: func(t *testing.T) {
				assertMessageReactionMissing(t, state, Emoji{ID: "emoji"})
			},
		},
		{
			name: "remove all",
			event: &MessageReactionRemoveAll{MessageReaction: &MessageReaction{
				ChannelID: "channel", MessageID: "message",
			}},
			check: func(t *testing.T) {
				message, err := state.Message("channel", "message")
				if err != nil {
					t.Fatalf("Message returned error: %v", err)
				}
				if len(message.Reactions) != 0 {
					t.Fatalf("Reactions len = %d, want 0", len(message.Reactions))
				}
			},
		},
	}

	for _, tt := range events {
		t.Run(tt.name, func(t *testing.T) {
			if err := state.OnInterface(session, tt.event); err != nil {
				t.Fatalf("OnInterface returned error: %v", err)
			}
			tt.check(t)
		})
	}
}

func TestMessageReactionStateCanBeDisabled(t *testing.T) {
	state, session := newMessageReactionTestState(t)
	state.TrackMessageReactions = false

	if !NewState().TrackMessageReactions {
		t.Fatal("TrackMessageReactions is disabled by default")
	}

	event := &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: Emoji{Name: "wave"},
	}}
	if err := state.OnInterface(session, event); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	message, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if len(message.Reactions) != 0 {
		t.Fatalf("Reactions len = %d, want 0", len(message.Reactions))
	}
}

func TestMessageReactionStateIgnoresMalformedEventsWhenDisabled(t *testing.T) {
	events := []interface{}{
		(*MessageReactionAdd)(nil),
		(*MessageReactionRemove)(nil),
		(*MessageReactionRemoveAll)(nil),
		(*MessageReactionRemoveEmoji)(nil),
		&MessageReactionAdd{},
		&MessageReactionRemove{},
		&MessageReactionRemoveAll{},
		&MessageReactionRemoveEmoji{},
	}

	tests := []struct {
		name  string
		setup func(*State)
	}{
		{
			name: "tracking disabled",
			setup: func(state *State) {
				state.TrackMessageReactions = false
			},
		},
		{
			name: "message caching disabled",
			setup: func(state *State) {
				state.TrackMessageReactions = true
				state.MaxMessageCount = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			state.MaxMessageCount = 1
			tt.setup(state)
			for _, event := range events {
				if err := state.OnInterface(&Session{StateEnabled: true}, event); err != nil {
					t.Fatalf("OnInterface returned error: %v", err)
				}
			}
		})
	}
}

func TestStateOnInterfaceRejectsMalformedMessageReactionEvents(t *testing.T) {
	validEmoji := Emoji{Name: "wave"}
	tests := []struct {
		name  string
		event interface{}
	}{
		{name: "nil add", event: (*MessageReactionAdd)(nil)},
		{name: "nil remove", event: (*MessageReactionRemove)(nil)},
		{name: "nil remove all", event: (*MessageReactionRemoveAll)(nil)},
		{name: "nil remove emoji", event: (*MessageReactionRemoveEmoji)(nil)},
		{name: "add missing reaction", event: &MessageReactionAdd{}},
		{name: "remove missing reaction", event: &MessageReactionRemove{}},
		{name: "remove all missing reaction", event: &MessageReactionRemoveAll{}},
		{name: "remove emoji missing reaction", event: &MessageReactionRemoveEmoji{}},
		{
			name: "add missing channel",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "user", MessageID: "message", Emoji: validEmoji,
			}},
		},
		{
			name: "add missing message",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", Emoji: validEmoji,
			}},
		},
		{
			name: "add missing user",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				ChannelID: "channel", MessageID: "message", Emoji: validEmoji,
			}},
		},
		{
			name: "add missing emoji",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message",
			}},
		},
		{
			name: "add invalid reaction type",
			event: &MessageReactionAdd{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: validEmoji,
				Type: ReactionType(2),
			}},
		},
		{
			name: "remove missing user",
			event: &MessageReactionRemove{MessageReaction: &MessageReaction{
				ChannelID: "channel", MessageID: "message", Emoji: validEmoji,
			}},
		},
		{
			name: "remove missing emoji",
			event: &MessageReactionRemove{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message",
			}},
		},
		{
			name: "remove invalid reaction type",
			event: &MessageReactionRemove{MessageReaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: validEmoji,
				Type: ReactionType(2),
			}},
		},
		{
			name: "remove all missing message",
			event: &MessageReactionRemoveAll{MessageReaction: &MessageReaction{
				ChannelID: "channel",
			}},
		},
		{
			name: "remove emoji missing emoji",
			event: &MessageReactionRemoveEmoji{MessageReaction: &MessageReaction{
				ChannelID: "channel", MessageID: "message",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, session := newMessageReactionTestState(t)
			assertStateInvalidData(t, func() error {
				return state.OnInterface(session, tt.event)
			})
		})
	}
}

func TestMessageReactionStateIgnoresUnknownCacheEntries(t *testing.T) {
	state, session := newMessageReactionTestState(t)
	wave := Emoji{Name: "wave"}
	if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
	}}); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	events := []interface{}{
		&MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "user", ChannelID: "unknown", MessageID: "message", Emoji: wave,
		}},
		&MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "user", ChannelID: "channel", MessageID: "unknown", Emoji: wave,
		}},
		&MessageReactionRemove{MessageReaction: &MessageReaction{
			UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: Emoji{Name: "unknown"},
		}},
		&MessageReactionRemoveEmoji{MessageReaction: &MessageReaction{
			ChannelID: "channel", MessageID: "message", Emoji: Emoji{Name: "unknown"},
		}},
		&MessageReactionRemoveAll{MessageReaction: &MessageReaction{
			ChannelID: "channel", MessageID: "unknown",
		}},
	}

	for _, event := range events {
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}
	assertMessageReactionState(t, state, wave, 1, 1, 0, false, false, nil)
}

func TestMessageReactionStateDoesNotMutateSnapshots(t *testing.T) {
	state, session := newMessageReactionTestState(t)
	wave := Emoji{Name: "wave"}
	if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
	}}); err != nil {
		t.Fatalf("OnInterface(add normal) returned error: %v", err)
	}

	channelSnapshot, err := state.Channel("channel")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	messageSnapshot := channelSnapshot.Messages[0]
	colors := []string{"#ff00ff"}
	addBurst := &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
		Burst: true, Type: ReactionTypeBurst, BurstColors: colors,
	}}
	if err := state.OnInterface(session, addBurst); err != nil {
		t.Fatalf("OnInterface(add burst) returned error: %v", err)
	}
	colors[0] = "#000000"
	addBurst.BurstColors[0] = "#111111"

	if messageSnapshot.Reactions[0].Count != 1 || messageSnapshot.Reactions[0].CountDetails.Burst != 0 {
		t.Fatal("held message snapshot was mutated by reaction add")
	}
	assertMessageReactionState(t, state, wave, 2, 1, 1, false, false, []string{"#ff00ff"})

	custom := Emoji{
		ID: "emoji", Name: "custom", Roles: []string{"role"},
		User: &User{ID: "creator", Username: "creator"},
	}
	addCustom := &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: custom,
	}}
	if err := state.OnInterface(session, addCustom); err != nil {
		t.Fatalf("OnInterface(add custom) returned error: %v", err)
	}
	addCustom.Emoji.Name = "changed"
	addCustom.Emoji.Roles[0] = "changed"
	addCustom.Emoji.User.Username = "changed"

	customSnapshot := mustMessageReaction(t, state, Emoji{ID: "emoji"})
	if customSnapshot.Emoji.Name != "custom" || customSnapshot.Emoji.Roles[0] != "role" || customSnapshot.Emoji.User.Username != "creator" {
		t.Fatalf("cached emoji aliases event payload: %#v", customSnapshot.Emoji)
	}

	beforeRemove, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if err := state.OnInterface(session, &MessageReactionRemove{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: Emoji{ID: "emoji"},
	}}); err != nil {
		t.Fatalf("OnInterface(remove) returned error: %v", err)
	}
	if len(beforeRemove.Reactions) != 2 {
		t.Fatal("held message snapshot was mutated by reaction remove")
	}
	assertMessageReactionMissing(t, state, Emoji{ID: "emoji"})

	if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: custom,
	}}); err != nil {
		t.Fatalf("OnInterface(add custom) returned error: %v", err)
	}
	beforeRemoveEmoji, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if err := state.OnInterface(session, &MessageReactionRemoveEmoji{MessageReaction: &MessageReaction{
		ChannelID: "channel", MessageID: "message", Emoji: Emoji{ID: "emoji", Name: "renamed"},
	}}); err != nil {
		t.Fatalf("OnInterface(remove emoji) returned error: %v", err)
	}
	if len(beforeRemoveEmoji.Reactions) != 2 {
		t.Fatal("held message snapshot was mutated by reaction remove emoji")
	}

	if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: custom,
	}}); err != nil {
		t.Fatalf("OnInterface(add custom) returned error: %v", err)
	}
	beforeRemoveAll, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if err := state.OnInterface(session, &MessageReactionRemoveAll{MessageReaction: &MessageReaction{
		ChannelID: "channel", MessageID: "message",
	}}); err != nil {
		t.Fatalf("OnInterface(remove all) returned error: %v", err)
	}
	if len(beforeRemoveAll.Reactions) != 2 {
		t.Fatal("held message snapshot was mutated by reaction remove all")
	}
}

func TestMessageReactionStateSharesUnchangedReactions(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 1
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{
		ID: "message", ChannelID: "channel",
		Reactions: []*MessageReactions{
			{Count: 1, CountDetails: MessageReactionCountDetails{Normal: 1}, Emoji: &Emoji{ID: "keep"}},
			{Count: 2, CountDetails: MessageReactionCountDetails{Normal: 2}, Emoji: &Emoji{ID: "target"}},
		},
	}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}
	reaction := &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message",
		Emoji: Emoji{ID: "target"}, Type: ReactionTypeNormal,
	}

	beforeAdd, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if err := state.messageReactionUpdate(reaction, messageReactionAdd); err != nil {
		t.Fatalf("messageReactionUpdate(add) returned error: %v", err)
	}
	afterAdd, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error after add: %v", err)
	}
	if afterAdd.Reactions[0] != beforeAdd.Reactions[0] {
		t.Fatal("reaction add copied an unchanged reaction")
	}
	if afterAdd.Reactions[1] == beforeAdd.Reactions[1] || beforeAdd.Reactions[1].Count != 2 || afterAdd.Reactions[1].Count != 3 {
		t.Fatal("reaction add did not copy only the changed reaction")
	}

	if err := state.messageReactionUpdate(reaction, messageReactionRemove); err != nil {
		t.Fatalf("messageReactionUpdate(remove) returned error: %v", err)
	}
	afterRemove, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error after remove: %v", err)
	}
	if afterRemove.Reactions[0] != afterAdd.Reactions[0] {
		t.Fatal("reaction remove copied an unchanged reaction")
	}
	if afterRemove.Reactions[1] == afterAdd.Reactions[1] || afterAdd.Reactions[1].Count != 3 || afterRemove.Reactions[1].Count != 2 {
		t.Fatal("reaction remove did not copy only the changed reaction")
	}

	if err := state.messageReactionUpdate(&MessageReaction{
		ChannelID: "channel", MessageID: "message", Emoji: Emoji{ID: "target"},
	}, messageReactionRemoveEmoji); err != nil {
		t.Fatalf("messageReactionUpdate(remove emoji) returned error: %v", err)
	}
	afterRemoveEmoji, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error after remove emoji: %v", err)
	}
	if len(afterRemoveEmoji.Reactions) != 1 || afterRemoveEmoji.Reactions[0] != afterRemove.Reactions[0] {
		t.Fatal("reaction emoji removal copied the unchanged reaction")
	}
}

func TestMessageReactionRemovalReleasesRemovedReference(t *testing.T) {
	tests := []struct {
		name     string
		kind     messageReactionUpdateKind
		reaction *MessageReaction
	}{
		{
			name: "remove",
			kind: messageReactionRemove,
			reaction: &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message",
				Emoji: Emoji{ID: "remove"}, Type: ReactionTypeNormal,
			},
		},
		{
			name: "remove emoji",
			kind: messageReactionRemoveEmoji,
			reaction: &MessageReaction{
				ChannelID: "channel", MessageID: "message", Emoji: Emoji{ID: "remove"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewState()
			state.MaxMessageCount = 10
			if err := state.GuildAdd(&Guild{
				ID:       "guild",
				Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
			}); err != nil {
				t.Fatalf("GuildAdd returned error: %v", err)
			}
			if err := state.MessageAdd(&Message{
				ID: "message", ChannelID: "channel",
				Reactions: []*MessageReactions{
					{Count: 1, CountDetails: MessageReactionCountDetails{Normal: 1}, Emoji: &Emoji{ID: "keep"}},
					{Count: 1, CountDetails: MessageReactionCountDetails{Normal: 1}, Emoji: &Emoji{ID: "remove"}},
				},
			}); err != nil {
				t.Fatalf("MessageAdd returned error: %v", err)
			}

			if err := state.messageReactionUpdate(tt.reaction, tt.kind); err != nil {
				t.Fatalf("messageReactionUpdate returned error: %v", err)
			}

			message, err := state.Message("channel", "message")
			if err != nil {
				t.Fatalf("Message returned error: %v", err)
			}
			for i, reaction := range message.Reactions[len(message.Reactions):cap(message.Reactions)] {
				if reaction != nil {
					t.Fatalf("Reactions backing array entry %d still retains removed emoji %q", len(message.Reactions)+i, reaction.Emoji.ID)
				}
			}
		})
	}
}

func TestMessageReactionStateReplacesThreadOwner(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 10
	state.User = &User{ID: "bot"}
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Threads: []*Channel{{
			ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread,
		}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "thread"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	guildSnapshot, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	threadSnapshot := guildSnapshot.Threads[0]
	event := &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "thread", MessageID: "message", Emoji: Emoji{Name: "wave"},
	}}
	if err := state.OnInterface(&Session{StateEnabled: true, State: state}, event); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	currentGuild, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	currentThread, err := state.Channel("thread")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if currentGuild == guildSnapshot || currentGuild.Threads[0] == threadSnapshot {
		t.Fatal("reaction update reused the thread owner snapshot")
	}
	if currentGuild.Threads[0] != currentThread || state.channelMap["thread"] != currentThread {
		t.Fatal("guild thread slice and channel map do not reference the replacement")
	}
	if len(threadSnapshot.Messages[0].Reactions) != 0 {
		t.Fatal("held thread snapshot was mutated")
	}
	if len(currentThread.Messages[0].Reactions) != 1 {
		t.Fatalf("current thread reactions len = %d, want 1", len(currentThread.Messages[0].Reactions))
	}
}

func TestMessageReactionStateReplacesPrivateChannelOwner(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 10
	state.User = &User{ID: "bot"}
	if err := state.ChannelAdd(&Channel{ID: "dm", Type: ChannelTypeDM}); err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "dm"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}

	channelSnapshot := state.PrivateChannels[0]
	event := &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "dm", MessageID: "message", Emoji: Emoji{Name: "wave"},
	}}
	if err := state.OnInterface(&Session{StateEnabled: true, State: state}, event); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

	current, err := state.Channel("dm")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if state.PrivateChannels[0] == channelSnapshot {
		t.Fatal("reaction update reused the private channel owner snapshot")
	}
	if state.PrivateChannels[0] != current || state.channelMap["dm"] != current {
		t.Fatal("private channel slice and channel map do not reference the replacement")
	}
	if len(channelSnapshot.Messages[0].Reactions) != 0 {
		t.Fatal("held private channel snapshot was mutated")
	}
	if len(current.Messages[0].Reactions) != 1 {
		t.Fatalf("current private channel reactions len = %d, want 1", len(current.Messages[0].Reactions))
	}
}

func TestMessageReactionStateRepairsMissingCountDetails(t *testing.T) {
	tests := []struct {
		name       string
		event      func(*MessageReaction) interface{}
		wantCount  int
		wantNormal int
		wantBurst  int
	}{
		{
			name: "add normal", event: func(reaction *MessageReaction) interface{} {
				return &MessageReactionAdd{MessageReaction: reaction}
			},
			wantCount: 3, wantNormal: 3,
		},
		{
			name: "add burst", event: func(reaction *MessageReaction) interface{} {
				reaction.Burst = true
				reaction.Type = ReactionTypeBurst
				return &MessageReactionAdd{MessageReaction: reaction}
			},
			wantCount: 3, wantNormal: 2, wantBurst: 1,
		},
		{
			name: "remove normal", event: func(reaction *MessageReaction) interface{} {
				return &MessageReactionRemove{MessageReaction: reaction}
			},
			wantCount: 1, wantNormal: 1,
		},
		{
			name: "remove burst", event: func(reaction *MessageReaction) interface{} {
				reaction.Burst = true
				reaction.Type = ReactionTypeBurst
				return &MessageReactionRemove{MessageReaction: reaction}
			},
			wantCount: 1, wantNormal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
				ID: "message", ChannelID: "channel",
				Reactions: []*MessageReactions{{
					Count: 2,
					Emoji: &Emoji{Name: "wave"},
				}},
			}); err != nil {
				t.Fatalf("MessageAdd returned error: %v", err)
			}
			session := &Session{StateEnabled: true, State: state}

			reaction := &MessageReaction{
				UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: Emoji{Name: "wave"},
			}
			if err := state.OnInterface(session, tt.event(reaction)); err != nil {
				t.Fatalf("OnInterface returned error: %v", err)
			}
			assertMessageReactionState(
				t,
				state,
				Emoji{Name: "wave"},
				tt.wantCount,
				tt.wantNormal,
				tt.wantBurst,
				false,
				false,
				nil,
			)
		})
	}
}

func TestMessageReactionStateDeduplicatesBotEvents(t *testing.T) {
	state, session := newMessageReactionTestState(t)
	wave := Emoji{Name: "wave"}
	for _, event := range []interface{}{
		&MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
		}},
		&MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
			Burst: true, Type: ReactionTypeBurst, BurstColors: []string{"#ff00ff"},
		}},
	} {
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface setup returned error: %v", err)
		}
	}

	for _, event := range []interface{}{
		&MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
		}},
		&MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
			Burst: true, Type: ReactionTypeBurst,
		}},
	} {
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}
	assertMessageReactionState(t, state, wave, 2, 1, 1, true, true, []string{"#ff00ff"})

	removeNormal := &MessageReactionRemove{MessageReaction: &MessageReaction{
		UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
	}}
	if err := state.OnInterface(session, removeNormal); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	if err := state.OnInterface(session, removeNormal); err != nil {
		t.Fatalf("OnInterface duplicate returned error: %v", err)
	}
	assertMessageReactionState(t, state, wave, 1, 0, 1, false, true, []string{"#ff00ff"})

	removeBurst := &MessageReactionRemove{MessageReaction: &MessageReaction{
		UserID: "bot", ChannelID: "channel", MessageID: "message", Emoji: wave,
		Burst: true, Type: ReactionTypeBurst,
	}}
	if err := state.OnInterface(session, removeBurst); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	if err := state.OnInterface(session, removeBurst); err != nil {
		t.Fatalf("OnInterface duplicate returned error: %v", err)
	}
	assertMessageReactionMissing(t, state, wave)
}

func TestMessageReactionStateMutatorsDoNotRaceSnapshots(t *testing.T) {
	state, session := newMessageReactionTestState(t)
	wave := Emoji{Name: "wave"}
	if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
	}}); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}

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
			if err != nil {
				continue
			}
			for _, reaction := range message.Reactions {
				if reaction == nil {
					continue
				}
				_ = reaction.Count
				_ = reaction.CountDetails.Normal
				_ = reaction.CountDetails.Burst
				_ = reaction.Me
				_ = reaction.MeBurst
				for _, color := range reaction.BurstColors {
					_ = color
				}
				if reaction.Emoji != nil {
					_ = reaction.Emoji.ID
					_ = reaction.Emoji.Name
				}
			}
		}
	}()

	for i := 0; i < 500; i++ {
		burst := &MessageReaction{
			UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
			Burst: true, Type: ReactionTypeBurst, BurstColors: []string{"#ff00ff"},
		}
		if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: burst}); err != nil {
			t.Fatalf("OnInterface(add) returned error: %v", err)
		}
		if err := state.OnInterface(session, &MessageReactionRemove{MessageReaction: burst}); err != nil {
			t.Fatalf("OnInterface(remove) returned error: %v", err)
		}
		custom := &MessageReaction{
			UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: Emoji{ID: "emoji"},
		}
		if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: custom}); err != nil {
			t.Fatalf("OnInterface(add custom) returned error: %v", err)
		}
		if err := state.OnInterface(session, &MessageReactionRemoveEmoji{MessageReaction: custom}); err != nil {
			t.Fatalf("OnInterface(remove emoji) returned error: %v", err)
		}
		if err := state.OnInterface(session, &MessageReactionRemoveAll{MessageReaction: &MessageReaction{
			ChannelID: "channel", MessageID: "message",
		}}); err != nil {
			t.Fatalf("OnInterface(remove all) returned error: %v", err)
		}
		if err := state.OnInterface(session, &MessageReactionAdd{MessageReaction: &MessageReaction{
			UserID: "user", ChannelID: "channel", MessageID: "message", Emoji: wave,
		}}); err != nil {
			t.Fatalf("OnInterface(reset) returned error: %v", err)
		}
	}

	close(done)
	wg.Wait()
}

func newMessageReactionTestState(t *testing.T) (*State, *Session) {
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
	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}
	return state, &Session{StateEnabled: true, State: state}
}

func mustMessageReaction(t *testing.T, state *State, emoji Emoji) *MessageReactions {
	t.Helper()
	message, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	for _, reaction := range message.Reactions {
		if reaction != nil && reactionEmojiEqual(reaction.Emoji, &emoji) {
			return reaction
		}
	}
	t.Fatalf("reaction for emoji %#v not found", emoji)
	return nil
}

func assertMessageReactionState(
	t *testing.T,
	state *State,
	emoji Emoji,
	count, normal, burst int,
	me, meBurst bool,
	colors []string,
) {
	t.Helper()
	reaction := mustMessageReaction(t, state, emoji)
	if reaction.Count != count || reaction.CountDetails.Normal != normal || reaction.CountDetails.Burst != burst {
		t.Fatalf(
			"reaction counts = (%d, normal %d, burst %d), want (%d, normal %d, burst %d)",
			reaction.Count,
			reaction.CountDetails.Normal,
			reaction.CountDetails.Burst,
			count,
			normal,
			burst,
		)
	}
	if reaction.Me != me || reaction.MeBurst != meBurst {
		t.Fatalf("reaction me flags = (%t, %t), want (%t, %t)", reaction.Me, reaction.MeBurst, me, meBurst)
	}
	if len(reaction.BurstColors) != len(colors) {
		t.Fatalf("BurstColors = %#v, want %#v", reaction.BurstColors, colors)
	}
	for i := range colors {
		if reaction.BurstColors[i] != colors[i] {
			t.Fatalf("BurstColors = %#v, want %#v", reaction.BurstColors, colors)
		}
	}
}

func assertMessageReactionMissing(t *testing.T, state *State, emoji Emoji) {
	t.Helper()
	message, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	for _, reaction := range message.Reactions {
		if reaction != nil && reactionEmojiEqual(reaction.Emoji, &emoji) {
			t.Fatalf("reaction for emoji %#v is still cached", emoji)
		}
	}
}

func BenchmarkStateThreadMembersUpdateSnapshot(b *testing.B) {
	threadMembers := make([]*ThreadMember, 100)
	for i := range threadMembers {
		threadMembers[i] = &ThreadMember{ID: "thread", UserID: "member-" + strconv.Itoa(i)}
	}
	thread := &Channel{
		ID:                   "thread",
		GuildID:              "guild",
		Type:                 ChannelTypeGuildPublicThread,
		Recipients:           make([]*User, 10),
		Messages:             make([]*Message, 100),
		PermissionOverwrites: make([]*PermissionOverwrite, 10),
		Members:              threadMembers,
		AvailableTags:        make([]ForumTag, 10),
		AppliedTags:          make([]string, 10),
		MemberCount:          len(threadMembers),
	}

	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild", Threads: []*Channel{thread}}); err != nil {
		b.Fatal(err)
	}
	updates := make([]*ThreadMembersUpdate, len(threadMembers)+1)
	for i := range updates {
		removed := "member-" + strconv.Itoa(i)
		added := "member-" + strconv.Itoa((i+len(threadMembers))%len(updates))
		updates[i] = &ThreadMembersUpdate{
			ID:             "thread",
			GuildID:        "guild",
			MemberCount:    len(threadMembers),
			RemovedMembers: []string{removed},
			AddedMembers: []AddedThreadMember{{
				ThreadMember: &ThreadMember{ID: "thread", UserID: added},
			}},
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.ThreadMembersUpdate(updates[i%len(updates)]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildMemberCountSnapshot(b *testing.B) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		delta := 1
		if i%2 != 0 {
			delta = -1
		}
		state.Lock()
		err := state.updateGuildMemberCount("guild", delta)
		state.Unlock()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildUpdatePreservedSlices(b *testing.B) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 20),
		SoundboardSounds:     make([]*SoundboardSound, 20),
	}); err != nil {
		b.Fatal(err)
	}
	update := &Guild{ID: "guild", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.GuildAdd(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildUpdateFilteredThreads(b *testing.B) {
	threads := make([]*Channel, 20)
	for i := range threads {
		threads[i] = &Channel{
			ID:      "thread-" + strconv.Itoa(i),
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
			Member:  &ThreadMember{ID: "thread-" + strconv.Itoa(i), UserID: "user"},
		}
	}
	state := NewState()
	state.TrackThreadMembers = false
	if err := state.GuildAdd(&Guild{ID: "guild", Threads: threads}); err != nil {
		b.Fatal(err)
	}
	update := &Guild{ID: "guild", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.GuildAdd(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildUpdatePreservedChannelMap(b *testing.B) {
	channels := make([]*Channel, 100)
	for i := range channels {
		channels[i] = &Channel{ID: "channel-" + strconv.Itoa(i), GuildID: "guild"}
	}
	threads := make([]*Channel, 50)
	for i := range threads {
		threads[i] = &Channel{
			ID:      "thread-" + strconv.Itoa(i),
			GuildID: "guild",
			Type:    ChannelTypeGuildPublicThread,
		}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{ID: "guild", Channels: channels, Threads: threads}); err != nil {
		b.Fatal(err)
	}
	update := &Guild{ID: "guild", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.GuildAdd(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildUnavailableSnapshot(b *testing.B) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	event := &GuildDelete{Guild: &Guild{ID: "guild", Unavailable: true}}
	session := &Session{StateEnabled: true}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.OnInterface(session, event); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildScheduledEventUserCountSnapshot(b *testing.B) {
	events := make([]*GuildScheduledEvent, 10)
	for i := range events {
		events[i] = &GuildScheduledEvent{ID: "event-" + strconv.Itoa(i), GuildID: "guild", UserCount: 100}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: events,
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		delta := 1
		if i%2 != 0 {
			delta = -1
		}
		if err := state.updateGuildScheduledEventUserCount("guild", "event-0", delta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatePresenceUpdateGuildSnapshot(b *testing.B) {
	presences := make([]*Presence, 1000)
	for i := range presences {
		presences[i] = &Presence{User: &User{ID: "user-" + strconv.Itoa(i)}, Status: StatusOnline}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            presences,
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &Presence{User: &User{ID: "user-0"}, Status: StatusIdle}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.PresenceAdd("guild", update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateMemberUpdateGuildSnapshot(b *testing.B) {
	members := make([]*Member, 1000)
	for i := range members {
		members[i] = &Member{GuildID: "guild", User: &User{ID: "user-" + strconv.Itoa(i)}}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              members,
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &Member{GuildID: "guild", User: &User{ID: "user-0", Username: "updated"}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.MemberAdd(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateRoleUpdateGuildSnapshot(b *testing.B) {
	roles := make([]*Role, 100)
	for i := range roles {
		roles[i] = &Role{ID: "role-" + strconv.Itoa(i)}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                roles,
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &Role{ID: "role-0", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.RoleAdd("guild", update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateVoiceUpdateGuildSnapshot(b *testing.B) {
	voiceStates := make([]*VoiceState, 500)
	for i := range voiceStates {
		voiceStates[i] = &VoiceState{GuildID: "guild", ChannelID: "channel", UserID: "user-" + strconv.Itoa(i)}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          voiceStates,
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &VoiceStateUpdate{VoiceState: &VoiceState{
		GuildID:   "guild",
		ChannelID: "channel-2",
		UserID:    "user-0",
	}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.voiceStateUpdate(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateEmojiUpdateGuildSnapshot(b *testing.B) {
	emojis := make([]*Emoji, 100)
	for i := range emojis {
		emojis[i] = &Emoji{ID: "emoji-" + strconv.Itoa(i)}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               emojis,
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &Emoji{ID: "emoji-0", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.EmojiAdd("guild", update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildEmojisReplaceSnapshot(b *testing.B) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	event := &GuildEmojisUpdate{GuildID: "guild", Emojis: make([]*Emoji, 100)}
	session := &Session{StateEnabled: true}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.OnInterface(session, event); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateSoundboardUpdateGuildSnapshot(b *testing.B) {
	sounds := make([]*SoundboardSound, 10)
	for i := range sounds {
		sounds[i] = &SoundboardSound{SoundID: "sound-" + strconv.Itoa(i), GuildID: "guild"}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     sounds,
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &SoundboardSound{SoundID: "sound-0", GuildID: "guild", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := state.guildSoundboardSoundAdd(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateStageInstanceUpdateGuildSnapshot(b *testing.B) {
	instances := make([]*StageInstance, 10)
	for i := range instances {
		instances[i] = &StageInstance{ID: "stage-" + strconv.Itoa(i), GuildID: "guild"}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       instances,
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &StageInstance{ID: "stage-0", GuildID: "guild", Topic: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := state.guildStageInstanceAdd(update); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateScheduledEventUpdateGuildSnapshot(b *testing.B) {
	events := make([]*GuildScheduledEvent, 10)
	for i := range events {
		events[i] = &GuildScheduledEvent{ID: "event-" + strconv.Itoa(i), GuildID: "guild"}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: events,
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	update := &GuildScheduledEvent{ID: "event-0", GuildID: "guild", Name: "updated"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := state.guildScheduledEventAdd(update, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateChannelAddRemoveGuildSnapshot(b *testing.B) {
	channels := make([]*Channel, 100)
	for i := range channels {
		channels[i] = &Channel{ID: "channel-" + strconv.Itoa(i), GuildID: "guild", Type: ChannelTypeGuildText}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             channels,
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	channel := channels[0]

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.ChannelRemove(channel); err != nil {
			b.Fatal(err)
		}
		if err := state.ChannelAdd(channel); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateThreadListSyncGuildSnapshot(b *testing.B) {
	threads := make([]*Channel, 10)
	for i := range threads {
		threads[i] = &Channel{
			ID:             "thread-" + strconv.Itoa(i),
			GuildID:        "guild",
			ParentID:       "parent",
			Type:           ChannelTypeGuildPublicThread,
			ThreadMetadata: &ThreadMetadata{},
		}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             make([]*Channel, 100),
		Threads:              threads,
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	tls := &ThreadListSync{GuildID: "guild", Threads: threads}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.ThreadListSync(tls); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateGuildMembersChunkGuildSnapshot(b *testing.B) {
	members := make([]*Member, 1000)
	presences := make([]*Presence, 1000)
	for i := range members {
		id := "user-" + strconv.Itoa(i)
		members[i] = &Member{GuildID: "guild", User: &User{ID: id}}
		presences[i] = &Presence{User: &User{ID: id}, Status: StatusOffline}
	}
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 100),
		Emojis:               make([]*Emoji, 100),
		Stickers:             make([]*Sticker, 100),
		Members:              members,
		Presences:            presences,
		Channels:             make([]*Channel, 100),
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 500),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
		MemberCount:          1000,
	}); err != nil {
		b.Fatal(err)
	}
	chunk := &GuildMembersChunk{
		GuildID:   "guild",
		Members:   []*Member{{User: &User{ID: "user-0", Username: "updated"}}},
		Presences: []*Presence{{User: &User{ID: "user-0"}, Status: StatusOnline}},
	}
	session := &Session{StateEnabled: true}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.OnInterface(session, chunk); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateMessageReactionUpdateSnapshot(b *testing.B) {
	reactions := make([]*MessageReactions, 20)
	for i := range reactions {
		reactions[i] = &MessageReactions{
			Count:        1,
			CountDetails: MessageReactionCountDetails{Normal: 1},
			Emoji: &Emoji{
				ID:    "emoji-" + strconv.Itoa(i),
				Roles: []string{"role-a", "role-b"},
				User:  &User{ID: "creator"},
			},
			BurstColors: []string{"#ff0000", "#00ff00"},
		}
	}
	state := NewState()
	state.MaxMessageCount = 1
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		b.Fatal(err)
	}
	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel", Reactions: reactions}); err != nil {
		b.Fatal(err)
	}
	update := &MessageReaction{
		UserID: "user", ChannelID: "channel", MessageID: "message",
		Emoji: Emoji{ID: "emoji-0"}, Type: ReactionTypeNormal,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.messageReactionUpdate(update, messageReactionAdd); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateThreadMemberUpdateSnapshot(b *testing.B) {
	threadMessages := make([]*Message, 100)
	threadMembers := make([]*ThreadMember, 100)
	for i := range threadMessages {
		threadMessages[i] = &Message{ID: "message-" + strconv.Itoa(i), ChannelID: "thread"}
		threadMembers[i] = &ThreadMember{ID: "thread", UserID: "member-" + strconv.Itoa(i)}
	}
	thread := &Channel{
		ID:                   "thread",
		GuildID:              "guild",
		Type:                 ChannelTypeGuildPublicThread,
		Recipients:           make([]*User, 10),
		Messages:             threadMessages,
		PermissionOverwrites: make([]*PermissionOverwrite, 10),
		Members:              threadMembers,
		AvailableTags:        make([]ForumTag, 10),
		AppliedTags:          make([]string, 10),
	}

	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:        "guild",
		Members:   make([]*Member, 1000),
		Presences: make([]*Presence, 1000),
		Threads:   []*Channel{thread},
	}); err != nil {
		b.Fatal(err)
	}
	updates := make([]*ThreadMemberUpdate, 101)
	for i := range updates {
		updates[i] = &ThreadMemberUpdate{ThreadMember: &ThreadMember{
			ID:     "thread",
			UserID: "current-" + strconv.Itoa(i),
		}}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.ThreadMemberUpdate(updates[i%len(updates)]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStateMessageAddGuildSnapshot(b *testing.B) {
	const maxMessageCount = 100

	messages := make([]*Message, maxMessageCount)
	for i := range messages {
		messages[i] = &Message{
			ID:        "seed-" + strconv.Itoa(i),
			ChannelID: "target",
		}
	}
	channels := make([]*Channel, 100)
	channels[0] = &Channel{
		ID:                   "target",
		GuildID:              "guild",
		Messages:             messages,
		PermissionOverwrites: make([]*PermissionOverwrite, 10),
	}
	for i := 1; i < len(channels); i++ {
		channels[i] = &Channel{ID: "channel-" + strconv.Itoa(i), GuildID: "guild"}
	}

	state := NewState()
	state.MaxMessageCount = maxMessageCount
	if err := state.GuildAdd(&Guild{
		ID:                   "guild",
		Roles:                make([]*Role, 20),
		Emojis:               make([]*Emoji, 20),
		Stickers:             make([]*Sticker, 20),
		Members:              make([]*Member, 1000),
		Presences:            make([]*Presence, 1000),
		Channels:             channels,
		Threads:              make([]*Channel, 50),
		VoiceStates:          make([]*VoiceState, 100),
		Features:             make([]GuildFeature, 10),
		StageInstances:       make([]*StageInstance, 10),
		GuildScheduledEvents: make([]*GuildScheduledEvent, 10),
		SoundboardSounds:     make([]*SoundboardSound, 10),
	}); err != nil {
		b.Fatal(err)
	}

	replacements := make([]*Message, maxMessageCount+1)
	for i := range replacements {
		replacements[i] = &Message{
			ID:        "replacement-" + strconv.Itoa(i),
			ChannelID: "target",
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.MessageAdd(replacements[i%len(replacements)]); err != nil {
			b.Fatal(err)
		}
	}
}

func TestReplaceChannelCopiesOnlyModifiedGuildSlice(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 2
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Members: []*Member{{
			GuildID: "guild",
			User:    &User{ID: "member"},
		}},
		Channels: []*Channel{{
			ID:                   "channel",
			GuildID:              "guild",
			Messages:             []*Message{{ID: "seed", ChannelID: "channel"}},
			PermissionOverwrites: []*PermissionOverwrite{{ID: "role"}},
		}},
		Threads: []*Channel{{
			ID:                   "thread",
			GuildID:              "guild",
			Type:                 ChannelTypeGuildPublicThread,
			Messages:             []*Message{{ID: "thread-message", ChannelID: "thread"}},
			PermissionOverwrites: []*PermissionOverwrite{{ID: "thread-role"}},
			Members:              []*ThreadMember{{ID: "thread", UserID: "other"}},
			AvailableTags:        []ForumTag{{ID: "tag"}},
			AppliedTags:          []string{"tag"},
		}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}

	beforeMessage, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error: %v", err)
	}
	if err := state.MessageAdd(&Message{ID: "message", ChannelID: "channel"}); err != nil {
		t.Fatalf("MessageAdd returned error: %v", err)
	}
	afterMessage, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after MessageAdd: %v", err)
	}
	if &beforeMessage.Channels[0] == &afterMessage.Channels[0] {
		t.Fatal("MessageAdd reused the guild channel backing array")
	}
	if &beforeMessage.Members[0] != &afterMessage.Members[0] {
		t.Fatal("MessageAdd copied the unrelated guild member backing array")
	}
	if &beforeMessage.Channels[0].Messages[0] == &afterMessage.Channels[0].Messages[0] {
		t.Fatal("MessageAdd reused the channel message backing array")
	}
	if &beforeMessage.Channels[0].PermissionOverwrites[0] != &afterMessage.Channels[0].PermissionOverwrites[0] {
		t.Fatal("MessageAdd copied the unrelated permission overwrite backing array")
	}
	if len(beforeMessage.Channels[0].Messages) != 1 || len(afterMessage.Channels[0].Messages) != 2 {
		t.Fatalf("message snapshots = (%d, %d), want (1, 2)", len(beforeMessage.Channels[0].Messages), len(afterMessage.Channels[0].Messages))
	}

	if err := state.ThreadMemberUpdate(&ThreadMemberUpdate{
		GuildID: "guild",
		ThreadMember: &ThreadMember{
			ID:     "thread",
			UserID: "member",
		},
	}); err != nil {
		t.Fatalf("ThreadMemberUpdate returned error: %v", err)
	}
	afterThread, err := state.Guild("guild")
	if err != nil {
		t.Fatalf("Guild returned error after ThreadMemberUpdate: %v", err)
	}
	if &afterMessage.Threads[0] == &afterThread.Threads[0] {
		t.Fatal("ThreadMemberUpdate reused the guild thread backing array")
	}
	if &afterMessage.Members[0] != &afterThread.Members[0] {
		t.Fatal("ThreadMemberUpdate copied the unrelated guild member backing array")
	}
	beforeThread := afterMessage.Threads[0]
	updatedThread := afterThread.Threads[0]
	if &beforeThread.Messages[0] != &updatedThread.Messages[0] ||
		&beforeThread.PermissionOverwrites[0] != &updatedThread.PermissionOverwrites[0] ||
		&beforeThread.Members[0] != &updatedThread.Members[0] ||
		&beforeThread.AvailableTags[0] != &updatedThread.AvailableTags[0] ||
		&beforeThread.AppliedTags[0] != &updatedThread.AppliedTags[0] {
		t.Fatal("ThreadMemberUpdate copied an unrelated thread slice")
	}
	if afterMessage.Threads[0].Member != nil || afterThread.Threads[0].Member == nil {
		t.Fatalf("thread member snapshots = (%#v, %#v), want (nil, non-nil)", afterMessage.Threads[0].Member, afterThread.Threads[0].Member)
	}
}

func TestChannelUpdateObfuscation(t *testing.T) {
	for _, test := range []struct {
		name        string
		channelType ChannelType
	}{
		{name: "text", channelType: ChannelTypeGuildText},
		{name: "voice", channelType: ChannelTypeGuildVoice},
		{name: "forum", channelType: ChannelTypeGuildForum},
		{name: "thread", channelType: ChannelTypeGuildPublicThread},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
				t.Fatal(err)
			}
			visible := &Channel{
				ID: "channel", GuildID: "guild", Type: test.channelType,
				Name: "visible", Topic: "topic", ParentID: "parent", Position: 3,
				LastMessageID: "message", ApplicationID: "application",
				PermissionOverwrites: []*PermissionOverwrite{{ID: "role", Type: PermissionOverwriteTypeRole, Allow: PermissionViewChannel}},
				Messages:             []*Message{{ID: "message", Content: "cached"}},
			}
			if visible.IsThread() {
				visible.ThreadMetadata = &ThreadMetadata{AutoArchiveDuration: 1440}
			}
			if err := state.ChannelAdd(visible); err != nil {
				t.Fatal(err)
			}

			var hidden ChannelUpdate
			payload := `{"id":"channel","guild_id":"guild","type":` + strconv.Itoa(int(test.channelType)) + `,"name":"___hidden___","topic":null,"parent_id":"parent","position":3,"last_message_id":null,"application_id":null,"last_pin_timestamp":null,"rtc_region":null,"thread_metadata":null,"default_sort_order":null,"flags":131072,"permission_overwrites":[{"id":"guild","type":0,"allow":"0","deny":"1024"}]}`
			if err := json.Unmarshal([]byte(payload), &hidden); err != nil {
				t.Fatalf("decode obfuscated channel: %v", err)
			}
			if err := state.OnInterface(&Session{StateEnabled: true}, &hidden); err != nil {
				t.Fatal(err)
			}
			cached, err := state.Channel("channel")
			if err != nil {
				t.Fatal(err)
			}
			if cached.Flags&ChannelFlagObfuscated == 0 || cached.Name != "___hidden___" {
				t.Fatalf("obfuscated channel not cached: %+v", cached)
			}
			if cached.Topic != "" || cached.LastMessageID != "" || cached.ApplicationID != "" || cached.ThreadMetadata != nil {
				t.Fatalf("obfuscated channel retained hidden metadata: %+v", cached)
			}
			if cached.ParentID != "parent" || cached.Position != 3 || cached.Type != test.channelType {
				t.Fatalf("obfuscation lost visible fields: %+v", cached)
			}
			if len(cached.PermissionOverwrites) != 1 || cached.PermissionOverwrites[0].ID != "guild" || cached.PermissionOverwrites[0].Deny != PermissionViewChannel || cached.PermissionOverwrites[0].Allow != 0 {
				t.Fatalf("obfuscation did not replace permission overwrites: %+v", cached.PermissionOverwrites)
			}
			if len(cached.Messages) != 1 || cached.Messages[0].ID != "message" {
				t.Fatal("channel update lost cached messages")
			}
			if visible.Name != "visible" || visible.Topic != "topic" || hidden.BeforeUpdate == nil || hidden.BeforeUpdate.Name != "visible" {
				t.Fatal("obfuscation mutated the original channel snapshot")
			}

			var restored ChannelUpdate
			data, err := json.Marshal(visible)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if err = state.OnInterface(&Session{StateEnabled: true}, &restored); err != nil {
				t.Fatal(err)
			}
			current, err := state.Channel("channel")
			if err != nil {
				t.Fatal(err)
			}
			if current.Flags != 0 || current.Name != "visible" || current.Topic != "topic" || current.PermissionOverwrites[0].ID != "role" {
				t.Fatalf("full channel data not restored: %+v", current)
			}
			if visible.IsThread() && (current.ThreadMetadata == nil || current.ThreadMetadata.AutoArchiveDuration != 1440) {
				t.Fatal("thread metadata not restored")
			}
			if cached.Name != "___hidden___" || restored.BeforeUpdate == nil || restored.BeforeUpdate.Flags&ChannelFlagObfuscated == 0 {
				t.Fatal("restoring visibility mutated the obfuscated channel snapshot")
			}
			guild, err := state.Guild("guild")
			if err != nil {
				t.Fatal(err)
			}
			channels := guild.Channels
			if current.IsThread() {
				channels = guild.Threads
			}
			if len(channels) != 1 || channels[0] != current {
				t.Fatal("guild channel list does not contain the current channel")
			}
		})
	}
}

func TestChannelDeleteEvictsChildThreads(t *testing.T) {
	for _, trackChannels := range []bool{true, false} {
		for _, kind := range []ChannelType{ChannelTypeGuildText, ChannelTypeGuildNews, ChannelTypeGuildForum, ChannelTypeGuildMedia} {
			t.Run(fmt.Sprintf("type=%d/trackChannels=%t", kind, trackChannels), func(t *testing.T) {
				session, err := New("")
				if err != nil {
					t.Fatal(err)
				}
				state := session.State
				state.TrackChannels = trackChannels
				parent := &Channel{ID: "parent", GuildID: "guild", Type: kind}
				child := &Channel{ID: "child", GuildID: "guild", ParentID: parent.ID, Type: ChannelTypeGuildPublicThread,
					Messages: []*Message{{ID: "message", Content: "retained message"}}}
				sibling := &Channel{ID: "sibling", GuildID: "guild", ParentID: "other", Type: ChannelTypeGuildPublicThread}
				if err := state.GuildAdd(&Guild{ID: "guild", Channels: []*Channel{parent}, Threads: []*Channel{child, sibling}}); err != nil {
					t.Fatal(err)
				}
				before, err := state.Guild("guild")
				if err != nil {
					t.Fatal(err)
				}
				payload := fmt.Sprintf(`{"op":0,"t":"CHANNEL_DELETE","s":1,"d":{"id":"parent","guild_id":"guild","type":%d}}`, kind)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{parent.ID, child.ID} {
					if _, err := state.Channel(id); !errors.Is(err, ErrStateNotFound) {
						t.Errorf("Channel(%q) error = %v, want ErrStateNotFound", id, err)
					}
				}
				after, err := state.Guild("guild")
				if err != nil {
					t.Fatal(err)
				}
				if len(after.Threads) != 1 || after.Threads[0] != sibling {
					t.Errorf("remaining threads = %v, want only sibling", after.Threads)
				}
				if len(before.Threads) != 2 || before.Threads[0] != child {
					t.Error("old guild snapshot was mutated")
				}
				if _, err := state.Message(child.ID, "message"); !errors.Is(err, ErrStateNotFound) {
					t.Errorf("deleted child's messages remain cached: %v", err)
				}
			})
		}
	}
}

func TestGatewayCreatesUpdateChannelLastMessage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		channelType   ChannelType
		threadEvent   bool
		newThread     bool
		trackChannels bool
		trackThreads  bool
		stateEnabled  bool
		wantUpdate    bool
	}{
		{"text message", ChannelTypeGuildText, false, false, true, true, true, true},
		{"thread message", ChannelTypeGuildPublicThread, false, false, false, true, true, true},
		{"DM message", ChannelTypeDM, false, false, true, false, true, true},
		{"forum post without thread cache", ChannelTypeGuildForum, true, true, true, false, true, true},
		{"media post", ChannelTypeGuildMedia, true, true, true, true, true, true},
		{"existing forum thread", ChannelTypeGuildForum, true, false, true, true, true, false},
		{"ordinary parent thread", ChannelTypeGuildText, true, true, true, true, true, false},
		{"untracked channel", ChannelTypeGuildText, false, false, false, true, true, false},
		{"untracked thread", ChannelTypeGuildPublicThread, false, false, true, false, true, false},
		{"disabled state", ChannelTypeGuildText, false, false, true, true, false, false},
	} {
		for _, maxMessages := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/messages=%d", tc.name, maxMessages), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = tc.stateEnabled
				session.State.MaxMessageCount = maxMessages
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				channel := &Channel{ID: "300", GuildID: "100", Type: tc.channelType, LastMessageID: "400"}
				if tc.channelType == ChannelTypeDM {
					channel.GuildID = ""
				}
				if err := session.State.ChannelAdd(channel); err != nil {
					t.Fatal(err)
				}
				session.State.TrackChannels = tc.trackChannels
				session.State.TrackThreads = tc.trackThreads
				before, err := session.State.Channel("300")
				if err != nil {
					t.Fatal(err)
				}
				var callbackChannel *Channel
				capture := func() {
					callbackChannel, err = session.State.Channel("300")
					if err != nil {
						t.Error(err)
					}
				}
				session.AddHandler(func(_ *Session, _ *MessageCreate) { capture() })
				session.AddHandler(func(_ *Session, _ *ThreadCreate) { capture() })
				eventType := "MESSAGE_CREATE"
				payload := `{"id":"500","channel_id":"300","author":{"id":"200"},"content":"new message","timestamp":"2026-10-31T00:00:00Z","type":0}`
				if channel.GuildID != "" {
					payload = fmt.Sprintf(`{"guild_id":%q,`, channel.GuildID) + payload[1:]
				}
				if tc.threadEvent {
					eventType = "THREAD_CREATE"
					payload = fmt.Sprintf(`{"id":"500","guild_id":"100","parent_id":"300","type":11,"name":"post","newly_created":%t,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-31T00:00:00Z","locked":false}}`, tc.newThread)
				}
				if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":%q,"d":%s}`, eventType, payload))); err != nil {
					t.Fatal(err)
				}
				after, err := session.State.Channel("300")
				if err != nil {
					t.Fatal(err)
				}
				want := "400"
				if tc.wantUpdate {
					want = "500"
				}
				if after.LastMessageID != want || callbackChannel == nil || callbackChannel.LastMessageID != want {
					t.Errorf("last message = %q, want %q before callback", after.LastMessageID, want)
				}
				if before.LastMessageID != "400" {
					t.Error("create event mutated a retained channel snapshot")
				}
				var indexed *Channel
				if tc.channelType == ChannelTypeDM {
					indexed = session.State.PrivateChannels[0]
				} else {
					guild, err := session.State.Guild("100")
					if err != nil {
						t.Fatal(err)
					}
					if channel.IsThread() {
						indexed = guild.Threads[0]
					} else {
						indexed = guild.Channels[0]
					}
				}
				if indexed != after || callbackChannel != after {
					t.Error("channel cache indexes and callback disagree")
				}
				if maxMessages == 0 && len(after.Messages) != 0 {
					t.Error("channel metadata update retained message bodies with caching disabled")
				}
			})
		}
	}
}

func TestChannelLastMessagePreservesNonCreationAndErrorBehavior(t *testing.T) {
	state := NewState()
	state.MaxMessageCount = 2
	if err := state.ChannelAdd(&Channel{ID: "300", Type: ChannelTypeDM, LastMessageID: "500"}); err != nil {
		t.Fatal(err)
	}
	// Explicit cache insertion can contain historical messages.
	if err := state.MessageAdd(&Message{ID: "400", ChannelID: "300"}); err != nil {
		t.Fatal(err)
	}
	session := &Session{StateEnabled: true}
	for _, event := range []interface{}{
		&MessageUpdate{Message: &Message{ID: "400", ChannelID: "300", Content: "edited"}},
		&MessageDelete{Message: &Message{ID: "500", ChannelID: "300"}},
		&MessageCreate{Message: &Message{ChannelID: "300"}},
	} {
		_ = state.OnInterface(session, event)
		channel, err := state.Channel("300")
		if err != nil || channel.LastMessageID != "500" {
			t.Fatalf("%T changed last message: %v", event, err)
		}
	}
	state.MaxMessageCount = -1
	before, _ := state.Channel("300")
	err := state.OnInterface(session, &MessageCreate{Message: &Message{ID: "600", ChannelID: "300"}})
	if !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("negative message limit error = %v", err)
	}
	after, _ := state.Channel("300")
	if after != before {
		t.Fatal("rejected message create changed the channel snapshot")
	}
}

func TestChannelPinsUpdateState(t *testing.T) {
	oldTimestamp := time.Date(2026, time.July, 12, 8, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID: "guild",
		Channels: []*Channel{{
			ID:               "guild-channel",
			GuildID:          "guild",
			Type:             ChannelTypeGuildText,
			LastPinTimestamp: &oldTimestamp,
		}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if err := state.ChannelAdd(&Channel{
		ID:               "dm-channel",
		Type:             ChannelTypeDM,
		LastPinTimestamp: &oldTimestamp,
	}); err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}

	session := &Session{StateEnabled: true}
	wantTimestamp := time.Date(2026, time.July, 13, 9, 10, 11, 123000000, time.UTC)
	t.Run("guild channel timestamp", func(t *testing.T) {
		beforeGuild, err := state.Guild("guild")
		if err != nil {
			t.Fatalf("Guild returned error: %v", err)
		}
		beforeChannel, err := state.Channel("guild-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}

		var event ChannelPinsUpdate
		if err := json.Unmarshal([]byte(`{"channel_id":"guild-channel","guild_id":"guild","last_pin_timestamp":"2026-07-13T09:10:11.123Z"}`), &event); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		if err := state.OnInterface(session, &event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}

		channel, err := state.Channel("guild-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}
		guild, err := state.Guild("guild")
		if err != nil {
			t.Fatalf("Guild returned error: %v", err)
		}
		if channel.LastPinTimestamp == nil || !channel.LastPinTimestamp.Equal(wantTimestamp) {
			t.Fatalf("LastPinTimestamp = %v, want %v", channel.LastPinTimestamp, wantTimestamp)
		}
		if event.LastPinTimestamp != "2026-07-13T09:10:11.123Z" {
			t.Fatalf("event LastPinTimestamp = %q, want original value", event.LastPinTimestamp)
		}
		if guild == beforeGuild || channel == beforeChannel || guild.Channels[0] != channel {
			t.Fatal("channel pins update did not replace the guild channel snapshot")
		}
		if beforeChannel.LastPinTimestamp == nil || !beforeChannel.LastPinTimestamp.Equal(oldTimestamp) {
			t.Fatal("channel pins update mutated a held channel snapshot")
		}
	})

	t.Run("null clears guild timestamp", func(t *testing.T) {
		var event ChannelPinsUpdate
		if err := json.Unmarshal([]byte(`{"channel_id":"guild-channel","guild_id":"guild","last_pin_timestamp":null}`), &event); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		if err := state.OnInterface(session, &event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
		channel, err := state.Channel("guild-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}
		if channel.LastPinTimestamp != nil {
			t.Fatalf("LastPinTimestamp = %v, want nil", channel.LastPinTimestamp)
		}
		if event.LastPinTimestamp != "" {
			t.Fatalf("event LastPinTimestamp = %q, want empty null representation", event.LastPinTimestamp)
		}
	})

	t.Run("private channel timestamp", func(t *testing.T) {
		before := state.PrivateChannels[0]
		event := &ChannelPinsUpdate{
			ChannelID:        "dm-channel",
			LastPinTimestamp: "2026-07-13T09:10:11.123Z",
		}
		if err := state.OnInterface(session, event); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
		channel, err := state.Channel("dm-channel")
		if err != nil {
			t.Fatalf("Channel returned error: %v", err)
		}
		if channel.LastPinTimestamp == nil || !channel.LastPinTimestamp.Equal(wantTimestamp) {
			t.Fatalf("LastPinTimestamp = %v, want %v", channel.LastPinTimestamp, wantTimestamp)
		}
		if channel == before || state.PrivateChannels[0] != channel {
			t.Fatal("channel pins update did not replace the private channel snapshot")
		}
	})
}

func TestChannelPinsUpdateStateErrors(t *testing.T) {
	state := NewState()
	oldTimestamp := time.Date(2026, time.July, 12, 8, 0, 0, 0, time.UTC)
	if err := state.ChannelAdd(&Channel{ID: "dm", Type: ChannelTypeDM, LastPinTimestamp: &oldTimestamp}); err != nil {
		t.Fatalf("ChannelAdd returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	if err := state.OnInterface(session, &ChannelPinsUpdate{ChannelID: "missing"}); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("missing channel returned error %v, want %v", err, ErrStateNotFound)
	}
	for _, event := range []*ChannelPinsUpdate{nil, &ChannelPinsUpdate{}} {
		if err := state.OnInterface(session, event); !errors.Is(err, ErrStateInvalidData) {
			t.Fatalf("invalid event returned error %v, want %v", err, ErrStateInvalidData)
		}
	}

	before, err := state.Channel("dm")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	event := &ChannelPinsUpdate{ChannelID: "dm", LastPinTimestamp: "not-a-timestamp"}
	if err := state.OnInterface(session, event); !errors.Is(err, ErrStateInvalidData) {
		t.Fatalf("malformed timestamp returned error %v, want %v", err, ErrStateInvalidData)
	}
	after, err := state.Channel("dm")
	if err != nil {
		t.Fatalf("Channel returned error: %v", err)
	}
	if after != before || after.LastPinTimestamp == nil || !after.LastPinTimestamp.Equal(oldTimestamp) {
		t.Fatal("malformed timestamp changed cached channel state")
	}
	if event.LastPinTimestamp != "not-a-timestamp" {
		t.Fatal("malformed timestamp event was mutated")
	}
}

func TestChannelPinsUpdateStateConcurrentAccess(t *testing.T) {
	state := NewState()
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild", Type: ChannelTypeGuildText}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	session := &Session{StateEnabled: true}

	var readers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for j := 0; j < 1000; j++ {
				channel, err := state.Channel("channel")
				if err == nil && channel.LastPinTimestamp != nil {
					_ = channel.LastPinTimestamp.UnixNano()
				}
				_, _ = state.Guild("guild")
			}
		}()
	}
	close(start)
	for i := 0; i < 1000; i++ {
		timestamp := ""
		if i%2 == 0 {
			timestamp = "2026-07-13T09:10:11.123Z"
		}
		if err := state.OnInterface(session, &ChannelPinsUpdate{
			ChannelID:        "channel",
			GuildID:          "guild",
			LastPinTimestamp: timestamp,
		}); err != nil {
			t.Fatalf("OnInterface returned error: %v", err)
		}
	}
	readers.Wait()
}

const channelWithThreadMember = `{
	"id":"200", "guild_id":"100", "parent_id":"150", "type":12,
	"name":"private-thread", "owner_id":"300",
	"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false,"invitable":true},
	"member":{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1}
}`

func checkCurrentThreadMember(t *testing.T, channel *Channel) {
	t.Helper()
	if channel == nil || channel.Member == nil {
		t.Fatal("channel lost the current user's thread membership")
	}
	member := channel.Member
	if member.ID != "200" || member.UserID != "300" || member.Flags != 1 || !member.JoinTimestamp.Equal(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected thread member: %+v", member)
	}
}

func TestChannelIncludesCurrentThreadMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v"+APIVersion+"/channels/200" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(channelWithThreadMember)), Request: req}, nil
	})
	channel, err := session.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	checkCurrentThreadMember(t, channel)
}

func TestThreadCreateIncludesCurrentMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	var received *ThreadCreate
	session.AddHandler(func(_ *Session, event *ThreadCreate) { received = event })
	_, err = session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"THREAD_CREATE","d":`+channelWithThreadMember+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if received == nil {
		t.Fatal("THREAD_CREATE handler was not called")
	}
	t.Run("callback", func(t *testing.T) { checkCurrentThreadMember(t, received.Channel) })
	cached, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("cache", func(t *testing.T) { checkCurrentThreadMember(t, cached) })
}

func TestGuildMemberAddCountsOnlyNewCachedMembers(t *testing.T) {
	for _, initiallyCached := range []bool{false, true} {
		t.Run(fmt.Sprintf("initially_cached=%t", initiallyCached), func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			member := func(nick string) string {
				return fmt.Sprintf(`{"user":{"id":"200","username":"member"},"nick":%q,"roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false}`, nick)
			}
			members, initialCount := "[]", 0
			if initiallyCached {
				members, initialCount = "["+member("initial")+"]", 1
			}
			dispatch("GUILD_CREATE", fmt.Sprintf(`{"id":"100","name":"guild","member_count":%d,"members":%s}`, initialCount, members))
			original, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			callbacks := 0
			session.AddHandler(func(s *Session, event *GuildMemberAdd) {
				callbacks++
				guild, err := s.State.Guild(event.GuildID)
				if err != nil {
					t.Error(err)
					return
				}
				cached, err := s.State.Member(event.GuildID, event.User.ID)
				if err != nil {
					t.Error(err)
					return
				}
				if guild.MemberCount != 1 || len(guild.Members) != 1 {
					t.Errorf("member add callback sees count=%d, cached members=%d; want 1 each", guild.MemberCount, len(guild.Members))
				}
				if cached.Nick != event.Nick || guild.Members[0] != cached {
					t.Error("member add did not refresh both cache indexes before its callback")
				}
			})
			add := func(nick string) {
				t.Helper()
				data := member(nick)
				dispatch("GUILD_MEMBER_ADD", `{"guild_id":"100",`+data[1:])
			}
			add("first")
			beforeRepeated, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			previousCount := beforeRepeated.MemberCount
			// Discord also sends GUILD_MEMBER_ADD for existing guild members.
			add("refreshed")
			add("refreshed again")
			if original.MemberCount != initialCount || (initiallyCached && original.Members[0].Nick != "initial") || beforeRepeated.MemberCount != previousCount || beforeRepeated.Members[0].Nick != "first" {
				t.Error("later member adds changed an earlier guild snapshot")
			}
			dispatch("GUILD_MEMBER_REMOVE", `{"guild_id":"100","user":{"id":"200","username":"member"}}`)
			afterRemove, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			if afterRemove.MemberCount != 0 || len(afterRemove.Members) != 0 {
				t.Errorf("after removal: count=%d, cached members=%d; want 0 each", afterRemove.MemberCount, len(afterRemove.Members))
			}
			add("rejoined")
			if callbacks != 4 {
				t.Errorf("member add callbacks=%d, want 4", callbacks)
			}
		})
	}
}

func TestGuildMemberEditPreservesGuildIdentity(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "GuildMemberEdit"
		if legacy {
			name = "GuildMemberEditComplex"
		}
		t.Run(name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			body := `{"user":{"id":"200","username":"member","discriminator":"0"},"nick":"renamed","avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}`
			session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPatch || req.URL.String() != EndpointGuildMember("100", "200") {
					t.Errorf("request = %s %s, want member PATCH", req.Method, req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			nick := "renamed"
			edit := func() (*Member, error) {
				params := &GuildMemberParams{Nick: nick}
				if legacy {
					return session.GuildMemberEditComplex("100", "200", params)
				}
				return session.GuildMemberEdit("100", "200", params)
			}
			member, err := edit()
			if err != nil || member == nil {
				t.Fatalf("edit returned %v, %v", member, err)
			}
			if member.GuildID != "100" || member.Nick != nick {
				t.Errorf("edited member identity = guild %q, nick %q", member.GuildID, member.Nick)
			}
			if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
				t.Errorf("AvatarURL = %q, want %q", got, want)
			}
			if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
				t.Errorf("BannerURL = %q, want %q", got, want)
			}
			if err := session.State.MemberAdd(member); err != nil {
				t.Errorf("caching edited member failed: %v", err)
			} else if cached, err := session.State.Member("100", "200"); err != nil || cached.Nick != nick {
				t.Errorf("edited member was not retrievable from its guild: %v", err)
			}

			// Preserve the existing nil result for a null response without a panic.
			body = `null`
			if member, err := edit(); err != nil || member != nil {
				t.Fatalf("null response returned %v, %v", member, err)
			}
		})
	}
}

func TestGuildMemberUpdatePreservesOmittedNickAndPending(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fields       string
		eventNick    string
		wantNick     string
		wantPending  bool
		programmatic bool
	}{
		{name: "omitted", wantNick: "nickname", wantPending: true},
		{name: "clear_nick", fields: `,"nick":null`, wantPending: true},
		{name: "complete_screening", fields: `,"pending":false`, wantNick: "nickname"},
		{name: "replace_both", fields: `,"nick":"new nickname","pending":false`, eventNick: "new nickname", wantNick: "new nickname"},
		{name: "programmatic_zero_values", programmatic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			dispatch := func(sequence int, event, data string) {
				t.Helper()
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, event, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			dispatch(1, "GUILD_CREATE", `{"id":"100","member_count":1,"members":[{"user":{"id":"200","username":"member"},"nick":"nickname","pending":true,"roles":["300"],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false}]}`)
			before, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			original := before.Members[0]
			var callback *GuildMemberUpdate
			session.AddHandler(func(s *Session, update *GuildMemberUpdate) {
				callback = update
				cached, err := s.State.Member("100", "200")
				if err != nil {
					t.Error(err)
					return
				}
				if cached.Nick != tc.wantNick || cached.Pending != tc.wantPending {
					t.Errorf("callback cache nick=%q pending=%t, want %q %t", cached.Nick, cached.Pending, tc.wantNick, tc.wantPending)
				}
				guild, err := s.State.Guild("100")
				if err != nil {
					t.Error(err)
					return
				}
				if guild.Members[0] != cached || cached == original || len(cached.Roles) != 0 || cached.User.Username != "updated" {
					t.Error("member update did not replace both cache indexes and apply supplied fields")
				}
			})
			if tc.programmatic {
				session.handleEvent(guildMemberUpdateEventType, &GuildMemberUpdate{Member: &Member{
					GuildID: "100", User: &User{ID: "200", Username: "updated"}, Roles: []string{},
				}})
			} else {
				dispatch(2, "GUILD_MEMBER_UPDATE", `{"guild_id":"100","user":{"id":"200","username":"updated"},"roles":[],"avatar":null,"banner":null,"joined_at":"2026-10-30T10:00:00Z"`+tc.fields+`}`)
			}
			if callback == nil || callback.BeforeUpdate == nil {
				t.Fatal("member update callback or BeforeUpdate missing")
			}
			if callback.Nick != tc.eventNick || callback.Pending {
				t.Error("cache merging modified the incoming event")
			}
			if original.Nick != "nickname" || !original.Pending || original.User.Username != "member" || len(original.Roles) != 1 ||
				callback.BeforeUpdate.Nick != "nickname" || !callback.BeforeUpdate.Pending {
				t.Error("member update changed an earlier snapshot")
			}
		})
	}
}

func TestGuildMembersChunkIdentityWithoutCache(t *testing.T) {
	for _, mode := range []string{"tracked", "state_disabled", "members_disabled", "guild_uncached"} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/gateway=%t", mode, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = mode != "state_disabled"
				session.State.TrackMembers = mode != "members_disabled"
				if mode != "guild_uncached" {
					if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
						t.Fatal(err)
					}
				}
				cache := NewState()
				if err := cache.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				payload := []byte(`{"guild_id":"100","chunk_index":0,"chunk_count":1,"nonce":"lookup","members":[{
					"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0,"pending":false}]}`)
				called := false
				session.AddHandler(func(_ *Session, event *GuildMembersChunk) {
					called = true
					if len(event.Members) != 1 || event.Members[0] == nil || event.Nonce != "lookup" {
						t.Error("callback did not receive the requested member chunk")
						return
					}
					member := event.Members[0]
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := cache.MemberAdd(member); err != nil {
						t.Errorf("caching callback member: %v", err)
					} else if cached, err := cache.Member("100", "200"); err != nil || cached.GuildID != "100" {
						t.Errorf("retrieving callback member: %v", err)
					}
				})
				if gateway {
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"GUILD_MEMBERS_CHUNK","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else {
					var event GuildMembersChunk
					if err := json.Unmarshal(payload, &event); err != nil {
						t.Fatal(err)
					}
					session.handleEvent(guildMembersChunkEventType, &event)
				}
				if !called {
					t.Fatal("member chunk callback did not run")
				}
			})
		}
	}
}

func TestGuildMembersChunkIdentityAbsentMembers(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	var received *GuildMembersChunk
	session.AddHandler(func(_ *Session, event *GuildMembersChunk) { received = event })
	for _, members := range []string{`[]`, `null`, `[null]`} {
		received = nil
		payload := fmt.Sprintf(`{"op":0,"s":1,"t":"GUILD_MEMBERS_CHUNK","d":{"guild_id":"100","chunk_index":0,"chunk_count":1,"members":%s}}`, members)
		if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if received == nil {
			t.Fatal("callback did not run")
		}
		for _, member := range received.Members {
			if member != nil {
				t.Fatal("absent member was not preserved")
			}
		}
	}
}

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

func TestInteractionMembersPreserveGuildIdentity(t *testing.T) {
	for _, kind := range []InteractionType{InteractionApplicationCommand, InteractionMessageComponent, InteractionApplicationCommandAutocomplete, InteractionModalSubmit} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("type_%d/gateway_%t", kind, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				data := `"id":"300","name":"inspect","type":1,"options":[{"name":"user","type":6,"value":"201"}]`
				switch kind {
				case InteractionMessageComponent:
					data = `"custom_id":"select","component_type":5,"values":["201"]`
				case InteractionApplicationCommandAutocomplete:
					data = `"id":"300","name":"inspect","type":1,"options":[{"name":"user","type":6,"value":"201"},{"name":"query","type":3,"value":"abc","focused":true}]`
				case InteractionModalSubmit:
					data = `"custom_id":"modal","components":[{"type":18,"label":"User","component":{"type":5,"custom_id":"select","values":["201"]}}]`
				}
				payload := []byte(fmt.Sprintf(`{"id":"400","application_id":"500","type":%d,"guild_id":"100","channel_id":"600","token":"test","version":1,
					"member":{"user":{"id":"200","username":"invoker","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0},
					"data":{%s,"resolved":{"users":{"201":{"id":"201","username":"selected","discriminator":"0"}},"members":{"201":{"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","flags":0}}}}}`, kind, data))
				var interaction *Interaction
				if gateway {
					session.AddHandler(func(_ *Session, event *InteractionCreate) { interaction = event.Interaction })
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"INTERACTION_CREATE","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else if err := json.Unmarshal(payload, &interaction); err != nil {
					t.Fatal(err)
				}
				if interaction == nil || interaction.Member == nil {
					t.Fatal("interaction or invoking member missing")
				}
				members := []*Member{interaction.Member}
				switch data := interaction.Data.(type) {
				case ApplicationCommandInteractionData:
					members = append(members, data.Resolved.Members["201"])
				case MessageComponentInteractionData:
					members = append(members, data.Resolved.Members["201"])
				case ModalSubmitInteractionData:
					members = append(members, data.Resolved.Members["201"])
				default:
					t.Fatalf("unexpected interaction data %T", interaction.Data)
				}
				for _, member := range members {
					if member == nil || member.User == nil {
						t.Fatal("member or linked user missing")
					}
					if member.GuildID != "100" {
						t.Errorf("member %s guild = %q, want 100", member.User.ID, member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", member.User.ID, "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", member.User.ID, "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := session.State.MemberAdd(member); err != nil {
						t.Errorf("caching interaction member failed: %v", err)
					} else if cached, err := session.State.Member("100", member.User.ID); err != nil || cached.User.ID != member.User.ID {
						t.Errorf("member not retrievable from its guild: %v", err)
					}
				}
			})
		}
	}
}

func TestInteractionMemberGuildIdentityOptionalData(t *testing.T) {
	for _, payload := range []string{
		`{"type":1}`,
		`{"type":2,"user":{"id":"200"},"data":{"id":"300","name":"inspect","type":1}}`,
		`{"type":2,"guild_id":"100","data":{"resolved":{"members":{"200":null}}}}`,
	} {
		var interaction Interaction
		if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
			t.Fatal(err)
		}
		if interaction.Member != nil {
			t.Fatal("decoder invented an invoking member")
		}
	}
}

func TestMessageCacheRejectsNegativeLimit(t *testing.T) {
	for _, event := range []string{"create", "update", "direct"} {
		t.Run(event, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			state := session.State
			if err := state.ChannelAdd(&Channel{ID: "channel", Type: ChannelTypeDM}); err != nil {
				t.Fatal(err)
			}
			state.MaxMessageCount = -1
			message := &Message{ID: "message", ChannelID: "channel"}
			switch event {
			case "create":
				err = state.OnInterface(session, &MessageCreate{Message: message})
			case "update":
				err = state.OnInterface(session, &MessageUpdate{Message: message})
			case "direct":
				err = state.MessageAdd(message)
			}
			if !errors.Is(err, ErrStateInvalidData) {
				t.Fatalf("error = %v, want ErrStateInvalidData", err)
			}
			channel, err := state.Channel("channel")
			if err != nil {
				t.Fatal(err)
			}
			if len(channel.Messages) != 0 {
				t.Fatal("invalid limit changed the message cache")
			}
		})
	}
}

func TestMessageUpdatePreservesInteractionMemberSnapshot(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v"+APIVersion+"/channels/channel/messages/message" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		// guild_id is a gateway extra field, absent from this REST message.
		body := `{
			"id":"message","channel_id":"channel","author":{"id":"bot","username":"Bot","bot":true},
			"content":"before","timestamp":"2026-10-30T10:00:00Z","edited_timestamp":null,
			"tts":false,"mention_everyone":false,"mentions":[],"mention_roles":[],
			"attachments":[],"embeds":[],"pinned":false,"type":20,
			"interaction":{
				"id":"interaction","type":2,"name":"command","user":{"id":"user","username":"User"},
				"member":{"nick":"Nick","roles":[],"joined_at":"2025-01-01T00:00:00Z"}
			}
		}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	message, err := session.ChannelMessage("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	session.State = newMessageUpdateState(t, message)
	before, err := session.State.Message("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	if before.Interaction == nil || before.Interaction.Member == nil || before.Interaction.Member.GuildID != "" {
		t.Fatalf("unexpected initial interaction: %#v", before.Interaction)
	}
	var event *MessageUpdate
	session.AddHandler(func(_ *Session, update *MessageUpdate) { event = update })
	start := make(chan struct{})
	readDone := make(chan int, 1)
	go func() {
		<-start
		observed := 0
		for range 1000 {
			if before.Interaction.Member.GuildID != "" {
				observed++
			}
		}
		readDone <- observed
	}()
	close(start)
	_, updateErr := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"t":"MESSAGE_UPDATE","s":1,"d":{"id":"message","channel_id":"channel","guild_id":"guild","content":"after"}}`))
	observed := <-readDone
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	after, err := session.State.Message("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	if observed != 0 || before.Interaction.Member.GuildID != "" || message.Interaction.Member.GuildID != "" {
		t.Fatal("message update mutated the earlier interaction member snapshot")
	}
	if event == nil || event.BeforeUpdate == nil || event.BeforeUpdate.Interaction == nil || event.BeforeUpdate.Interaction.Member == nil || event.BeforeUpdate.Interaction.Member.GuildID != "" {
		t.Fatal("BeforeUpdate lost the original interaction member")
	}
	if event.Interaction != nil {
		t.Fatal("gateway event was populated with omitted interaction data")
	}
	if after.Content != "after" || after.GuildID != "guild" || after.Interaction == nil || after.Interaction.Member == nil {
		t.Fatalf("unexpected updated message: %#v", after)
	}
	if after.Interaction.Member.GuildID != "guild" || after.Interaction.Member.User != after.Interaction.User || after.Interaction.Member.Nick != "Nick" {
		t.Fatalf("updated interaction member was not preserved and linked: %#v", after.Interaction.Member)
	}
}

func TestMessageUpdatePreservesMemberSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		author bool
	}{
		{name: "author still omitted", fields: `"content":"edited"`},
		{name: "author arrives", fields: `"author":{"id":"author","username":"Author"}`, author: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			session.State = newMessageUpdateState(t, nil)
			// An update for an uncached message can carry a partial member
			// before the cache knows the message's author.
			initial := `{"op":0,"t":"MESSAGE_UPDATE","s":1,"d":{"id":"message","channel_id":"channel","guild_id":"guild","member":{"nick":"Nick","roles":[],"joined_at":"2025-01-01T00:00:00Z"}}}`
			if _, err := session.onEvent(websocket.TextMessage, []byte(initial)); err != nil {
				t.Fatal(err)
			}
			before, err := session.State.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			if before.Member == nil || before.Member.User != nil || before.Member.GuildID != "guild" {
				t.Fatalf("unexpected initial member: %#v", before.Member)
			}
			var event *MessageUpdate
			session.AddHandler(func(_ *Session, update *MessageUpdate) { event = update })

			// Cached messages are snapshots. Reading an earlier member must
			// remain safe while another gateway update fills omitted fields.
			start := make(chan struct{})
			readDone := make(chan int, 1)
			go func() {
				<-start
				observed := 0
				for range 1000 {
					if before.Member.User != nil {
						observed++
					}
				}
				readDone <- observed
			}()
			close(start)
			payload := `{"op":0,"t":"MESSAGE_UPDATE","s":2,"d":{"id":"message","channel_id":"channel","guild_id":"guild",` + tc.fields + `}}`
			_, updateErr := session.onEvent(websocket.TextMessage, []byte(payload))
			observed := <-readDone
			if updateErr != nil {
				t.Fatal(updateErr)
			}
			after, err := session.State.Message("channel", "message")
			if err != nil {
				t.Fatal(err)
			}
			if observed != 0 || before.Member.User != nil || event == nil || event.BeforeUpdate == nil || event.BeforeUpdate.Member == nil || event.BeforeUpdate.Member.User != nil {
				t.Fatal("message update mutated the member in an earlier snapshot")
			}
			if after.Member == nil || after.Member.Nick != "Nick" || after.Member.GuildID != "guild" {
				t.Fatalf("updated message lost member data: %#v", after.Member)
			}
			if tc.author && (after.Author == nil || after.Member.User != after.Author) {
				t.Fatal("updated member was not linked to the newly received author")
			}
			if !tc.author && (after.Author != nil || after.Member.User != nil) {
				t.Fatal("update invented an omitted author")
			}
		})
	}
}

func TestMessageResolvedMembersPreserveGuildIdentity(t *testing.T) {
	for _, eventType := range []string{messageCreateEventType, messageUpdateEventType} {
		for _, stateEnabled := range []bool{false, true} {
			for _, gateway := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/state=%t/gateway=%t", eventType, stateEnabled, gateway), func(t *testing.T) {
					session, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					session.SyncEvents = true
					session.StateEnabled = stateEnabled
					if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
						t.Fatal(err)
					}
					payload := []byte(`{"id":"400","channel_id":"300","guild_id":"100",
						"resolved":{"users":{"200":{"id":"200","username":"selected","discriminator":"0"}},
						"members":{"200":{"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","flags":0}}}}`)
					var received *Message
					session.AddHandler(func(_ *Session, event *MessageCreate) { received = event.Message })
					session.AddHandler(func(_ *Session, event *MessageUpdate) { received = event.Message })
					if gateway {
						if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":%q,"d":%s}`, eventType, payload))); err != nil {
							t.Fatal(err)
						}
					} else {
						var event interface{}
						if eventType == messageCreateEventType {
							event = &MessageCreate{}
						} else {
							event = &MessageUpdate{}
						}
						if err := json.Unmarshal(payload, event); err != nil {
							t.Fatal(err)
						}
						session.handleEvent(eventType, event)
					}
					if received == nil || received.Resolved == nil {
						t.Fatal("callback did not receive message resolved data")
					}
					member := received.Resolved.Members["200"]
					if member == nil || member.User == nil || member.User != received.Resolved.Users["200"] {
						t.Fatal("resolved member or linked user missing")
					}
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := session.State.MemberAdd(member); err != nil {
						t.Errorf("caching resolved member failed: %v", err)
					} else if cached, err := session.State.Member("100", "200"); err != nil || cached.GuildID != "100" || cached.User.ID != "200" {
						t.Errorf("member not retrievable from its guild: %v", err)
					}
				})
			}
		}
	}
}

func TestMessageResolvedMemberGuildIdentityOptionalData(t *testing.T) {
	for _, payload := range []string{
		`{"guild_id":"100"}`,
		`{"guild_id":"100","resolved":null}`,
		`{"guild_id":"100","resolved":{"members":null}}`,
		`{"guild_id":"100","resolved":{"members":{"200":null}}}`,
	} {
		var message Message
		if err := json.Unmarshal([]byte(payload), &message); err != nil {
			t.Fatal(err)
		}
		if message.Resolved != nil && message.Resolved.Members["200"] != nil {
			t.Fatal("decoder invented a resolved member")
		}
	}
}

func newMessageUpdateState(t *testing.T, message *Message) *State {
	t.Helper()
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		t.Fatalf("GuildAdd returned error: %v", err)
	}
	if message != nil {
		if err := state.MessageAdd(message); err != nil {
			t.Fatalf("MessageAdd returned error: %v", err)
		}
	}
	return state
}

func unmarshalMessageUpdate(t *testing.T, payload string) *MessageUpdate {
	t.Helper()
	var update MessageUpdate
	if err := json.Unmarshal([]byte(payload), &update); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return &update
}

func TestMessageUpdateMergePreservesOmittedFields(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	message := &Message{
		ID:              "message",
		ChannelID:       "channel",
		GuildID:         "guild",
		Content:         "before",
		Timestamp:       timestamp,
		Author:          &User{ID: "author"},
		Flags:           MessageFlagsSuppressEmbeds,
		Type:            MessageTypeReply,
		Pinned:          true,
		TTS:             true,
		MentionEveryone: true,
		MentionRoles:    []string{"role"},
		Reactions: []*MessageReactions{{
			Count: 1,
			Me:    true,
			Emoji: &Emoji{Name: "wave"},
		}},
	}
	state := newMessageUpdateState(t, message)
	update := unmarshalMessageUpdate(t, `{"id":"message","channel_id":"channel","content":"after"}`)

	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if got.Content != "after" || got.Author != message.Author || got.Timestamp != timestamp ||
		got.Flags != message.Flags || got.Type != message.Type || !got.Pinned || !got.TTS ||
		!got.MentionEveryone || !reflect.DeepEqual(got.MentionRoles, message.MentionRoles) ||
		!reflect.DeepEqual(got.Reactions, message.Reactions) {
		t.Fatalf("cached message lost omitted fields: %#v", got)
	}
	if update.Author != nil || update.Flags != 0 || update.Type != 0 || update.Pinned || update.Reactions != nil {
		t.Fatalf("event payload was replaced with merged state: %#v", update.Message)
	}
	if update.BeforeUpdate == nil || update.BeforeUpdate.Content != "before" || update.BeforeUpdate == got {
		t.Fatalf("BeforeUpdate = %#v, want isolated pre-update message", update.BeforeUpdate)
	}
}

func TestMessageUpdateMergeAppliesEveryExplicitField(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	one := 1
	message := &Message{
		ID: "message", ChannelID: "channel", ChannelType: ChannelTypeGuildText, GuildID: "guild",
		Content: "old", Timestamp: timestamp, EditedTimestamp: &timestamp, MentionRoles: []string{"role"},
		TTS: true, MentionEveryone: true, Author: &User{ID: "author"},
		Attachments: []*MessageAttachment{{ID: "attachment"}}, Components: []MessageComponent{&ActionsRow{}},
		Embeds: []*MessageEmbed{{Title: "embed"}}, Mentions: []*User{{ID: "mention"}},
		Reactions: []*MessageReactions{{Count: 1, Me: true, Emoji: &Emoji{Name: "wave"}}}, Nonce: "nonce",
		Pinned: true, Type: MessageTypeReply, WebhookID: "webhook", Member: &Member{GuildID: "guild", User: &User{ID: "member"}},
		MentionChannels: []*Channel{{ID: "mentioned-channel"}}, Activity: &MessageActivity{Type: MessageActivityTypeJoin, PartyID: "party"},
		Application: &MessageApplication{ID: "application"}, ApplicationID: "application",
		MessageReference: &MessageReference{MessageID: "reference"}, ReferencedMessage: &Message{ID: "reference"},
		MessageSnapshots: []MessageSnapshot{{Message: &Message{ID: "snapshot"}}}, Interaction: &MessageInteraction{ID: "interaction"},
		InteractionMetadata: &MessageInteractionMetadata{ID: "metadata"}, Flags: MessageFlagsSuppressEmbeds,
		Thread: &Channel{ID: "thread"}, StickerItems: []*StickerItem{{ID: "sticker"}}, Stickers: []*Sticker{{ID: "legacy-sticker"}},
		Position: &one, RoleSubscriptionData: &MessageRoleSubscriptionData{TierName: "tier"},
		Resolved: &ComponentInteractionDataResolved{Users: map[string]*User{"user": {ID: "user"}}},
		Poll:     &Poll{AllowMultiselect: true}, Call: &MessageCall{Participants: []string{"user"}},
		SharedClientTheme: &MessageSharedClientTheme{Colors: []string{"ffffff"}, GradientAngle: 1, BaseMix: 1},
	}
	state := newMessageUpdateState(t, message)
	update := unmarshalMessageUpdate(t, `{
		"id":"message","channel_id":"channel","channel_type":0,"guild_id":"guild",
		"content":"","timestamp":"0001-01-01T00:00:00Z","edited_timestamp":null,"mention_roles":[],
		"tts":false,"mention_everyone":false,"author":null,"attachments":[],"components":[],"embeds":[],
		"mentions":[],"reactions":[{"count":0,"count_details":{"burst":0,"normal":0},"me":false,"me_burst":false,"emoji":{"id":null,"name":""},"burst_colors":[]}],
		"nonce":"","pinned":false,"type":0,"webhook_id":"","member":null,"mention_channels":[],
		"activity":null,"application":null,"application_id":"","message_reference":null,"referenced_message":null,
		"message_snapshots":[],"interaction":null,"interaction_metadata":null,"flags":0,"thread":null,
		"sticker_items":[],"stickers":[],"position":null,"role_subscription_data":null,"resolved":null,
		"poll":null,"call":null,"shared_client_theme":null
	}`)

	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if !reflect.DeepEqual(got, update.Message) {
		t.Fatalf("cached message = %#v, want explicit update %#v", got, update.Message)
	}
	if got.Reactions[0].Count != 0 || got.Reactions[0].Me || got.Flags != 0 || got.Type != 0 || got.Pinned {
		t.Fatalf("explicit zero values were not applied: %#v", got)
	}
	if got.MentionRoles == nil || got.Attachments == nil || got.Components == nil || got.Embeds == nil || got.Mentions == nil {
		t.Fatalf("explicit empty arrays became nil: %#v", got)
	}
	if got.EditedTimestamp != nil || got.Poll != nil || got.ReferencedMessage != nil || got.Position != nil {
		t.Fatalf("explicit null values were not applied: %#v", got)
	}
	update.Reactions[0].Count = 99
	if got.Reactions[0].Count != 0 {
		t.Fatalf("cached update aliases event payload: %#v", got.Reactions)
	}
}

func TestMessageUpdateMergeCacheMiss(t *testing.T) {
	state := newMessageUpdateState(t, nil)
	update := unmarshalMessageUpdate(t, `{"id":"message","channel_id":"channel","content":"partial"}`)
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if got.Content != "partial" || got.Author != nil || update.BeforeUpdate != nil {
		t.Fatalf("cache miss message = %#v, BeforeUpdate = %#v", got, update.BeforeUpdate)
	}
	if got == update.Message {
		t.Fatal("cached message aliases event payload")
	}
	update.Content = "mutated"
	if got.Content != "partial" {
		t.Fatalf("cached content = %q after event mutation, want partial", got.Content)
	}
}

func BenchmarkMessageUpdateMergeCacheMiss(b *testing.B) {
	state := NewState()
	state.MaxMessageCount = 10
	if err := state.GuildAdd(&Guild{
		ID:       "guild",
		Channels: []*Channel{{ID: "channel", GuildID: "guild"}},
	}); err != nil {
		b.Fatal(err)
	}
	updates := make([]*MessageUpdate, state.MaxMessageCount+1)
	for i := range updates {
		updates[i] = &MessageUpdate{Message: &Message{
			ID:        "message-" + strconv.Itoa(i),
			ChannelID: "channel",
		}}
	}
	for i := 0; i < state.MaxMessageCount; i++ {
		if err := state.MessageAdd(updates[i].Message); err != nil {
			b.Fatal(err)
		}
	}

	i := state.MaxMessageCount
	b.ReportAllocs()
	b.ResetTimer()
	for ; i < b.N+state.MaxMessageCount; i++ {
		update := updates[i%len(updates)]
		if err := state.messageUpdate(update); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMessageUpdateMergeProgrammaticCompatibility(t *testing.T) {
	state := newMessageUpdateState(t, &Message{
		ID:        "message",
		ChannelID: "channel",
		Content:   "before",
		Flags:     MessageFlagsSuppressEmbeds,
	})
	update := &MessageUpdate{Message: &Message{ID: "message", ChannelID: "channel", Content: "after"}}
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatalf("OnInterface returned error: %v", err)
	}
	got, err := state.Message("channel", "message")
	if err != nil {
		t.Fatalf("Message returned error: %v", err)
	}
	if got.Content != "after" || got.Flags != MessageFlagsSuppressEmbeds {
		t.Fatalf("programmatic update = %#v", got)
	}
}

func TestMessageUpdateMergeConcurrent(t *testing.T) {
	state := newMessageUpdateState(t, &Message{ID: "message", ChannelID: "channel", Content: "initial"})
	session := &Session{StateEnabled: true}
	var wg sync.WaitGroup
	updates := make([]*MessageUpdate, 25)
	for i := range updates {
		updates[i] = unmarshalMessageUpdate(t, `{"id":"message","channel_id":"channel","content":"updated"}`)
	}
	for _, update := range updates {
		wg.Add(2)
		go func(update *MessageUpdate) {
			defer wg.Done()
			if err := state.OnInterface(session, update); err != nil {
				t.Errorf("OnInterface returned error: %v", err)
			}
		}(update)
		go func() {
			defer wg.Done()
			if _, err := state.Message("channel", "message"); err != nil {
				t.Errorf("Message returned error: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestMessageUpdateUnmarshalMalformed(t *testing.T) {
	for _, payload := range []string{`{"id":`, `[]`, `{"id":false}`} {
		var update MessageUpdate
		if err := json.Unmarshal([]byte(payload), &update); err == nil {
			t.Fatalf("json.Unmarshal(%q) returned nil error", payload)
		}
		if update.Message != nil {
			t.Fatalf("json.Unmarshal(%q) left Message = %#v", payload, update.Message)
		}
	}
}

type permissionRoundTripper func(*http.Request) (*http.Response, error)

func (f permissionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestEffectiveChannelPermissions(t *testing.T) {
	activeTimeout := time.Now().Add(time.Hour)
	expiredTimeout := time.Now().Add(-time.Hour)

	const sendDependencies int64 = PermissionSendTTSMessages |
		PermissionMentionEveryone |
		PermissionEmbedLinks |
		PermissionAttachFiles
	const guildPermissions int64 = PermissionManageGuild |
		PermissionViewAuditLogs |
		PermissionManageEvents |
		PermissionCreateEvents
	const voicePermissions int64 = PermissionVoicePrioritySpeaker |
		PermissionVoiceStreamVideo |
		PermissionVoiceConnect |
		PermissionVoiceSpeak |
		PermissionVoiceMuteMembers |
		PermissionVoiceDeafenMembers |
		PermissionVoiceMoveMembers |
		PermissionVoiceUseVAD |
		PermissionUseEmbeddedActivities |
		PermissionUseSoundboard |
		PermissionUseExternalSounds
	const connectDependentPermissions int64 = voicePermissions |
		PermissionManageChannels |
		PermissionManageRoles
	const basePermissions int64 = PermissionViewChannel |
		PermissionSendMessages |
		PermissionSendMessagesInThreads |
		sendDependencies |
		PermissionReadMessageHistory |
		guildPermissions

	tests := []struct {
		name        string
		permissions int64
		overwrites  []*PermissionOverwrite
		channelType ChannelType
		thread      bool
		timeout     *time.Time
		owner       bool
		admin       bool
		want        int64
	}{
		{
			name:        "nil timeout keeps permissions",
			permissions: basePermissions,
			want:        basePermissions,
		},
		{
			name:        "missing view channel clears channel permissions",
			permissions: basePermissions &^ PermissionViewChannel,
			want:        guildPermissions,
		},
		{
			name:        "missing send messages clears dependent permissions",
			permissions: basePermissions &^ PermissionSendMessages,
			want:        basePermissions &^ PermissionSendMessages &^ sendDependencies,
		},
		{
			name:        "thread uses send messages in threads",
			permissions: basePermissions,
			thread:      true,
			want:        basePermissions &^ PermissionSendMessages,
		},
		{
			name:        "missing thread send clears dependent permissions",
			permissions: basePermissions &^ PermissionSendMessagesInThreads,
			thread:      true,
			want:        basePermissions &^ PermissionSendMessagesInThreads &^ PermissionSendMessages &^ sendDependencies,
		},
		{
			name:        "voice without connect clears voice and channel management permissions",
			permissions: basePermissions | connectDependentPermissions,
			overwrites: []*PermissionOverwrite{
				{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionVoiceConnect},
			},
			channelType: ChannelTypeGuildVoice,
			want:        basePermissions,
		},
		{
			name:        "voice without connect keeps set voice channel status",
			permissions: basePermissions | connectDependentPermissions | PermissionSetVoiceChannelStatus,
			overwrites: []*PermissionOverwrite{
				{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionVoiceConnect},
			},
			channelType: ChannelTypeGuildVoice,
			want:        basePermissions | PermissionSetVoiceChannelStatus,
		},
		{
			name: "stage without connect clears voice but keeps stage permissions",
			permissions: basePermissions |
				(connectDependentPermissions &^ PermissionVoiceConnect) |
				PermissionVoiceRequestToSpeak,
			channelType: ChannelTypeGuildStageVoice,
			want:        basePermissions | PermissionVoiceRequestToSpeak,
		},
		{
			name:        "member overwrite restores voice connect",
			permissions: basePermissions | connectDependentPermissions,
			overwrites: []*PermissionOverwrite{
				{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionVoiceConnect},
				{ID: "user", Type: PermissionOverwriteTypeMember, Allow: PermissionVoiceConnect},
			},
			channelType: ChannelTypeGuildVoice,
			want:        basePermissions | connectDependentPermissions,
		},
		{
			name:        "active timeout keeps view and history",
			permissions: basePermissions,
			timeout:     &activeTimeout,
			want:        PermissionViewChannel | PermissionReadMessageHistory,
		},
		{
			name:        "expired timeout keeps permissions",
			permissions: basePermissions,
			timeout:     &expiredTimeout,
			want:        basePermissions,
		},
		{
			name:        "owner bypasses active timeout and thread masks",
			permissions: basePermissions,
			thread:      true,
			timeout:     &activeTimeout,
			owner:       true,
			want:        PermissionAll,
		},
		{
			name:        "administrator bypasses active timeout and thread masks",
			permissions: basePermissions,
			thread:      true,
			timeout:     &activeTimeout,
			admin:       true,
			want:        PermissionAll,
		},
		{
			name:        "owner bypasses missing voice connect",
			permissions: basePermissions | (connectDependentPermissions &^ PermissionVoiceConnect),
			channelType: ChannelTypeGuildVoice,
			owner:       true,
			want:        PermissionAll,
		},
		{
			name:        "administrator bypasses missing stage connect",
			permissions: basePermissions | (connectDependentPermissions &^ PermissionVoiceConnect),
			channelType: ChannelTypeGuildStageVoice,
			admin:       true,
			want:        PermissionAll,
		},
		{
			name:        "member overwrite restores send messages",
			permissions: basePermissions,
			overwrites: []*PermissionOverwrite{
				{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionSendMessages},
				{ID: "user", Type: PermissionOverwriteTypeMember, Allow: PermissionSendMessages},
			},
			want: basePermissions,
		},
		{
			name:        "dependent allows do not restore denied send messages",
			permissions: basePermissions,
			overwrites: []*PermissionOverwrite{
				{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionSendMessages},
				{ID: "user", Type: PermissionOverwriteTypeMember, Allow: sendDependencies},
			},
			want: basePermissions &^ PermissionSendMessages &^ sendDependencies,
		},
		{
			name:        "missing view channel wins after overwrites",
			permissions: basePermissions,
			thread:      true,
			overwrites: []*PermissionOverwrite{
				{ID: "guild", Type: PermissionOverwriteTypeRole, Deny: PermissionViewChannel},
				{ID: "user", Type: PermissionOverwriteTypeMember, Allow: PermissionSendMessages},
			},
			want: guildPermissions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := []*Role{{ID: "guild", Permissions: tt.permissions}}
			memberRoles := []string{}
			if tt.admin {
				roles = append(roles, &Role{ID: "admin", Permissions: PermissionAdministrator})
				memberRoles = append(memberRoles, "admin")
			}

			member := &Member{
				GuildID:                    "guild",
				User:                       &User{ID: "user"},
				Roles:                      memberRoles,
				CommunicationDisabledUntil: tt.timeout,
			}
			channel := &Channel{
				ID:                   "channel",
				GuildID:              "guild",
				Type:                 tt.channelType,
				PermissionOverwrites: tt.overwrites,
			}
			guild := &Guild{
				ID:       "guild",
				OwnerID:  "owner",
				Roles:    roles,
				Members:  []*Member{member},
				Channels: []*Channel{channel},
			}
			channelID := channel.ID
			if tt.owner {
				guild.OwnerID = member.User.ID
			}
			if tt.thread {
				thread := &Channel{
					ID:       "thread",
					GuildID:  guild.ID,
					ParentID: channel.ID,
					Type:     ChannelTypeGuildPublicThread,
				}
				guild.Threads = []*Channel{thread}
				channelID = thread.ID
			}

			newState := func(includeMember bool) *State {
				state := NewState()
				guildCopy := *guild
				if !includeMember {
					guildCopy.Members = nil
				}
				if err := state.GuildAdd(&guildCopy); err != nil {
					t.Fatalf("GuildAdd returned error: %v", err)
				}
				return state
			}

			state := newState(true)
			statePermissions, stateErr := state.UserChannelPermissions(member.User.ID, channelID)
			messagePermissions, messageErr := state.MessagePermissions(&Message{
				ChannelID: channelID,
				Author:    member.User,
				Member:    member,
			})
			stateSession := &Session{State: state}
			stateSessionPermissions, stateSessionErr := stateSession.UserChannelPermissions(member.User.ID, channelID)

			memberJSON, err := json.Marshal(member)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			requests := 0
			restSession, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			restSession.State = newState(false)
			restSession.Client.Transport = permissionRoundTripper(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.Method != http.MethodGet {
					t.Fatalf("method = %q, want %q", r.Method, http.MethodGet)
				}
				if !strings.HasSuffix(r.URL.Path, "/guilds/guild/members/user") {
					t.Fatalf("path = %q, want guild member endpoint", r.URL.Path)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(string(memberJSON))),
					Request:    r,
				}, nil
			})
			restPermissions, restErr := restSession.UserChannelPermissions(member.User.ID, channelID)

			results := []struct {
				name        string
				permissions int64
				err         error
			}{
				{name: "State.UserChannelPermissions", permissions: statePermissions, err: stateErr},
				{name: "State.MessagePermissions", permissions: messagePermissions, err: messageErr},
				{name: "Session.UserChannelPermissions state", permissions: stateSessionPermissions, err: stateSessionErr},
				{name: "Session.UserChannelPermissions REST fallback", permissions: restPermissions, err: restErr},
			}
			for _, result := range results {
				t.Run(result.name, func(t *testing.T) {
					if result.err != nil {
						t.Fatalf("returned error: %v", result.err)
					}
					if result.permissions != tt.want {
						t.Fatalf("permissions = %d, want %d", result.permissions, tt.want)
					}
				})
			}

			wantRequests := 1
			if tt.owner {
				wantRequests = 0
			}
			if requests != wantRequests {
				t.Fatalf("REST requests = %d, want %d", requests, wantRequests)
			}
		})
	}
}

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

func TestPresenceUpdateClearsExplicitNullAvatar(t *testing.T) {
	for _, tc := range []struct {
		name            string
		avatar          string
		wantAvatar      string
		offline         bool
		disableTracking bool
		disableState    bool
		programmatic    bool
	}{
		{name: "clear_online", avatar: `,"avatar":null`},
		{name: "clear_offline", avatar: `,"avatar":null`, offline: true},
		{name: "replace", avatar: `,"avatar":"new-avatar"`, wantAvatar: "new-avatar"},
		{name: "omit", wantAvatar: "old-avatar"},
		{name: "malformed_avatar", avatar: `,"avatar":123`, wantAvatar: "old-avatar"},
		{name: "tracking_disabled", avatar: `,"avatar":null`, wantAvatar: "old-avatar", disableTracking: true},
		{name: "state_disabled", avatar: `,"avatar":null`, wantAvatar: "old-avatar", disableState: true},
		{name: "programmatic_zero", wantAvatar: "old-avatar", programmatic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			if err := session.State.GuildAdd(&Guild{ID: "100", Presences: []*Presence{{
				User:   &User{ID: "200", Username: "cached-name", Discriminator: "0", Avatar: "old-avatar"},
				Status: StatusOnline,
			}}}); err != nil {
				t.Fatal(err)
			}
			before, err := session.State.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			old := before.Presences[0]
			session.State.TrackPresences = !tc.disableTracking
			session.StateEnabled = !tc.disableState
			called := false
			session.AddHandler(func(s *Session, update *PresenceUpdate) {
				called = true
				if update.GuildID != "100" || update.User == nil || update.User.ID != "200" || update.User.Username != "" {
					t.Error("presence decoding or cache merge changed the event's identity/user fields")
				}
				current, err := s.State.Presence("100", "200")
				if err != nil {
					t.Error(err)
					return
				}
				if current.User.Avatar != tc.wantAvatar || current.User.Username != "cached-name" {
					t.Errorf("cached avatar/name = %q/%q, want %q/cached-name", current.User.Avatar, current.User.Username, tc.wantAvatar)
				}
				wantUser := &User{ID: "200", Discriminator: "0", Avatar: tc.wantAvatar}
				if got := current.User.AvatarURL(""); got != wantUser.AvatarURL("") {
					t.Errorf("cached avatar URL = %q, want %q", got, wantUser.AvatarURL(""))
				}
				guild, err := s.State.Guild("100")
				if err != nil || len(guild.Presences) != 1 || guild.Presences[0] != current {
					t.Errorf("guild and presence cache disagree: %v", err)
				}
				if !tc.disableTracking && !tc.disableState && (current == old || current.User == old.User) {
					t.Error("presence update reused the previous snapshot")
				}
			})
			status := StatusIdle
			if tc.offline {
				status = StatusOffline
			}
			if tc.programmatic {
				session.handleEvent(presenceUpdateEventType, &PresenceUpdate{GuildID: "100", Presence: Presence{User: &User{ID: "200"}, Status: status}})
			} else {
				payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200"%s},"status":%q,"activities":[],"client_status":{}}}`, tc.avatar, status)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			if !called {
				t.Fatal("presence callback did not run")
			}
			if old.User.Avatar != "old-avatar" || before.Presences[0] != old || old.Status != StatusOnline {
				t.Error("presence update mutated an earlier snapshot")
			}
		})
	}
}

func TestPresenceUpdateClearsInactiveClients(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  Status
		clients string
		want    ClientStatus
	}{
		{"desktop only", StatusIdle, `{"desktop":"idle"}`, ClientStatus{Desktop: StatusIdle}},
		{"mobile only", StatusOnline, `{"mobile":"online"}`, ClientStatus{Mobile: StatusOnline}},
		{"web only", StatusDoNotDisturb, `{"web":"dnd"}`, ClientStatus{Web: StatusDoNotDisturb}},
		{"offline", StatusOffline, `{}`, ClientStatus{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := NewState()
			initial := &Presence{
				User:         &User{ID: "user", Username: "cached-name"},
				Status:       StatusOnline,
				ClientStatus: ClientStatus{Desktop: StatusOnline, Mobile: StatusOnline, Web: StatusOnline},
			}
			if err := state.GuildAdd(&Guild{ID: "guild", Presences: []*Presence{initial}}); err != nil {
				t.Fatal(err)
			}
			old, err := state.Presence("guild", "user")
			if err != nil {
				t.Fatal(err)
			}
			var update PresenceUpdate
			payload := fmt.Sprintf(`{"guild_id":"guild","user":{"id":"user"},"status":%q,"activities":[],"client_status":%s}`, test.status, test.clients)
			if err := json.Unmarshal([]byte(payload), &update); err != nil {
				t.Fatal(err)
			}
			if err := state.OnInterface(&Session{StateEnabled: true}, &update); err != nil {
				t.Fatal(err)
			}
			current, err := state.Presence("guild", "user")
			if err != nil {
				t.Fatal(err)
			}
			if current.ClientStatus != test.want {
				t.Errorf("client status = %+v, want %+v", current.ClientStatus, test.want)
			}
			if current.Status != test.status || current.User.Username != "cached-name" {
				t.Errorf("presence status/user = %s/%s", current.Status, current.User.Username)
			}
			if old.ClientStatus != (ClientStatus{Desktop: StatusOnline, Mobile: StatusOnline, Web: StatusOnline}) {
				t.Errorf("previous snapshot changed: %+v", old.ClientStatus)
			}
		})
	}
}

func TestPresenceUpdateRefreshesCachedGlobalName(t *testing.T) {
	for _, tracking := range []struct {
		name      string
		presences bool
		members   bool
	}{
		{"both", true, true},
		{"presences_only", true, false},
		{"members_only", false, true},
		{"disabled", false, false},
	} {
		for _, tc := range []struct {
			name         string
			field        string
			want         string
			offline      bool
			programmatic bool
		}{
			{name: "replace", field: `,"global_name":"new-name"`, want: "new-name"},
			{name: "clear", field: `,"global_name":null`},
			{name: "omit", want: "old-name"},
			{name: "replace_offline", field: `,"global_name":"new-name"`, want: "new-name", offline: true},
			{name: "clear_offline", field: `,"global_name":null`, offline: true},
			{name: "omit_offline", want: "old-name", offline: true},
			{name: "malformed", field: `,"global_name":123`, want: "old-name"},
			{name: "programmatic_zero", want: "old-name", programmatic: true},
		} {
			t.Run(tracking.name+"/"+tc.name, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel, session.SyncEvents = -1, true
				session.Identify.Intents = IntentsGuildPresences
				user := &User{ID: "200", Username: "cached-name", GlobalName: "old-name", Avatar: "avatar"}
				if err := session.State.GuildAdd(&Guild{ID: "100",
					Members:   []*Member{{GuildID: "100", User: user, Roles: []string{"role"}}},
					Presences: []*Presence{{User: user, Status: StatusOnline}},
				}); err != nil {
					t.Fatal(err)
				}
				before, err := session.State.Guild("100")
				if err != nil {
					t.Fatal(err)
				}
				oldMember, oldPresence := before.Members[0], before.Presences[0]
				session.State.TrackPresences, session.State.TrackMembers = tracking.presences, tracking.members
				called := false
				session.AddHandler(func(s *Session, update *PresenceUpdate) {
					called = true
					member, err := s.State.Member("100", "200")
					if err != nil {
						t.Error(err)
						return
					}
					presence, err := s.State.Presence("100", "200")
					if err != nil {
						t.Error(err)
						return
					}
					for _, cache := range []struct {
						name        string
						user        *User
						displayName string
						tracked     bool
					}{
						{"member", member.User, member.DisplayName(), tracking.members},
						{"presence", presence.User, presence.User.DisplayName(), tracking.presences},
					} {
						want := "old-name"
						if cache.tracked {
							want = tc.want
						}
						wantDisplay := want
						if wantDisplay == "" {
							wantDisplay = "cached-name"
						}
						if cache.user.GlobalName != want || cache.displayName != wantDisplay {
							t.Errorf("%s global/display name = %q/%q, want %q/%q", cache.name, cache.user.GlobalName, cache.displayName, want, wantDisplay)
						}
						if cache.user.Username != "cached-name" || cache.user.Avatar != "avatar" {
							t.Errorf("%s merge lost omitted user fields", cache.name)
						}
					}
					guild, err := s.State.Guild("100")
					if err != nil || len(guild.Members) != 1 || guild.Members[0] != member || len(guild.Presences) != 1 || guild.Presences[0] != presence {
						t.Errorf("guild and member/presence caches disagree: %v", err)
					}
					if len(member.Roles) != 1 || member.Roles[0] != "role" {
						t.Error("partial presence lost member roles")
					}
					if update.User == nil || update.User.ID != "200" || update.User.Username != "" || update.User.Avatar != "" {
						t.Error("cache merge changed the partial event user")
					}
				})
				status := StatusOnline
				if tc.offline {
					status = StatusOffline
				}
				if tc.programmatic {
					session.handleEvent(presenceUpdateEventType, &PresenceUpdate{GuildID: "100", Presence: Presence{User: &User{ID: "200"}, Status: status}})
				} else {
					payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200"%s},"status":%q,"activities":[],"client_status":{}}}`, tc.field, status)
					if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				if !called {
					t.Fatal("presence callback did not run")
				}
				if oldMember.User.GlobalName != "old-name" || oldPresence.User.GlobalName != "old-name" || before.Members[0] != oldMember || before.Presences[0] != oldPresence {
					t.Error("presence update changed an earlier guild/member/presence snapshot")
				}
			})
		}
	}
}

func TestPresenceUpdateRefreshesCachedMemberAvatar(t *testing.T) {
	for _, trackPresences := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			avatar       string
			want         string
			offline      bool
			programmatic bool
		}{
			{name: "replace", avatar: `,"avatar":"new-avatar"`, want: "new-avatar"},
			{name: "clear", avatar: `,"avatar":null`},
			{name: "omit", want: "old-avatar"},
			{name: "clear_offline", avatar: `,"avatar":null`, offline: true},
			{name: "replace_offline", avatar: `,"avatar":"new-avatar"`, want: "new-avatar", offline: true},
			{name: "omit_offline", want: "old-avatar", offline: true},
			{name: "programmatic_zero", want: "old-avatar", programmatic: true},
		} {
			t.Run(fmt.Sprintf("presences=%t/%s", trackPresences, tc.name), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel, session.SyncEvents = -1, true
				// Presence events can arrive without the GUILD_MEMBERS intent.
				session.Identify.Intents = IntentsGuildPresences
				session.State.TrackPresences = trackPresences
				if err := session.State.GuildAdd(&Guild{ID: "100", Members: []*Member{{
					GuildID: "100", Nick: "guild-name", Roles: []string{"role"},
					User: &User{ID: "200", Username: "cached-name", Discriminator: "0", Avatar: "old-avatar"},
				}}}); err != nil {
					t.Fatal(err)
				}
				before, err := session.State.Guild("100")
				if err != nil {
					t.Fatal(err)
				}
				old := before.Members[0]
				called := false
				session.AddHandler(func(s *Session, update *PresenceUpdate) {
					called = true
					member, err := s.State.Member("100", "200")
					if err != nil {
						t.Error(err)
						return
					}
					if member.User.Avatar != tc.want {
						t.Errorf("member avatar = %q, want %q", member.User.Avatar, tc.want)
					}
					wantUser := &User{ID: "200", Discriminator: "0", Avatar: tc.want}
					if got := member.AvatarURL(""); got != wantUser.AvatarURL("") {
						t.Errorf("member avatar URL = %q, want %q", got, wantUser.AvatarURL(""))
					}
					if member.User.Username != "cached-name" || member.Nick != "guild-name" || len(member.Roles) != 1 || member.Roles[0] != "role" {
						t.Error("partial presence lost unrelated member fields")
					}
					guild, err := s.State.Guild("100")
					if err != nil || len(guild.Members) != 1 || guild.Members[0] != member {
						t.Errorf("guild and member cache disagree: %v", err)
					}
					if (!tc.offline || tc.avatar != "") && (member == old || member.User == old.User) {
						t.Error("presence update reused the earlier member snapshot")
					}
					if update.User.Username != "" {
						t.Error("cache merge modified the partial event user")
					}
				})
				status := StatusOnline
				if tc.offline {
					status = StatusOffline
				}
				if tc.programmatic {
					session.handleEvent(presenceUpdateEventType, &PresenceUpdate{GuildID: "100", Presence: Presence{User: &User{ID: "200"}, Status: status}})
				} else {
					payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200"%s},"status":%q,"activities":[],"client_status":{}}}`, tc.avatar, status)
					if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				if !called {
					t.Fatal("presence callback did not run")
				}
				if old.User.Avatar != "old-avatar" || before.Members[0] != old {
					t.Error("presence update modified an earlier guild/member snapshot")
				}
			})
		}
	}
}

func TestOfflinePresenceAvatarDoesNotAddMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200","avatar":"new-avatar"},"status":"offline","activities":[],"client_status":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.State.Member("100", "200"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("offline member lookup = %v, want not found", err)
	}
	guild, err := session.State.Guild("100")
	if err != nil || len(guild.Members) != 0 {
		t.Errorf("offline presence populated guild members: %v", err)
	}
}

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

func TestPresenceUpdateRefreshesCachedMemberDiscriminator(t *testing.T) {
	const userID = "175928847299117063"
	for _, tracking := range []struct {
		name      string
		presences bool
		members   bool
	}{
		{"both", true, true},
		{"presences_only", true, false},
		{"members_only", false, true},
		{"disabled", false, false},
	} {
		for _, status := range []Status{StatusOnline, StatusOffline} {
			for _, tc := range []struct {
				name          string
				field         string
				discriminator string
				tag           string
				avatarIndex   int
			}{
				{"legacy", `,"discriminator":"9876"`, "9876", "person#9876", 1},
				{"migrated", `,"discriminator":"0"`, "0", "person", 2},
				{"omitted", "", "1234", "person#1234", 4},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", tracking.name, status, tc.name), func(t *testing.T) {
					session, err := New("Bot test")
					if err != nil {
						t.Fatal(err)
					}
					session.LogLevel, session.SyncEvents = -1, true
					session.Identify.Intents = IntentsGuildPresences
					user := &User{ID: userID, Username: "person", Discriminator: "1234"}
					if err := session.State.GuildAdd(&Guild{ID: "100",
						Members:   []*Member{{GuildID: "100", User: user, Nick: "nickname", Roles: []string{"role"}}},
						Presences: []*Presence{{User: user, Status: StatusOnline}},
					}); err != nil {
						t.Fatal(err)
					}
					before, err := session.State.Guild("100")
					if err != nil {
						t.Fatal(err)
					}
					oldMember, oldPresence := before.Members[0], before.Presences[0]
					session.State.TrackPresences, session.State.TrackMembers = tracking.presences, tracking.members
					called := false
					session.AddHandler(func(s *Session, event *PresenceUpdate) {
						called = true
						member, err := s.State.Member("100", userID)
						if err != nil {
							t.Error(err)
							return
						}
						wantDiscriminator, wantTag, wantAvatar := "1234", "person#1234", 4
						if tracking.members {
							wantDiscriminator, wantTag, wantAvatar = tc.discriminator, tc.tag, tc.avatarIndex
						}
						if member.User.Discriminator != wantDiscriminator || member.User.String() != wantTag {
							t.Errorf("member discriminator/tag = %q/%q, want %q/%q", member.User.Discriminator, member.User.String(), wantDiscriminator, wantTag)
						}
						if got, want := member.AvatarURL(""), EndpointDefaultUserAvatar(wantAvatar); got != want {
							t.Errorf("member default avatar = %q, want %q", got, want)
						}
						presence, err := s.State.Presence("100", userID)
						if err != nil {
							t.Error(err)
							return
						}
						wantPresence := "1234"
						if tracking.presences {
							wantPresence = tc.discriminator
						}
						if presence.User.Discriminator != wantPresence {
							t.Errorf("presence discriminator = %q, want %q", presence.User.Discriminator, wantPresence)
						}
						guild, err := s.State.Guild("100")
						if err != nil || len(guild.Members) != 1 || guild.Members[0] != member {
							t.Errorf("guild/member indexes disagree: %v", err)
						}
						if member.Nick != "nickname" || len(member.Roles) != 1 || member.Roles[0] != "role" || member.User.Username != "person" {
							t.Error("partial presence lost unrelated member fields")
						}
						if event.User.Username != "" || event.User.Avatar != "" {
							t.Error("cache merge changed the partial event user")
						}
					})
					payload := fmt.Sprintf(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":%q%s},"status":%q,"activities":[],"client_status":{}}}`, userID, tc.field, status)
					if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
					if !called {
						t.Fatal("presence callback did not run")
					}
					if oldMember.User.Discriminator != "1234" || oldPresence.User.Discriminator != "1234" || before.Members[0] != oldMember || before.Presences[0] != oldPresence {
						t.Error("presence update changed an earlier snapshot")
					}
				})
			}
		}
	}
}

func TestOfflinePresenceDiscriminatorDoesNotAddMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"PRESENCE_UPDATE","d":{"guild_id":"100","user":{"id":"200","discriminator":"0"},"status":"offline","activities":[],"client_status":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.State.Member("100", "200"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("offline member lookup = %v, want not found", err)
	}
	guild, err := session.State.Guild("100")
	if err != nil || len(guild.Members) != 0 {
		t.Errorf("offline presence populated guild members: %v", err)
	}
}

func TestReactionMemberPreservesGuildIdentity(t *testing.T) {
	for _, stateEnabled := range []bool{false, true} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("state=%t/gateway=%t", stateEnabled, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = stateEnabled
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				payload := []byte(`{"guild_id":"100","channel_id":"300","message_id":"400","user_id":"200","emoji":{"id":null,"name":"👍"},"burst":false,"type":0,
					"member":{"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}`)
				called := false
				session.AddHandler(func(_ *Session, event *MessageReactionAdd) {
					called = true
					if event == nil || event.Member == nil || event.Member.User == nil {
						t.Error("callback did not receive the reaction member")
						return
					}
					member := event.Member
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					if err := session.State.MemberAdd(member); err != nil {
						t.Errorf("caching reaction member failed: %v", err)
					} else if cached, err := session.State.Member("100", "200"); err != nil || cached.User.ID != "200" {
						t.Errorf("member not retrievable from its guild: %v", err)
					}
				})
				if gateway {
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"MESSAGE_REACTION_ADD","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else {
					var event MessageReactionAdd
					if err := json.Unmarshal(payload, &event); err != nil {
						t.Fatal(err)
					}
					session.handleEvent(messageReactionAddEventType, &event)
				}
				if !called {
					t.Fatal("reaction callback did not run")
				}
			})
		}
	}
}

func TestReactionMemberGuildIdentityOptionalData(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	var received *MessageReactionAdd
	session.AddHandler(func(_ *Session, event *MessageReactionAdd) { received = event })
	for _, payload := range []string{
		`{"channel_id":"300","message_id":"400","user_id":"200","emoji":{"id":null,"name":"👍"},"burst":false,"type":0}`,
		`{"guild_id":"100","channel_id":"300","message_id":"400","user_id":"200","emoji":{"id":null,"name":"👍"},"burst":false,"type":0,"member":null}`,
	} {
		received = nil
		if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"MESSAGE_REACTION_ADD","d":%s}`, payload))); err != nil {
			t.Fatal(err)
		}
		if received == nil || received.Member != nil {
			t.Fatal("omitted/null reaction member was not preserved")
		}
	}
}

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

func TestScheduledEventUserMembersPreserveIdentity(t *testing.T) {
	for _, exception := range []bool{false, true} {
		for _, embeddedUser := range []bool{false, true} {
			t.Run(fmt.Sprintf("exception_%t/embedded_user_%t", exception, embeddedUser), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				userField := ""
				if embeddedUser {
					userField = `"user":{"id":"200","username":"embedded","discriminator":"0"},`
				}
				body := fmt.Sprintf(`[{"guild_scheduled_event_id":"300","user":{"id":"200","username":"subscriber","discriminator":"0"},"member":{%s"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}]`, userField)
				endpoint := EndpointGuildScheduledEventUsers("100", "300")
				if exception {
					endpoint = EndpointGuildScheduledEventExceptionUsers("100", "300", "400")
				}
				session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodGet || req.URL.String() != endpoint+"?with_member=true" {
						t.Errorf("request = %s %s, want subscriber GET with members", req.Method, req.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})
				fetch := func() ([]*GuildScheduledEventUser, error) {
					if exception {
						return session.GuildScheduledEventExceptionUsers("100", "300", "400", 0, true, "", "")
					}
					return session.GuildScheduledEventUsers("100", "300", 0, true, "", "")
				}
				users, err := fetch()
				if err != nil || len(users) != 1 || users[0] == nil || users[0].Member == nil {
					t.Fatalf("subscriber response = %#v, %v", users, err)
				}
				member := users[0].Member
				if member.GuildID != "100" {
					t.Errorf("member guild = %q, want 100", member.GuildID)
				}
				if !embeddedUser && member.User != users[0].User {
					t.Error("member did not link the subscriber user")
				} else if embeddedUser && (member.User == nil || member.User.Username != "embedded") {
					t.Error("member's existing user was replaced")
				}
				if got := member.Mention(); got != "<@!200>" {
					t.Errorf("Mention = %q, want <@!200>", got)
				}
				if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
					t.Errorf("AvatarURL = %q, want %q", got, want)
				}
				if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
					t.Errorf("BannerURL = %q, want %q", got, want)
				}
				if err := session.State.MemberAdd(member); err != nil {
					t.Errorf("caching subscriber member failed: %v", err)
				} else if cached, err := session.State.Member("100", "200"); err != nil || cached.User.ID != "200" {
					t.Errorf("member not retrievable from its guild: %v", err)
				}

				// Membership is optional even when requested; do not invent it.
				body = `[{"guild_scheduled_event_id":"300","user":{"id":"200"}}]`
				if users, err = fetch(); err != nil || len(users) != 1 || users[0].Member != nil {
					t.Fatalf("omitted member response = %#v, %v", users, err)
				}
				// Preserve previously accepted null responses and entries safely.
				body = `null`
				if users, err = fetch(); err != nil || users != nil {
					t.Fatalf("null response = %#v, %v", users, err)
				}
				body = `[null]`
				if users, err = fetch(); err != nil || len(users) != 1 || users[0] != nil {
					t.Fatalf("null entry response = %#v, %v", users, err)
				}
			})
		}
	}
}

func TestThreadArchiveEvictsCachedState(t *testing.T) {
	for _, trackChannels := range []bool{true, false} {
		t.Run(fmt.Sprintf("trackChannels=%t", trackChannels), func(t *testing.T) {
			session, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			state := session.State
			state.TrackChannels = trackChannels
			state.MaxMessageCount = 2
			parent := &Channel{ID: "150", GuildID: "100", Type: ChannelTypeGuildText}
			active := &Channel{ID: "199", GuildID: "100", ParentID: parent.ID, Type: ChannelTypeGuildPublicThread, Name: "active"}
			if err := state.GuildAdd(&Guild{ID: "100", Channels: []*Channel{parent}, Threads: []*Channel{active}}); err != nil {
				t.Fatal(err)
			}
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			threadData := func(id, name string, archived bool) string {
				return fmt.Sprintf(`{"id":%q,"guild_id":"100","parent_id":"150","owner_id":"300","type":11,"name":%q,"thread_metadata":{"archived":%t,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false}}`, id, name, archived)
			}
			var snapshot *Channel
			archiveCallbacks, cachedDuringCallbacks := 0, 0
			session.AddHandler(func(_ *Session, event *ThreadUpdate) {
				if !event.ThreadMetadata.Archived {
					return
				}
				archiveCallbacks++
				if _, err := state.Channel(event.ID); err == nil {
					cachedDuringCallbacks++
				}
				if event.ID == "900" {
					if event.BeforeUpdate != nil {
						t.Error("unknown archived thread had a previous snapshot")
					}
					return
				}
				if event.BeforeUpdate == nil || event.BeforeUpdate.ThreadMetadata.Archived || len(event.BeforeUpdate.Messages) != 1 {
					t.Error("archive callback lost the previous active thread and its message")
				}
				if event.ID == "200" {
					snapshot = event.BeforeUpdate
				}
			})
			for i := 0; i < 16; i++ {
				id := fmt.Sprint(200 + i)
				dispatch("THREAD_CREATE", threadData(id, "created", false))
				dispatch("MESSAGE_CREATE", fmt.Sprintf(`{"id":%q,"channel_id":%q,"guild_id":"100","content":"cached message","author":{"id":"300"}}`, "message-"+id, id))
				dispatch("THREAD_UPDATE", threadData(id, "archived", true))
			}
			// An update for an uncached archived thread must not add a cache entry.
			dispatch("THREAD_UPDATE", threadData("900", "already archived", true))
			guild, err := state.Guild("100")
			if err != nil {
				t.Fatal(err)
			}
			if len(guild.Threads) != 1 || guild.Threads[0] != active {
				t.Errorf("cache retains %d threads after 16 archive cycles and an uncached archive update, want only the active thread", len(guild.Threads))
			}
			wantChannels := 1
			if trackChannels {
				wantChannels++
			}
			if len(state.channelMap) != wantChannels {
				t.Errorf("channel index retains %d entries, want %d", len(state.channelMap), wantChannels)
			}
			if archiveCallbacks != 17 || cachedDuringCallbacks != 0 {
				t.Errorf("archive callbacks=%d, callbacks with retained cache=%d", archiveCallbacks, cachedDuringCallbacks)
			}
			if _, err := state.Message("200", "message-200"); !errors.Is(err, ErrStateNotFound) {
				t.Errorf("archived message lookup error = %v, want ErrStateNotFound", err)
			}
			if snapshot == nil || snapshot.Name != "created" || snapshot.ThreadMetadata.Archived || len(snapshot.Messages) != 1 {
				t.Fatal("previous snapshot was mutated by archiving")
			}
			// Active updates still refresh the cache, and unarchiving can re-add a thread.
			dispatch("THREAD_UPDATE", threadData("199", "renamed", false))
			dispatch("THREAD_CREATE", threadData("200", "reopened", false))
			current, err := state.Channel("199")
			if err != nil || current.Name != "renamed" || active.Name != "active" {
				t.Fatalf("active update did not preserve snapshot semantics: %v", err)
			}
			reopened, err := state.Channel("200")
			if err != nil || reopened.Name != "reopened" || len(reopened.Messages) != 0 {
				t.Fatalf("reopened thread retained archived messages: %v", err)
			}
		})
	}
}

func TestAddedThreadMemberPreservesGuildIdentity(t *testing.T) {
	for _, tracking := range []string{"enabled", "state_disabled", "members_disabled"} {
		for _, gateway := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/gateway=%t", tracking, gateway), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.StateEnabled = tracking != "state_disabled"
				session.State.TrackMembers = tracking != "members_disabled"
				if err := session.State.GuildAdd(&Guild{ID: "100", Threads: []*Channel{{
					ID: "300", GuildID: "100", Type: ChannelTypeGuildPublicThread,
				}}}); err != nil {
					t.Fatal(err)
				}
				cache := NewState()
				if err := cache.GuildAdd(&Guild{ID: "100"}); err != nil {
					t.Fatal(err)
				}
				payload := []byte(`{"id":"300","guild_id":"100","member_count":1,"added_members":[{
					"id":"300","user_id":"200","join_timestamp":"2026-10-30T10:00:00Z","flags":0,"presence":null,
					"member":{"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}]}`)
				called := false
				session.AddHandler(func(_ *Session, event *ThreadMembersUpdate) {
					called = true
					if len(event.AddedMembers) != 1 || event.AddedMembers[0].Member == nil {
						t.Error("callback did not receive the added guild member")
						return
					}
					member := event.AddedMembers[0].Member
					if member.GuildID != "100" {
						t.Errorf("member guild = %q, want 100", member.GuildID)
					}
					if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
						t.Errorf("AvatarURL = %q, want %q", got, want)
					}
					if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
						t.Errorf("BannerURL = %q, want %q", got, want)
					}
					// A callback can populate a separate cache regardless of session tracking.
					if err := cache.MemberAdd(member); err != nil {
						t.Errorf("caching callback member: %v", err)
					} else if cached, err := cache.Member("100", "200"); err != nil || cached.GuildID != "100" {
						t.Errorf("retrieving callback member: %v", err)
					}
				})
				if gateway {
					if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":"THREAD_MEMBERS_UPDATE","d":%s}`, payload))); err != nil {
						t.Fatal(err)
					}
				} else {
					var event ThreadMembersUpdate
					if err := json.Unmarshal(payload, &event); err != nil {
						t.Fatal(err)
					}
					session.handleEvent(threadMembersUpdateEventType, &event)
				}
				if !called {
					t.Fatal("thread member callback did not run")
				}
			})
		}
	}
}

func TestAddedThreadMemberGuildIdentityAbsentData(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	var received *ThreadMembersUpdate
	session.AddHandler(func(_ *Session, event *ThreadMembersUpdate) { received = event })
	for _, added := range []string{`[]`, `[{"id":"300","user_id":"200"}]`, `[{"id":"300","user_id":"200","member":null}]`} {
		received = nil
		payload := fmt.Sprintf(`{"op":0,"s":1,"t":"THREAD_MEMBERS_UPDATE","d":{"id":"300","guild_id":"100","member_count":1,"added_members":%s}}`, added)
		if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if received == nil {
			t.Fatal("callback did not run")
		}
		for _, member := range received.AddedMembers {
			if member.Member != nil {
				t.Fatal("absent member was not preserved")
			}
		}
	}
}

func TestThreadMembersUpdateClearsCurrentMember(t *testing.T) {
	for _, source := range []string{"THREAD_CREATE", "GUILD_CREATE"} {
		t.Run(source, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			dispatch("READY", `{"v":10,"user":{"id":"300","bot":true},"guilds":[],"session_id":"test","resume_gateway_url":"wss://gateway.discord.gg"}`)
			member := `{"join_timestamp":"2026-10-30T10:00:00Z","flags":1}`
			if source == "THREAD_CREATE" {
				member = `{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1}`
			}
			thread := `{"id":"200","guild_id":"100","parent_id":"150","type":11,"name":"public-thread","member_count":2,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false},"member":` + member + `}`
			if source == "GUILD_CREATE" {
				// Discord omits both membership IDs in a GUILD_CREATE thread.
				dispatch(source, `{"id":"100","threads":[`+thread+`]}`)
			} else {
				dispatch("GUILD_CREATE", `{"id":"100","threads":[]}`)
				dispatch(source, thread)
			}
			before, err := session.State.Channel("200")
			if err != nil || before.Member == nil {
				t.Fatalf("initial membership missing: %v", err)
			}
			if source == "GUILD_CREATE" && before.Member.UserID != "" {
				t.Fatal("fixture unexpectedly supplied the omitted user ID")
			}
			callbacks := 0
			session.AddHandler(func(s *Session, event *ThreadMembersUpdate) {
				callbacks++
				cached, err := s.State.Channel(event.ID)
				if err != nil {
					t.Fatal(err)
				}
				if event.RemovedMembers[0] == "300" && cached.Member != nil {
					t.Error("removal callback still sees the current user as a thread member")
				}
			})
			dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":1,"removed_member_ids":["400"]}`)
			otherRemoved, err := session.State.Channel("200")
			if err != nil || otherRemoved.Member != before.Member {
				t.Fatalf("another user's removal changed current membership: %v", err)
			}
			dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":0,"removed_member_ids":["300"]}`)
			after, err := session.State.Channel("200")
			if err != nil {
				t.Fatal(err)
			}
			if after.Member != nil || after.MemberCount != 0 {
				t.Errorf("current membership survived removal: member=%+v count=%d", after.Member, after.MemberCount)
			}
			if before.Member == nil || otherRemoved.Member != before.Member || before.MemberCount != 2 || otherRemoved.MemberCount != 1 {
				t.Error("removal modified an earlier thread snapshot")
			}
			guild, err := session.State.Guild("100")
			if err != nil || len(guild.Threads) != 1 || guild.Threads[0] != after {
				t.Fatalf("guild thread cache not updated: %v", err)
			}
			if callbacks != 2 {
				t.Fatalf("removal callbacks = %d, want 2", callbacks)
			}
		})
	}
}

func TestThreadCreateHonorsMemberTracking(t *testing.T) {
	for _, track := range []bool{false, true} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("tracking=%t/cached=%t", track, existing), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.SyncEvents = true
				session.State.TrackThreadMembers = track
				guild := &Guild{ID: "100"}
				var before *Channel
				if existing {
					// THREAD_CREATE also arrives when joining a known thread.
					before = &Channel{ID: "200", GuildID: "100", ParentID: "150", Type: ChannelTypeGuildPublicThread, Name: "public-thread"}
					guild.Threads = []*Channel{before}
				}
				if err := session.State.GuildAdd(guild); err != nil {
					t.Fatal(err)
				}
				if existing {
					before, err = session.State.Channel("200")
					if err != nil {
						t.Fatal(err)
					}
				}
				callbacks := 0
				session.AddHandler(func(s *Session, event *ThreadCreate) {
					callbacks++
					checkCurrentThreadMember(t, event.Channel)
					cached, err := s.State.Channel(event.ID)
					if err != nil {
						t.Fatal(err)
					}
					if track {
						checkCurrentThreadMember(t, cached)
					} else if cached.Member != nil || cached.Members != nil {
						t.Error("callback sees cached thread membership with tracking disabled")
					}
				})
				thread := `{"id":"200","guild_id":"100","parent_id":"150","type":11,"name":"public-thread","thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false},"member":{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1}}`
				if _, err := session.onEvent(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"THREAD_CREATE","d":`+thread+`}`)); err != nil {
					t.Fatal(err)
				}
				cached, err := session.State.Channel("200")
				if err != nil {
					t.Fatal(err)
				}
				cachedGuild, err := session.State.Guild("100")
				if err != nil || len(cachedGuild.Threads) != 1 || cachedGuild.Threads[0] != cached {
					t.Fatalf("guild thread cache disagrees: %v", err)
				}
				if before != nil && (cached == before || before.Member != nil || before.Members != nil) {
					t.Error("thread create changed the previous snapshot")
				}
				if callbacks != 1 {
					t.Fatalf("thread callbacks = %d, want 1", callbacks)
				}
			})
		}
	}
}

func TestThreadPinsUpdateWithoutChannelTracking(t *testing.T) {
	for _, channelType := range []ChannelType{ChannelTypeGuildNewsThread, ChannelTypeGuildPublicThread, ChannelTypeGuildPrivateThread} {
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			session.State.TrackChannels = false
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			sequence := 0
			dispatch := func(eventType, data string) {
				t.Helper()
				sequence++
				payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
				if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			dispatch("THREAD_CREATE", fmt.Sprintf(`{"id":"200","guild_id":"100","parent_id":"150","owner_id":"300","type":%d,"name":"thread","last_pin_timestamp":"2026-10-30T10:00:00Z","thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false}}`, channelType))
			var callbackChannel *Channel
			session.AddHandler(func(s *Session, event *ChannelPinsUpdate) {
				callbackChannel, err = s.State.Channel(event.ChannelID)
				if err != nil {
					t.Error(err)
				}
			})
			for _, timestamp := range []string{"2026-10-30T11:00:00Z", ""} {
				before, err := session.State.Channel("200")
				if err != nil {
					t.Fatal(err)
				}
				if before.LastPinTimestamp == nil {
					t.Fatal("expected an existing pin timestamp before the update")
				}
				previousTimestamp := *before.LastPinTimestamp
				value := "null"
				var expected *time.Time
				if timestamp != "" {
					parsed, err := time.Parse(time.RFC3339, timestamp)
					if err != nil {
						t.Fatal(err)
					}
					expected = &parsed
					value = fmt.Sprintf("%q", timestamp)
				}
				dispatch("CHANNEL_PINS_UPDATE", fmt.Sprintf(`{"guild_id":"100","channel_id":"200","last_pin_timestamp":%s}`, value))
				after, err := session.State.Channel("200")
				if err != nil {
					t.Fatal(err)
				}
				if (expected == nil && after.LastPinTimestamp != nil) || (expected != nil && (after.LastPinTimestamp == nil || !after.LastPinTimestamp.Equal(*expected))) {
					t.Errorf("thread pin timestamp = %v, want %v", after.LastPinTimestamp, expected)
				}
				guild, err := session.State.Guild("100")
				if err != nil {
					t.Fatal(err)
				}
				if len(guild.Threads) != 1 || guild.Threads[0] != after || callbackChannel != after || after == before {
					t.Error("thread pin update did not replace both cache indexes before its callback")
				}
				if before.LastPinTimestamp == nil || !before.LastPinTimestamp.Equal(previousTimestamp) || len(guild.Channels) != 0 {
					t.Error("pin update changed the previous snapshot or cached an ordinary channel")
				}
			}
		})
	}
}

func TestThreadPinsUpdatePreservesDisabledTracking(t *testing.T) {
	for _, channelType := range []ChannelType{ChannelTypeGuildText, ChannelTypeGuildPublicThread} {
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{ID: "guild"}); err != nil {
				t.Fatal(err)
			}
			channel := &Channel{ID: "channel", GuildID: "guild", Type: channelType}
			if err := state.ChannelAdd(channel); err != nil {
				t.Fatal(err)
			}
			state.TrackChannels = false
			state.TrackThreads = !channel.IsThread()
			if err := state.OnInterface(&Session{StateEnabled: true}, &ChannelPinsUpdate{ChannelID: "channel", LastPinTimestamp: "2026-10-30T11:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			after, err := state.Channel("channel")
			if err != nil || after != channel || after.LastPinTimestamp != nil {
				t.Errorf("pin update changed an untracked channel: %v", err)
			}
		})
	}
}

func TestThreadUpdatePreservesCachedMembership(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.SyncEvents = true
	if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
		t.Fatal(err)
	}
	sequence := 0
	dispatch := func(eventType, data string) {
		t.Helper()
		sequence++
		payload := fmt.Sprintf(`{"op":0,"s":%d,"t":%q,"d":%s}`, sequence, eventType, data)
		if _, err := session.onEvent(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	dispatch("THREAD_CREATE", channelWithThreadMember)
	dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":2,"added_members":[{"id":"200","user_id":"300","join_timestamp":"2026-10-30T10:00:00Z","flags":1,"member":{"user":{"id":"300"},"roles":[],"joined_at":"2026-10-30T09:00:00Z","deaf":false,"mute":false},"presence":null},{"id":"200","user_id":"400","join_timestamp":"2026-10-30T10:01:00Z","flags":0,"member":{"user":{"id":"400"},"roles":[],"joined_at":"2026-10-30T09:00:00Z","deaf":false,"mute":false},"presence":null}]}`)
	before, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	if before.Member == nil || len(before.Members) != 2 {
		t.Fatal("membership setup failed")
	}
	var received *ThreadUpdate
	session.AddHandler(func(_ *Session, event *ThreadUpdate) { received = event })
	dispatch("THREAD_UPDATE", `{"id":"200","guild_id":"100","parent_id":"150","owner_id":"300","type":12,"name":"renamed","member_count":2,"thread_metadata":{"archived":false,"auto_archive_duration":60,"archive_timestamp":"2026-10-30T10:00:00Z","locked":false,"invitable":true}}`)
	after, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	if after.Member != before.Member {
		t.Error("thread rename discarded the current user's membership")
	}
	if len(after.Members) != 2 || after.Members[0] != before.Members[0] || after.Members[1] != before.Members[1] {
		t.Error("thread rename discarded tracked members")
	}
	if after == before || after.Name != "renamed" || before.Name != "private-thread" {
		t.Fatal("thread rename did not create an updated cache snapshot")
	}
	if received == nil || received.BeforeUpdate == nil || received.BeforeUpdate.Member != before.Member || len(received.BeforeUpdate.Members) != 2 {
		t.Fatal("thread update lost the previous membership snapshot")
	}
	if received.Member != nil || received.Members != nil {
		t.Fatal("cache preservation modified the incoming event")
	}
	guild, err := session.State.Guild("100")
	if err != nil || len(guild.Threads) != 1 || guild.Threads[0] != after {
		t.Fatalf("guild thread cache not updated: %v", err)
	}

	// Later membership changes must still apply without changing old snapshots.
	membersAfterRename := len(after.Members)
	dispatch("THREAD_MEMBERS_UPDATE", `{"id":"200","guild_id":"100","member_count":1,"removed_member_ids":["400"]}`)
	current, err := session.State.Channel("200")
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Members) != 1 || current.Members[0].UserID != "300" {
		t.Error("membership removal lost the remaining tracked member")
	}
	if len(after.Members) != membersAfterRename || len(before.Members) != 2 || before.Members[1].UserID != "400" {
		t.Error("membership removal changed an earlier cache snapshot")
	}
}

func TestThreadChannelUpdateDoesNotRestoreReplacedMembership(t *testing.T) {
	for _, name := range []string{"tracking disabled", "obfuscated", "explicit replacement"} {
		t.Run(name, func(t *testing.T) {
			state := NewState()
			if err := state.GuildAdd(&Guild{ID: "guild", Threads: []*Channel{{
				ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread,
				Member: &ThreadMember{UserID: "self"}, Members: []*ThreadMember{{UserID: "other"}},
			}}}); err != nil {
				t.Fatal(err)
			}
			update := &Channel{ID: "thread", GuildID: "guild", Type: ChannelTypeGuildPublicThread}
			switch name {
			case "tracking disabled":
				state.TrackThreadMembers = false
			case "obfuscated":
				update.Flags = ChannelFlagObfuscated
			case "explicit replacement":
				update.Member = &ThreadMember{UserID: "self", Flags: 7}
				update.Members = []*ThreadMember{}
			}
			if err := state.ChannelAdd(update); err != nil {
				t.Fatal(err)
			}
			cached, err := state.Channel("thread")
			if err != nil {
				t.Fatal(err)
			}
			if cached.Member != update.Member || len(cached.Members) != 0 {
				t.Fatal("channel update restored obsolete membership")
			}
		})
	}
}

func TestVoiceStateMembersPreserveGuildIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		event        string
		userID       string
		stateEnabled bool
	}{
		{name: "REST user", userID: "200", stateEnabled: true},
		{name: "REST current user", userID: "@me", stateEnabled: true},
		{name: "voice update", event: "VOICE_STATE_UPDATE", stateEnabled: true},
		{name: "voice update without state", event: "VOICE_STATE_UPDATE"},
		{name: "guild create", event: "GUILD_CREATE", stateEnabled: true},
		{name: "guild create without state", event: "GUILD_CREATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.SyncEvents = true
			session.StateEnabled = tc.stateEnabled
			session.State.User = &User{ID: "bot"}
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			payload := `{"channel_id":"300","user_id":"200","session_id":"voice-session","deaf":false,"mute":false,"self_deaf":false,"self_mute":false,"self_video":false,"suppress":false,"request_to_speak_timestamp":null,
				"member":{"user":{"id":"200","username":"member","discriminator":"0"},"avatar":"guild-avatar","banner":"guild-banner","roles":[],"joined_at":"2026-10-30T10:00:00Z","deaf":false,"mute":false,"flags":0}}`
			var voice *VoiceState
			if tc.event == "" {
				session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodGet || req.URL.String() != EndpointGuildMemberVoiceState("100", tc.userID) {
						t.Errorf("request = %s %s, want voice-state GET", req.Method, req.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				voice, err = session.UserVoiceState("100", tc.userID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				capture := func(state *VoiceState) {
					voice = state
					if state == nil || state.Member == nil || state.Member.GuildID != "100" {
						t.Error("callback received a member without guild identity")
					}
					if tc.stateEnabled {
						cached, err := session.State.VoiceState("100", "200")
						if err != nil || cached != state {
							t.Errorf("voice state not cached before callback: %v", err)
						}
					}
				}
				switch tc.event {
				case "VOICE_STATE_UPDATE":
					payload = `{"guild_id":"100",` + payload[1:]
					session.AddHandler(func(_ *Session, event *VoiceStateUpdate) { capture(event.VoiceState) })
				case "GUILD_CREATE":
					payload = `{"id":"100","voice_states":[` + payload + `]}`
					session.AddHandler(func(_ *Session, event *GuildCreate) { capture(event.VoiceStates[0]) })
				}
				if _, err := session.onEvent(websocket.TextMessage, []byte(fmt.Sprintf(`{"op":0,"s":1,"t":%q,"d":%s}`, tc.event, payload))); err != nil {
					t.Fatal(err)
				}
			}
			if voice == nil || voice.Member == nil || voice.Member.User == nil {
				t.Fatal("voice state or member missing")
			}
			member := voice.Member
			if voice.GuildID != "100" || member.GuildID != "100" {
				t.Errorf("guild identity = voice %q, member %q, want 100", voice.GuildID, member.GuildID)
			}
			if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("100", "200", "guild-avatar"); got != want {
				t.Errorf("AvatarURL = %q, want %q", got, want)
			}
			if got, want := member.BannerURL(""), EndpointGuildMemberBanner("100", "200", "guild-banner"); got != want {
				t.Errorf("BannerURL = %q, want %q", got, want)
			}
			if err := session.State.MemberAdd(member); err != nil {
				t.Errorf("caching voice member failed: %v", err)
			} else if cached, err := session.State.Member("100", "200"); err != nil || cached.User.ID != "200" {
				t.Errorf("voice member not retrievable from its guild: %v", err)
			}
		})
	}
}

func TestUserVoiceStateOptionalMember(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"user_id":"200","channel_id":null}`, `null`} {
		session.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
		})
		voice, err := session.UserVoiceState("100", "200")
		if err != nil {
			t.Fatal(err)
		}
		if payload == "null" {
			if voice != nil {
				t.Fatal("null response produced a voice state")
			}
		} else if voice == nil || voice.Member != nil {
			t.Fatal("omitted member was not preserved")
		}
	}
}
