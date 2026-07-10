// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains code related to state tracking.  If enabled, state
// tracking will capture the initial READY packet and many other websocket
// events and maintain an in-memory state of guilds, channels, users, and
// so forth.  This information can be accessed through the Session.State struct.

package discordgo

import (
	"errors"
	"sort"
	"sync"
)

// ErrNilState is returned when the state is nil.
var ErrNilState = errors.New("state not instantiated, please use discordgo.New() or assign Session.State")

// ErrStateNotFound is returned when the state cache
// requested is not found
var ErrStateNotFound = errors.New("state cache not found")

// ErrStateInvalidData is returned when an event contains data that cannot be
// safely tracked in the state cache.
var ErrStateInvalidData = errors.New("state cache received invalid data")

// ErrMessageIncompletePermissions is returned when the message
// requested for permissions does not contain enough data to
// generate the permissions.
var ErrMessageIncompletePermissions = errors.New("message incomplete, unable to determine permissions")

// A State contains the current known state.
// As discord sends this in a READY blob, it seems reasonable to simply
// use that struct as the data store.
type State struct {
	sync.RWMutex
	Ready

	// MaxMessageCount represents how many messages per channel the state will store.
	MaxMessageCount    int
	TrackChannels      bool
	TrackThreads       bool
	TrackEmojis        bool
	TrackStickers      bool
	TrackMembers       bool
	TrackThreadMembers bool
	TrackRoles         bool
	TrackVoice         bool
	TrackPresences     bool

	guildMap   map[string]*Guild
	channelMap map[string]*Channel
	memberMap  map[string]map[string]*Member
}

// NewState creates an empty state.
func NewState() *State {
	return &State{
		Ready: Ready{
			PrivateChannels: []*Channel{},
			Guilds:          []*Guild{},
		},
		TrackChannels:      true,
		TrackThreads:       true,
		TrackEmojis:        true,
		TrackStickers:      true,
		TrackMembers:       true,
		TrackThreadMembers: true,
		TrackRoles:         true,
		TrackVoice:         true,
		TrackPresences:     true,
		guildMap:           make(map[string]*Guild),
		channelMap:         make(map[string]*Channel),
		memberMap:          make(map[string]map[string]*Member),
	}
}

func (s *State) createMemberMap(guild *Guild) {
	members := make(map[string]*Member)
	for _, m := range guild.Members {
		if m == nil || m.User == nil {
			continue
		}
		members[m.User.ID] = m
	}
	s.memberMap[guild.ID] = members
}

func (s *State) guildForTracking(guild *Guild) *Guild {
	if guild == nil {
		return nil
	}

	tracked := *guild
	if !s.TrackRoles {
		tracked.Roles = nil
	}
	if !s.TrackEmojis {
		tracked.Emojis = nil
	}
	if !s.TrackStickers {
		tracked.Stickers = nil
	}
	if !s.TrackMembers {
		tracked.Members = nil
	}
	if !s.TrackPresences {
		tracked.Presences = nil
	}
	if !s.TrackChannels {
		tracked.Channels = nil
	}
	if !s.TrackThreads {
		tracked.Threads = nil
	} else if !s.TrackThreadMembers {
		tracked.Threads = threadsWithoutMembers(tracked.Threads)
	}
	if !s.TrackVoice {
		tracked.VoiceStates = nil
	}

	return &tracked
}

func threadsWithoutMembers(threads []*Channel) []*Channel {
	if len(threads) == 0 {
		return threads
	}

	copied := make([]*Channel, len(threads))
	for i, thread := range threads {
		copied[i] = threadWithoutMembers(thread)
	}

	return copied
}

func threadWithoutMembers(thread *Channel) *Channel {
	if thread == nil {
		return nil
	}

	threadCopy := *thread
	threadCopy.Member = nil
	threadCopy.Members = nil
	return &threadCopy
}

func copyMember(member *Member) *Member {
	memberCopy := *member
	if member.User != nil {
		userCopy := *member.User
		memberCopy.User = &userCopy
	}
	return &memberCopy
}

func copyPresence(presence *Presence) *Presence {
	presenceCopy := *presence
	if presence.User != nil {
		userCopy := *presence.User
		presenceCopy.User = &userCopy
	}
	return &presenceCopy
}

func copyChannel(channel *Channel) *Channel {
	channelCopy := *channel
	channelCopy.Recipients = append([]*User(nil), channel.Recipients...)
	channelCopy.Messages = append([]*Message(nil), channel.Messages...)
	channelCopy.PermissionOverwrites = append([]*PermissionOverwrite(nil), channel.PermissionOverwrites...)
	channelCopy.Members = append([]*ThreadMember(nil), channel.Members...)
	channelCopy.AvailableTags = append([]ForumTag(nil), channel.AvailableTags...)
	channelCopy.AppliedTags = append([]string(nil), channel.AppliedTags...)
	return &channelCopy
}

func copyGuild(guild *Guild) *Guild {
	guildCopy := *guild
	guildCopy.Roles = append([]*Role(nil), guild.Roles...)
	guildCopy.Emojis = append([]*Emoji(nil), guild.Emojis...)
	guildCopy.Stickers = append([]*Sticker(nil), guild.Stickers...)
	guildCopy.Members = append([]*Member(nil), guild.Members...)
	guildCopy.Presences = append([]*Presence(nil), guild.Presences...)
	guildCopy.Channels = append([]*Channel(nil), guild.Channels...)
	guildCopy.Threads = append([]*Channel(nil), guild.Threads...)
	guildCopy.VoiceStates = append([]*VoiceState(nil), guild.VoiceStates...)
	guildCopy.Features = append([]GuildFeature(nil), guild.Features...)
	guildCopy.StageInstances = append([]*StageInstance(nil), guild.StageInstances...)
	return &guildCopy
}

func (s *State) replaceGuild(oldGuild, newGuild *Guild) {
	s.guildMap[newGuild.ID] = newGuild
	for i, guild := range s.Guilds {
		if guild == oldGuild || (guild != nil && guild.ID == newGuild.ID) {
			s.Guilds[i] = newGuild
			return
		}
	}
	s.Guilds = append(s.Guilds, newGuild)
}

func (s *State) updateGuildMemberCount(guildID string, delta int) error {
	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	updated.MemberCount += delta
	s.replaceGuild(guild, updated)
	return nil
}

