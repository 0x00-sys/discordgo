package discordgo

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
		PermissionUseExternalSounds |
		PermissionSetVoiceChannelStatus
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