// GuildAdd adds a guild to the current world state, or
// updates it if it already exists.
func (s *State) GuildAdd(guild *Guild) error {
	if s == nil {
		return ErrNilState
	}
	guild = s.guildForTracking(guild)
	if guild == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// Update the channels to point to the right guild, adding them to the channelMap as we go
	for _, c := range guild.Channels {
		if c == nil {
			continue
		}
		s.channelMap[c.ID] = c
	}

	// Add all the threads to the state in case of thread sync list.
	for _, t := range guild.Threads {
		if t == nil {
			continue
		}
		s.channelMap[t.ID] = t
	}

	// If this guild contains a new member slice, we must regenerate the member map so the pointers stay valid
	if s.TrackMembers {
		if guild.Members != nil {
			s.createMemberMap(guild)
		} else if _, ok := s.memberMap[guild.ID]; !ok {
			// Even if we have no new member slice, we still initialize the member map for this guild if it doesn't exist
			s.memberMap[guild.ID] = make(map[string]*Member)
		}
	} else {
		delete(s.memberMap, guild.ID)
	}

	if g, ok := s.guildMap[guild.ID]; ok {
		// We are about to replace `g` in the state with `guild`, but first we need to
		// make sure we preserve any fields that the `guild` doesn't contain from `g`.
		if guild.MemberCount == 0 {
			guild.MemberCount = g.MemberCount
		}
		if guild.Roles == nil && s.TrackRoles {
			guild.Roles = append([]*Role(nil), g.Roles...)
		}
		if guild.Emojis == nil && s.TrackEmojis {
			guild.Emojis = append([]*Emoji(nil), g.Emojis...)
		}
		if guild.Stickers == nil && s.TrackStickers {
			guild.Stickers = append([]*Sticker(nil), g.Stickers...)
		}
		if guild.Members == nil && s.TrackMembers {
			guild.Members = append([]*Member(nil), g.Members...)
		}
		if guild.Presences == nil && s.TrackPresences {
			guild.Presences = append([]*Presence(nil), g.Presences...)
		}
		if guild.Channels == nil && s.TrackChannels {
			guild.Channels = append([]*Channel(nil), g.Channels...)
		}
		if guild.Threads == nil && s.TrackThreads {
			if s.TrackThreadMembers {
				guild.Threads = append([]*Channel(nil), g.Threads...)
			} else {
				guild.Threads = threadsWithoutMembers(g.Threads)
			}
		}
		if guild.VoiceStates == nil && s.TrackVoice {
			guild.VoiceStates = append([]*VoiceState(nil), g.VoiceStates...)
		}
		for _, c := range guild.Channels {
			if c != nil {
				s.channelMap[c.ID] = c
			}
		}
		for _, t := range guild.Threads {
			if t != nil {
				s.channelMap[t.ID] = t
			}
		}
		if !s.TrackChannels {
			for _, c := range g.Channels {
				if c != nil {
					delete(s.channelMap, c.ID)
				}
			}
		}
		if !s.TrackThreads {
			for _, t := range g.Threads {
				if t != nil {
					delete(s.channelMap, t.ID)
				}
			}
		}
		s.replaceGuild(g, guild)
		return nil
	}

	s.Guilds = append(s.Guilds, guild)
	s.guildMap[guild.ID] = guild

	return nil
}

// GuildRemove removes a guild from current world state.
func (s *State) GuildRemove(guild *Guild) error {
	if s == nil {
		return ErrNilState
	}

	if guild == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// Fetch the guild under the write lock; a pointer obtained earlier
	// could have been replaced by a concurrent update, leaking
	// channelMap entries for channels added in between.
	old, ok := s.guildMap[guild.ID]
	if !ok {
		return ErrStateNotFound
	}

	delete(s.guildMap, guild.ID)
	delete(s.memberMap, guild.ID)

	for _, channel := range old.Channels {
		if channel != nil {
			delete(s.channelMap, channel.ID)
		}
	}
	for _, thread := range old.Threads {
		if thread != nil {
			delete(s.channelMap, thread.ID)
		}
	}

	for i, g := range s.Guilds {
		if g != nil && g.ID == guild.ID {
			s.Guilds = append(s.Guilds[:i], s.Guilds[i+1:]...)
			return nil
		}
	}

	return nil
}

// Guild gets a guild by ID. This is useful for querying if @me is in a guild.
func (s *State) Guild(guildID string) (*Guild, error) {
	if s == nil {
		return nil, ErrNilState
	}

	s.RLock()
	defer s.RUnlock()

	if g, ok := s.guildMap[guildID]; ok {
		return g, nil
	}

	return nil, ErrStateNotFound
}

func (s *State) presenceAdd(guildID string, presence *Presence) error {
	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	if err := presenceAddToGuild(updated, presence); err != nil {
		return err
	}
	s.replaceGuild(guild, updated)
	return nil
}

// presenceAddToGuild adds or merges a presence into a guild that was
// already copied for replacement; it must not be called on a guild
// pointer that has been handed out to callers.
func presenceAddToGuild(guild *Guild, presence *Presence) error {
	if presence == nil || presence.User == nil || presence.User.ID == "" {
		return ErrStateInvalidData
	}

	for i, p := range guild.Presences {
		if p == nil || p.User == nil {
			continue
		}

		if p.User.ID == presence.User.ID {
			//guild.Presences[i] = presence

			updated := copyPresence(p)

			//Update status
			updated.Activities = presence.Activities
			if presence.Status != "" {
				updated.Status = presence.Status
			}
			if presence.ClientStatus.Desktop != "" {
				updated.ClientStatus.Desktop = presence.ClientStatus.Desktop
			}
			if presence.ClientStatus.Mobile != "" {
				updated.ClientStatus.Mobile = presence.ClientStatus.Mobile
			}
			if presence.ClientStatus.Web != "" {
				updated.ClientStatus.Web = presence.ClientStatus.Web
			}

			//Update the optionally sent user information
			//ID Is a mandatory field so you should not need to check if it is empty
			updated.User.ID = presence.User.ID

			if presence.User.Avatar != "" {
				updated.User.Avatar = presence.User.Avatar
			}
			if presence.User.Discriminator != "" {
				updated.User.Discriminator = presence.User.Discriminator
			}
			if presence.User.Email != "" {
				updated.User.Email = presence.User.Email
			}
			if presence.User.Token != "" {
				updated.User.Token = presence.User.Token
			}
			if presence.User.Username != "" {
				updated.User.Username = presence.User.Username
			}

			guild.Presences[i] = updated
			return nil
		}
	}

	guild.Presences = append(guild.Presences, copyPresence(presence))
	return nil
}

// PresenceAdd adds a presence to the current world state, or
// updates it if it already exists.
func (s *State) PresenceAdd(guildID string, presence *Presence) error {
	if s == nil {
		return ErrNilState
	}

	s.Lock()
	defer s.Unlock()

	return s.presenceAdd(guildID, presence)
}

// PresenceRemove removes a presence from the current world state.
func (s *State) PresenceRemove(guildID string, presence *Presence) error {
	if s == nil {
		return ErrNilState
	}

	if presence == nil || presence.User == nil || presence.User.ID == "" {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// Fetch the guild under the write lock; a pointer obtained earlier
	// could have been replaced by a concurrent update, losing this
	// removal in an orphaned snapshot.
	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	for i, p := range updated.Presences {
		if p == nil || p.User == nil {
			continue
		}

		if p.User.ID == presence.User.ID {
			updated.Presences = append(updated.Presences[:i], updated.Presences[i+1:]...)
			s.replaceGuild(guild, updated)
			return nil
		}
	}

	return ErrStateNotFound
}

// Presence gets a presence by ID from a guild.
func (s *State) Presence(guildID, userID string) (*Presence, error) {
	if s == nil {
		return nil, ErrNilState
	}

	s.RLock()
	defer s.RUnlock()

	guild, ok := s.guildMap[guildID]
	if !ok {
		return nil, ErrStateNotFound
	}

	for _, p := range guild.Presences {
		if p == nil || p.User == nil {
			continue
		}

		if p.User.ID == userID {
			return p, nil
		}
	}

	return nil, ErrStateNotFound
}

// TODO: Consider moving Guild state update methods onto *Guild.

func (s *State) memberAdd(member *Member) error {
	guild, ok := s.guildMap[member.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	members, ok := s.memberMap[member.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	memberAddToGuild(members, updated, copyMember(member))
	s.replaceGuild(guild, updated)
	return nil
}

// memberAddToGuild adds or replaces a member in the member map and in a
// guild that was already copied for replacement; it must not be called
// on a guild pointer that has been handed out to callers.
func memberAddToGuild(members map[string]*Member, guild *Guild, member *Member) {
	m, ok := members[member.User.ID]
	if !ok {
		members[member.User.ID] = member
		guild.Members = append(guild.Members, member)
		return
	}

	// We are about to replace `m` in the state with `member`, but first we need to
	// make sure we preserve any fields that the `member` doesn't contain from `m`.
	if member.JoinedAt.IsZero() {
		member.JoinedAt = m.JoinedAt
	}
	members[member.User.ID] = member
	for i, guildMember := range guild.Members {
		if guildMember == m || (guildMember != nil && guildMember.User != nil && guildMember.User.ID == member.User.ID) {
			guild.Members[i] = member
			return
		}
	}
	guild.Members = append(guild.Members, member)
}

// MemberAdd adds a member to the current world state, or
// updates it if it already exists.
func (s *State) MemberAdd(member *Member) error {
	if s == nil {
		return ErrNilState
	}
	if member == nil || member.User == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	return s.memberAdd(member)
}

// MemberRemove removes a member from current world state.
func (s *State) MemberRemove(member *Member) error {
	if s == nil {
		return ErrNilState
	}

	if member == nil || member.User == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// Fetch the guild under the write lock; a pointer obtained earlier
	// could have been replaced by a concurrent update, losing this
	// removal in an orphaned snapshot.
	guild, ok := s.guildMap[member.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	members, ok := s.memberMap[member.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	_, ok = members[member.User.ID]
	if !ok {
		return ErrStateNotFound
	}
	delete(members, member.User.ID)

	updated := copyGuild(guild)
	for i, m := range updated.Members {
		if m == nil || m.User == nil {
			continue
		}
		if m.User.ID == member.User.ID {
			updated.Members = append(updated.Members[:i], updated.Members[i+1:]...)
			s.replaceGuild(guild, updated)
			return nil
		}
	}

	return ErrStateNotFound
}

// Member gets a member by ID from a guild.
func (s *State) Member(guildID, userID string) (*Member, error) {
	if s == nil {
		return nil, ErrNilState
	}

	s.RLock()
	defer s.RUnlock()

	members, ok := s.memberMap[guildID]
	if !ok {
		return nil, ErrStateNotFound
	}

	m, ok := members[userID]
	if ok {
		return m, nil
	}

	return nil, ErrStateNotFound
}

// RoleAdd adds a role to the current world state, or
// updates it if it already exists.
func (s *State) RoleAdd(guildID string, role *Role) error {
	if s == nil {
		return ErrNilState
	}
	if role == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	roleCopy := *role
	role = &roleCopy
	updated := copyGuild(guild)
	for i, r := range guild.Roles {
		if r == nil {
			continue
		}
		if r.ID == role.ID {
			updated.Roles[i] = role
			s.replaceGuild(guild, updated)
			return nil
		}
	}

	updated.Roles = append(updated.Roles, role)
	s.replaceGuild(guild, updated)
	return nil
}

// RoleRemove removes a role from current world state by ID.
func (s *State) RoleRemove(guildID, roleID string) error {
	if s == nil {
		return ErrNilState
	}

	s.Lock()
	defer s.Unlock()

	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	for i, r := range guild.Roles {
		if r == nil {
			continue
		}
		if r.ID == roleID {
			updated.Roles = append(updated.Roles[:i], updated.Roles[i+1:]...)
			s.replaceGuild(guild, updated)
			return nil
		}
	}

	return ErrStateNotFound
}

// Role gets a role by ID from a guild.
func (s *State) Role(guildID, roleID string) (*Role, error) {
	if s == nil {
		return nil, ErrNilState
	}

	guild, err := s.Guild(guildID)
	if err != nil {
		return nil, err
	}

	s.RLock()
	defer s.RUnlock()

	for _, r := range guild.Roles {
		if r == nil {
			continue
		}
		if r.ID == roleID {
			return r, nil
		}
	}

	return nil, ErrStateNotFound
}

func (s *State) replaceChannel(oldChannel, newChannel *Channel) {
	for i, channel := range s.PrivateChannels {
		if channel == oldChannel || (channel != nil && channel.ID == newChannel.ID) {
			s.PrivateChannels[i] = newChannel
			return
		}
	}

	for _, guild := range s.Guilds {
		if guild == nil {
			continue
		}
		for i, channel := range guild.Channels {
			if channel == oldChannel || (channel != nil && channel.ID == newChannel.ID) {
				updated := copyGuild(guild)
				updated.Channels[i] = newChannel
				s.replaceGuild(guild, updated)
				return
			}
		}
		for i, thread := range guild.Threads {
			if thread == oldChannel || (thread != nil && thread.ID == newChannel.ID) {
				updated := copyGuild(guild)
				updated.Threads[i] = newChannel
				s.replaceGuild(guild, updated)
				return
			}
		}
	}
}

// ChannelAdd adds a channel to the current world state, or
// updates it if it already exists.
// Channels may exist either as PrivateChannels or inside
// a guild.
func (s *State) ChannelAdd(channel *Channel) error {
	if s == nil {
		return ErrNilState
	}
	if channel == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// If the channel exists, replace it
	if c, ok := s.channelMap[channel.ID]; ok {
		channelCopy := *channel
		if channelCopy.Messages == nil {
			channelCopy.Messages = c.Messages
		}
		if channelCopy.PermissionOverwrites == nil {
			channelCopy.PermissionOverwrites = c.PermissionOverwrites
		}
		if channelCopy.ThreadMetadata == nil {
			channelCopy.ThreadMetadata = c.ThreadMetadata
		}

		channel = &channelCopy
		s.channelMap[channel.ID] = channel
		s.replaceChannel(c, channel)
		return nil
	}

	if channel.Type == ChannelTypeDM || channel.Type == ChannelTypeGroupDM {
		s.PrivateChannels = append(s.PrivateChannels, channel)
		s.channelMap[channel.ID] = channel
		return nil
	}

	guild, ok := s.guildMap[channel.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	if channel.IsThread() {
		updated.Threads = append(updated.Threads, channel)
	} else {
		updated.Channels = append(updated.Channels, channel)
	}

	s.channelMap[channel.ID] = channel
	s.replaceGuild(guild, updated)

	return nil
}

// ChannelRemove removes a channel from current world state.
func (s *State) ChannelRemove(channel *Channel) error {
	if s == nil {
		return ErrNilState
	}
	if channel == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	cached, ok := s.channelMap[channel.ID]
	if !ok {
		return ErrStateNotFound
	}

	if cached.Type == ChannelTypeDM || cached.Type == ChannelTypeGroupDM {
		for i, c := range s.PrivateChannels {
			if c != nil && c.ID == channel.ID {
				s.PrivateChannels = append(s.PrivateChannels[:i], s.PrivateChannels[i+1:]...)
				break
			}
		}
		delete(s.channelMap, channel.ID)
		return nil
	}

	guild, ok := s.guildMap[cached.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	if cached.IsThread() {
		for i, t := range guild.Threads {
			if t != nil && t.ID == channel.ID {
				updated.Threads = append(updated.Threads[:i], updated.Threads[i+1:]...)
				break
			}
		}
	} else {
		for i, c := range guild.Channels {
			if c != nil && c.ID == channel.ID {
				updated.Channels = append(updated.Channels[:i], updated.Channels[i+1:]...)
				break
			}
		}
	}

	delete(s.channelMap, channel.ID)
	s.replaceGuild(guild, updated)

	return nil
}

func replaceThreadInGuild(guild *Guild, oldThread, newThread *Channel) bool {
	for i, thread := range guild.Threads {
		if thread == oldThread || (thread != nil && thread.ID == newThread.ID) {
			guild.Threads[i] = newThread
			return true
		}
	}
	return false
}

// ThreadListSync syncs guild threads with provided ones.
func (s *State) ThreadListSync(tls *ThreadListSync) error {
	if s == nil {
		return ErrNilState
	}
	if tls == nil {
		return ErrStateInvalidData
	}

	for _, t := range tls.Threads {
		if t == nil {
			return ErrStateInvalidData
		}
	}
	if s.TrackThreadMembers {
		for _, m := range tls.Members {
			if m == nil {
				return ErrStateInvalidData
			}
		}
	}

	s.Lock()
	defer s.Unlock()

	guild, ok := s.guildMap[tls.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	// This algorithm filters out archived or
	// threads which are children of channels in channelIDs
	// and then it adds all synced threads to guild threads and cache
	updated := copyGuild(guild)
	updated.Threads = updated.Threads[:0]
outer:
	for _, t := range guild.Threads {
		if t == nil {
			continue
		}
		active := t.ThreadMetadata == nil || !t.ThreadMetadata.Archived
		if active && tls.ChannelIDs != nil {
			for _, v := range tls.ChannelIDs {
				if t.ParentID == v {
					delete(s.channelMap, t.ID)
					continue outer
				}
			}
			updated.Threads = append(updated.Threads, t)
		} else {
			delete(s.channelMap, t.ID)
		}
	}
	syncedThreads := make(map[string]*Channel, len(tls.Threads))
	for _, t := range tls.Threads {
		syncedThreads[t.ID] = t
		if !s.TrackThreadMembers {
			t = threadWithoutMembers(t)
		}
		s.channelMap[t.ID] = t
		updated.Threads = append(updated.Threads, t)
	}

	if s.TrackThreadMembers {
		for _, m := range tls.Members {
			if c, ok := s.channelMap[m.ID]; ok {
				if synced := syncedThreads[m.ID]; synced != nil {
					synced.Member = m
				}
				copied := copyChannel(c)
				copied.Member = m
				s.channelMap[m.ID] = copied
				replaceThreadInGuild(updated, c, copied)
			}
		}
	}

	s.replaceGuild(guild, updated)
	return nil
}

// ThreadMembersUpdate updates thread members list
func (s *State) ThreadMembersUpdate(tmu *ThreadMembersUpdate) error {
	if s == nil {
		return ErrNilState
	}
	if tmu == nil {
		return ErrStateInvalidData
	}

	for _, addedMember := range tmu.AddedMembers {
		if addedMember.ThreadMember == nil {
			return ErrStateInvalidData
		}
		if s.TrackMembers && addedMember.Member != nil && addedMember.Member.User == nil {
			return ErrStateInvalidData
		}
	}

	s.Lock()
	defer s.Unlock()

	thread, ok := s.channelMap[tmu.ID]
	if !ok {
		return ErrStateNotFound
	}
	updated := copyChannel(thread)

	if len(tmu.RemovedMembers) > 0 {
		removedMembers := make(map[string]struct{}, len(tmu.RemovedMembers))
		for _, removedMember := range tmu.RemovedMembers {
			removedMembers[removedMember] = struct{}{}
		}

		members := updated.Members[:0]
		for _, member := range updated.Members {
			if member == nil {
				members = append(members, member)
				continue
			}
			if _, ok := removedMembers[member.UserID]; ok {
				continue
			}
			members = append(members, member)
		}
		updated.Members = members
	}

	for _, addedMember := range tmu.AddedMembers {
		updated.Members = append(updated.Members, addedMember.ThreadMember)
		if s.TrackMembers && addedMember.Member != nil {
			member := *addedMember.Member
			if member.GuildID == "" {
				member.GuildID = tmu.GuildID
			}
			if err := s.memberAdd(&member); err != nil {
				return err
			}
		}
		if s.TrackPresences && addedMember.Presence != nil {
			if err := s.presenceAdd(tmu.GuildID, addedMember.Presence); err != nil {
				return err
			}
		}
	}
	updated.MemberCount = tmu.MemberCount
	s.channelMap[tmu.ID] = updated
	s.replaceChannel(thread, updated)

	return nil
}

// ThreadMemberUpdate sets or updates member data for the current user.
func (s *State) ThreadMemberUpdate(mu *ThreadMemberUpdate) error {
	if s == nil {
		return ErrNilState
	}
	if mu == nil || mu.ThreadMember == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	thread, ok := s.channelMap[mu.ID]
	if !ok {
		return ErrStateNotFound
	}
	updated := copyChannel(thread)
	updated.Member = mu.ThreadMember
	s.channelMap[mu.ID] = updated
	s.replaceChannel(thread, updated)
	return nil
}

// Channel gets a channel by ID, it will look in all guilds and private channels.
func (s *State) Channel(channelID string) (*Channel, error) {
	if s == nil {
		return nil, ErrNilState
	}

	s.RLock()
	defer s.RUnlock()

	if c, ok := s.channelMap[channelID]; ok {
		return c, nil
	}

	return nil, ErrStateNotFound
}

// Emoji returns an emoji for a guild and emoji id.
func (s *State) Emoji(guildID, emojiID string) (*Emoji, error) {
	if s == nil {
		return nil, ErrNilState
	}

	guild, err := s.Guild(guildID)
	if err != nil {
		return nil, err
	}

	s.RLock()
	defer s.RUnlock()

	for _, e := range guild.Emojis {
		if e.ID == emojiID {
			return e, nil
		}
	}

	return nil, ErrStateNotFound
}

// EmojiAdd adds an emoji to the current world state.
func (s *State) EmojiAdd(guildID string, emoji *Emoji) error {
	if s == nil {
		return ErrNilState
	}

	if emoji == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// Fetch the guild under the write lock; a pointer obtained earlier
	// could have been replaced by a concurrent update, losing this
	// change in an orphaned snapshot.
	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)
	emojiAddToGuild(updated, emoji)
	s.replaceGuild(guild, updated)
	return nil
}

// emojiAddToGuild adds or replaces an emoji in a guild that was already
// copied for replacement; it must not be called on a guild pointer that
// has been handed out to callers.
func emojiAddToGuild(guild *Guild, emoji *Emoji) {
	for i, e := range guild.Emojis {
		if e != nil && e.ID == emoji.ID {
			guild.Emojis[i] = emoji
			return
		}
	}

	guild.Emojis = append(guild.Emojis, emoji)
}

// EmojisAdd adds multiple emojis to the world state.
func (s *State) EmojisAdd(guildID string, emojis []*Emoji) error {
	if s == nil {
		return ErrNilState
	}

	for _, e := range emojis {
		if e == nil {
			return ErrStateInvalidData
		}
	}

	s.Lock()
	defer s.Unlock()

	guild, ok := s.guildMap[guildID]
	if !ok {
		return ErrStateNotFound
	}

	// Copy the guild once for the whole batch.
	updated := copyGuild(guild)
	for _, e := range emojis {
		emojiAddToGuild(updated, e)
	}
	s.replaceGuild(guild, updated)
	return nil
}

// MessageAdd adds a message to the current world state, or updates it if it exists.
// If the channel cannot be found, the message is discarded.
// Messages are kept in state up to s.MaxMessageCount per channel.
func (s *State) MessageAdd(message *Message) error {
	if s == nil {
		return ErrNilState
	}

	if message == nil {
		return ErrStateInvalidData
	}

	s.Lock()
	defer s.Unlock()

	// Fetch the channel under the write lock; a pointer obtained
	// earlier could have been replaced by a concurrent update, losing
	// this change in an orphaned snapshot.
	c, ok := s.channelMap[message.ChannelID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyChannel(c)

	// If the message exists, merge in the new message contents into a
	// copy; the cached message is a shared snapshot.
	for i, m := range updated.Messages {
		if m == nil {
			continue
		}
		if m.ID == message.ID {
			merged := *m
			if message.Content != "" {
				merged.Content = message.Content
			}
			if message.EditedTimestamp != nil {
				merged.EditedTimestamp = message.EditedTimestamp
			}
			if message.Mentions != nil {
				merged.Mentions = message.Mentions
			}
			if message.Embeds != nil {
				merged.Embeds = message.Embeds
			}
			if message.Attachments != nil {
				merged.Attachments = message.Attachments
			}
			if !message.Timestamp.IsZero() {
				merged.Timestamp = message.Timestamp
			}
			if message.Author != nil {
				merged.Author = message.Author
			}
			if message.Components != nil {
				merged.Components = message.Components
			}

			updated.Messages[i] = &merged
			s.channelMap[updated.ID] = updated
			s.replaceChannel(c, updated)
			return nil
		}
	}

	updated.Messages = append(updated.Messages, message)

	if len(updated.Messages) > s.MaxMessageCount {
		updated.Messages = updated.Messages[len(updated.Messages)-s.MaxMessageCount:]
	}

	s.channelMap[updated.ID] = updated
	s.replaceChannel(c, updated)
	return nil
}

// MessageRemove removes a message from the world state.
func (s *State) MessageRemove(message *Message) error {
	if s == nil {
		return ErrNilState
	}
	if message == nil {
		return ErrStateInvalidData
	}

	return s.messageRemoveByID(message.ChannelID, message.ID)
}

// messageRemoveByID removes a message by channelID and messageID from the world state.
func (s *State) messageRemoveByID(channelID, messageID string) error {
	s.Lock()
	defer s.Unlock()

	// Fetch the channel under the write lock; a pointer obtained
	// earlier could have been replaced by a concurrent update, losing
	// this removal in an orphaned snapshot.
	c, ok := s.channelMap[channelID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyChannel(c)
	for i, m := range updated.Messages {
		if m == nil {
			continue
		}
		if m.ID == messageID {
			updated.Messages = append(updated.Messages[:i], updated.Messages[i+1:]...)
			s.channelMap[updated.ID] = updated
			s.replaceChannel(c, updated)
			return nil
		}
	}

	return ErrStateNotFound
}

func (s *State) fillMessageGuildID(message *Message) {
	if message == nil || message.GuildID != "" || message.ChannelID == "" {
		return
	}

	s.RLock()
	defer s.RUnlock()

	if channel, ok := s.channelMap[message.ChannelID]; ok && channel != nil {
		message.GuildID = channel.GuildID
	}
}

func (s *State) voiceStateUpdate(update *VoiceStateUpdate) error {
	s.Lock()
	defer s.Unlock()

	// Fetch the guild under the write lock; a pointer obtained earlier
	// could have been replaced by a concurrent update, losing this
	// change in an orphaned snapshot.
	guild, ok := s.guildMap[update.GuildID]
	if !ok {
		return ErrStateNotFound
	}

	updated := copyGuild(guild)

	// Handle Leaving Channel
	if update.ChannelID == "" {
		for i, state := range updated.VoiceStates {
			if state != nil && state.UserID == update.UserID {
				updated.VoiceStates = append(updated.VoiceStates[:i], updated.VoiceStates[i+1:]...)
				s.replaceGuild(guild, updated)
				return nil
			}
		}
	} else {
		for i, state := range updated.VoiceStates {
			if state != nil && state.UserID == update.UserID {
				updated.VoiceStates[i] = update.VoiceState
				s.replaceGuild(guild, updated)
				return nil
			}
		}

		updated.VoiceStates = append(updated.VoiceStates, update.VoiceState)
		s.replaceGuild(guild, updated)
	}

	return nil
}

// VoiceState gets a VoiceState by guild and user ID.
func (s *State) VoiceState(guildID, userID string) (*VoiceState, error) {
	if s == nil {
		return nil, ErrNilState
	}

	s.RLock()
	defer s.RUnlock()

	guild, ok := s.guildMap[guildID]
	if !ok {
		return nil, ErrStateNotFound
	}

	for _, state := range guild.VoiceStates {
		if state.UserID == userID {
			return state, nil
		}
	}

	return nil, ErrStateNotFound
}

// Message gets a message by channel and message ID.
func (s *State) Message(channelID, messageID string) (*Message, error) {
	if s == nil {
		return nil, ErrNilState
	}

	c, err := s.Channel(channelID)
	if err != nil {
		return nil, err
	}

	s.RLock()
	defer s.RUnlock()

	for _, m := range c.Messages {
		if m.ID == messageID {
			return m, nil
		}
	}

	return nil, ErrStateNotFound
}

// OnReady takes a Ready event and updates all internal state.
func (s *State) onReady(se *Session, r *Ready) (err error) {
	if s == nil {
		return ErrNilState
	}

	s.Lock()
	defer s.Unlock()

	s.guildMap = make(map[string]*Guild, len(r.Guilds))
	s.channelMap = make(map[string]*Channel)
	s.memberMap = make(map[string]map[string]*Member, len(r.Guilds))

	// We must track at least the current user for Voice, even
	// if state is disabled, store the bare essentials.
	if !se.StateEnabled {
		ready := Ready{
			Version:     r.Version,
			SessionID:   r.SessionID,
			User:        r.User,
			Shard:       r.Shard,
			Application: r.Application,
		}

		s.Ready = ready

		return nil
	}

	ready := *r
	ready.Guilds = make([]*Guild, 0, len(r.Guilds))
	for _, g := range r.Guilds {
		tracked := s.guildForTracking(g)
		if tracked == nil {
			continue
		}
		ready.Guilds = append(ready.Guilds, tracked)
	}
	if !s.TrackChannels {
		ready.PrivateChannels = nil
	}
	s.Ready = ready

	for _, g := range s.Guilds {
		if g == nil {
			continue
		}
		s.guildMap[g.ID] = g
		if s.TrackMembers {
			s.createMemberMap(g)
		} else {
			delete(s.memberMap, g.ID)
		}

		for _, c := range g.Channels {
			if c == nil {
				continue
			}
			s.channelMap[c.ID] = c
		}

		for _, t := range g.Threads {
			if t == nil {
				continue
			}
			s.channelMap[t.ID] = t
		}
	}

	if s.TrackChannels {
		for _, c := range s.PrivateChannels {
			if c == nil {
				continue
			}
			s.channelMap[c.ID] = c
		}
	}

	return nil
}

// OnInterface handles all events related to states.
func (s *State) OnInterface(se *Session, i interface{}) (err error) {
	if s == nil {
		return ErrNilState
	}
	if se == nil || i == nil {
		return ErrStateInvalidData
	}

	r, ok := i.(*Ready)
	if ok {
		if r == nil {
			return ErrStateInvalidData
		}
		return s.onReady(se, r)
	}

	if !se.StateEnabled {
		return nil
	}

	switch t := i.(type) {
	case *GuildCreate:
		if t == nil {
			return ErrStateInvalidData
		}
		err = s.GuildAdd(t.Guild)
	case *GuildUpdate:
		if t == nil {
			return ErrStateInvalidData
		}
		err = s.GuildAdd(t.Guild)
	case *GuildDelete:
		if t == nil || t.Guild == nil {
			return ErrStateInvalidData
		}

		var old *Guild
		old, err = s.Guild(t.ID)
		if err == nil {
			oldCopy := *old
			t.BeforeDelete = &oldCopy
		}

		if t.Unavailable {
			// An unavailable delete carries only {ID, Unavailable};
			// merging it through GuildAdd would zero every other field
			// of the cached guild. Keep the cached data and only flag
			// the guild as unavailable.
			s.Lock()
			if guild, ok := s.guildMap[t.ID]; ok {
				updated := copyGuild(guild)
				updated.Unavailable = true
				s.replaceGuild(guild, updated)
				s.Unlock()
			} else {
				s.Unlock()
				err = s.GuildAdd(t.Guild)
			}
		} else {
			err = s.GuildRemove(t.Guild)
		}
	case *GuildMemberAdd:
		if t == nil || t.Member == nil || t.Member.GuildID == "" {
			return ErrStateInvalidData
		}
		if s.TrackMembers && t.Member.User == nil {
			return ErrStateInvalidData
		}

		// Updates the MemberCount of the guild.
		s.Lock()
		err = s.updateGuildMemberCount(t.Member.GuildID, 1)
		s.Unlock()
		if err != nil {
			return err
		}

		// Caches member if tracking is enabled.
		if s.TrackMembers {
			err = s.MemberAdd(t.Member)
		}
	case *GuildMemberUpdate:
		if s.TrackMembers {
			if t == nil || t.Member == nil || t.Member.GuildID == "" || t.Member.User == nil {
				return ErrStateInvalidData
			}

			var old *Member
			old, err = s.Member(t.GuildID, t.User.ID)
			if err == nil {
				oldCopy := *old
				if oldCopy.User != nil {
					oldUser := *oldCopy.User
					oldCopy.User = &oldUser
				}
				t.BeforeUpdate = &oldCopy
			}

			err = s.MemberAdd(t.Member)
		}
	case *GuildMemberRemove:
		if t == nil || t.Member == nil || t.Member.GuildID == "" {
			return ErrStateInvalidData
		}
		if s.TrackMembers && t.Member.User == nil {
			return ErrStateInvalidData
		}

		// Updates the MemberCount of the guild.
		s.Lock()
		err = s.updateGuildMemberCount(t.Member.GuildID, -1)
		s.Unlock()
		if err != nil {
			return err
		}

		// Removes member from the cache if tracking is enabled.
		if s.TrackMembers {
			old, err := s.Member(t.Member.GuildID, t.Member.User.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeDelete = &oldCopy
			}

			err = s.MemberRemove(t.Member)
		}
	case *GuildMembersChunk:
		if (s.TrackMembers || s.TrackPresences) && t == nil {
			return ErrStateInvalidData
		}
		if s.TrackMembers {
			for _, m := range t.Members {
				if m == nil || m.User == nil {
					return ErrStateInvalidData
				}
			}

			// Copy the guild once for the whole chunk; copying per
			// member would churn through one guild snapshot per entry.
			s.Lock()
			guild, gok := s.guildMap[t.GuildID]
			members, mok := s.memberMap[t.GuildID]
			if gok && mok {
				updated := copyGuild(guild)
				for i := range t.Members {
					t.Members[i].GuildID = t.GuildID
					memberAddToGuild(members, updated, copyMember(t.Members[i]))
				}
				s.replaceGuild(guild, updated)
			} else {
				err = ErrStateNotFound
			}
			s.Unlock()
		}

		if s.TrackPresences {
			s.Lock()
			guild, ok := s.guildMap[t.GuildID]
			if ok {
				updated := copyGuild(guild)
				for _, p := range t.Presences {
					if perr := presenceAddToGuild(updated, p); perr != nil {
						err = perr
					}
				}
				s.replaceGuild(guild, updated)
			} else {
				err = ErrStateNotFound
			}
			s.Unlock()
		}
	case *GuildRoleCreate:
		if s.TrackRoles {
			if t == nil || t.GuildRole == nil || t.Role == nil {
				return ErrStateInvalidData
			}
			err = s.RoleAdd(t.GuildID, t.Role)
		}
	case *GuildRoleUpdate:
		if s.TrackRoles {
			if t == nil || t.GuildRole == nil || t.Role == nil {
				return ErrStateInvalidData
			}
			old, err := s.Role(t.GuildID, t.Role.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeUpdate = &oldCopy
			}

			err = s.RoleAdd(t.GuildID, t.Role)
		}
	case *GuildRoleDelete:
		if s.TrackRoles {
			if t == nil {
				return ErrStateInvalidData
			}
			old, err := s.Role(t.GuildID, t.RoleID)
			if err == nil {
				oldCopy := *old
				t.BeforeDelete = &oldCopy
			}

			err = s.RoleRemove(t.GuildID, t.RoleID)
		}
	case *GuildEmojisUpdate:
		if s.TrackEmojis {
			if t == nil {
				return ErrStateInvalidData
			}
			s.Lock()
			defer s.Unlock()
			guild, ok := s.guildMap[t.GuildID]
			if !ok {
				return ErrStateNotFound
			}
			updated := copyGuild(guild)
			updated.Emojis = t.Emojis
			s.replaceGuild(guild, updated)
		}
	case *GuildStickersUpdate:
		if s.TrackStickers {
			if t == nil {
				return ErrStateInvalidData
			}
			s.Lock()
			defer s.Unlock()
			guild, ok := s.guildMap[t.GuildID]
			if !ok {
				return ErrStateNotFound
			}
			updated := copyGuild(guild)
			updated.Stickers = t.Stickers
			s.replaceGuild(guild, updated)
		}
	case *ChannelCreate:
		if s.TrackChannels {
			if t == nil || t.Channel == nil {
				return ErrStateInvalidData
			}
			err = s.ChannelAdd(t.Channel)
		}
	case *ChannelUpdate:
		if s.TrackChannels {
			if t == nil || t.Channel == nil {
				return ErrStateInvalidData
			}
			old, err := s.Channel(t.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeUpdate = &oldCopy
			}
			err = s.ChannelAdd(t.Channel)
		}
	case *ChannelDelete:
		if s.TrackChannels {
			if t == nil || t.Channel == nil {
				return ErrStateInvalidData
			}
			old, err := s.Channel(t.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeDelete = &oldCopy
			}
			err = s.ChannelRemove(t.Channel)
		}
	case *ThreadCreate:
		if s.TrackThreads {
			if t == nil || t.Channel == nil {
				return ErrStateInvalidData
			}
			err = s.ChannelAdd(t.Channel)
		}
	case *ThreadUpdate:
		if s.TrackThreads {
			if t == nil || t.Channel == nil {
				return ErrStateInvalidData
			}
			old, err := s.Channel(t.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeUpdate = &oldCopy
			}
			err = s.ChannelAdd(t.Channel)
		}
	case *ThreadDelete:
		if s.TrackThreads {
			if t == nil || t.Channel == nil {
				return ErrStateInvalidData
			}
			old, err := s.Channel(t.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeDelete = &oldCopy
			}
			err = s.ChannelRemove(t.Channel)
		}
	case *ThreadMemberUpdate:
		if s.TrackThreads && s.TrackThreadMembers {
			if t == nil || t.ThreadMember == nil {
				return ErrStateInvalidData
			}
			err = s.ThreadMemberUpdate(t)
		}
	case *ThreadMembersUpdate:
		if s.TrackThreads && s.TrackThreadMembers {
			err = s.ThreadMembersUpdate(t)
		}
	case *ThreadListSync:
		if s.TrackThreads {
			err = s.ThreadListSync(t)
		}
	case *MessageCreate:
		if t == nil || t.Message == nil {
			return ErrStateInvalidData
		}
		s.fillMessageGuildID(t.Message)
		if s.MaxMessageCount != 0 {
			err = s.MessageAdd(t.Message)
		}
	case *MessageUpdate:
		if t == nil || t.Message == nil {
			return ErrStateInvalidData
		}
		s.fillMessageGuildID(t.Message)
		if s.MaxMessageCount != 0 {
			var old *Message
			old, err = s.Message(t.ChannelID, t.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeUpdate = &oldCopy
			}

			err = s.MessageAdd(t.Message)
		}
	case *MessageDelete:
		if t == nil || t.Message == nil {
			return ErrStateInvalidData
		}
		s.fillMessageGuildID(t.Message)
		if s.MaxMessageCount != 0 {
			var old *Message
			old, err = s.Message(t.ChannelID, t.ID)
			if err == nil {
				oldCopy := *old
				t.BeforeDelete = &oldCopy
			}

			err = s.MessageRemove(t.Message)
		}
	case *MessageDeleteBulk:
		if s.MaxMessageCount != 0 {
			if t == nil {
				return ErrStateInvalidData
			}
			for _, mID := range t.Messages {
				s.messageRemoveByID(t.ChannelID, mID)
			}
		}
	case *VoiceStateUpdate:
		if s.TrackVoice {
			if t == nil || t.VoiceState == nil {
				return ErrStateInvalidData
			}

			var old *VoiceState
			old, err = s.VoiceState(t.GuildID, t.UserID)
			if err == nil {
				oldCopy := *old
				t.BeforeUpdate = &oldCopy
			}

			err = s.voiceStateUpdate(t)
		}
	case *PresenceUpdate:
		if (s.TrackPresences || s.TrackMembers) && t == nil {
			return ErrStateInvalidData
		}
		if s.TrackPresences {
			err = s.PresenceAdd(t.GuildID, &t.Presence)
			if err != nil {
				return err
			}
		}
		if s.TrackMembers {
			if t.Status == StatusOffline {
				return
			}
			if t.User == nil || t.User.ID == "" {
				return ErrStateInvalidData
			}

			var m *Member
			m, err = s.Member(t.GuildID, t.User.ID)

			if err != nil {
				// Member not found; this is a user coming online
				m = &Member{
					GuildID: t.GuildID,
					User:    t.User,
				}
			} else {
				// The cached member is a shared snapshot; mutate a
				// copy and let MemberAdd replace it.
				m = copyMember(m)
				if m.User != nil && t.User.Username != "" {
					m.User.Username = t.User.Username
				}
			}

			err = s.MemberAdd(m)
		}

	}

	return
}

// UserChannelPermissions returns the permission of a user in a channel.
// userID    : The ID of the user to calculate permissions for.
// channelID : The ID of the channel to calculate permission for.
func (s *State) UserChannelPermissions(userID, channelID string) (apermissions int64, err error) {
	if s == nil {
		return 0, ErrNilState
	}

	s.RLock()
	defer s.RUnlock()

	guild, channel, thread, err := s.channelPermissionContext(channelID)
	if err != nil {
		return
	}

	members, ok := s.memberMap[guild.ID]
	if !ok {
		return 0, ErrStateNotFound
	}

	member, ok := members[userID]
	if !ok {
		return 0, ErrStateNotFound
	}

	apermissions = memberPermissions(guild, channel, userID, member.Roles)
	if thread {
		apermissions = threadPermissions(apermissions)
	}
	return
}

// MessagePermissions returns the permissions of the author of the message
// in the channel in which it was sent.
func (s *State) MessagePermissions(message *Message) (apermissions int64, err error) {
	if s == nil {
		return 0, ErrNilState
	}

	if message.Author == nil || message.Member == nil {
		return 0, ErrMessageIncompletePermissions
	}

	s.RLock()
	defer s.RUnlock()

	guild, channel, thread, err := s.channelPermissionContext(message.ChannelID)
	if err != nil {
		return
	}

	apermissions = memberPermissions(guild, channel, message.Author.ID, message.Member.Roles)
	if thread {
		apermissions = threadPermissions(apermissions)
	}
	return
}

func (s *State) channelPermissionContext(channelID string) (guild *Guild, channel *Channel, thread bool, err error) {
	var ok bool
	channel, ok = s.channelMap[channelID]
	if !ok {
		err = ErrStateNotFound
		return
	}

	thread = channel.IsThread()
	if thread && channel.ParentID != "" {
		channel, ok = s.channelMap[channel.ParentID]
		if !ok {
			err = ErrStateNotFound
			return
		}
	}

	guild, ok = s.guildMap[channel.GuildID]
	if !ok {
		err = ErrStateNotFound
	}
	return
}

// UserColor returns the color of a user in a channel.
// While colors are defined at a Guild level, determining for a channel is more useful in message handlers.
// 0 is returned in cases of error, which is the color of @everyone.
// userID    : The ID of the user to calculate the color for.
// channelID   : The ID of the channel to calculate the color for.
func (s *State) UserColor(userID, channelID string) int {
	if s == nil {
		return 0
	}

	s.RLock()
	defer s.RUnlock()

	channel, ok := s.channelMap[channelID]
	if !ok {
		return 0
	}

	guild, ok := s.guildMap[channel.GuildID]
	if !ok {
		return 0
	}

	members, ok := s.memberMap[guild.ID]
	if !ok {
		return 0
	}

	member, ok := members[userID]
	if !ok {
		return 0
	}

	return firstRoleColorColor(guild, member.Roles)
}

// MessageColor returns the color of the author's name as displayed
// in the client associated with this message.
func (s *State) MessageColor(message *Message) int {
	if s == nil {
		return 0
	}

	if message.Member == nil || message.Member.Roles == nil {
		return 0
	}

	s.RLock()
	defer s.RUnlock()

	channel, ok := s.channelMap[message.ChannelID]
	if !ok {
		return 0
	}

	guild, ok := s.guildMap[channel.GuildID]
	if !ok {
		return 0
	}

	return firstRoleColorColor(guild, message.Member.Roles)
}

func firstRoleColorColor(guild *Guild, memberRoles []string) int {
	roles := append(Roles(nil), guild.Roles...)
	sort.Sort(roles)

	for _, role := range roles {
		for _, roleID := range memberRoles {
			if role.ID == roleID {
				if role.Color != 0 {
					return role.Color
				}
			}
		}
	}

	for _, role := range roles {
		if role.ID == guild.ID {
			return role.Color
		}
	}

	return 0
}
