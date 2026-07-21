// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package discordgo

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMember_DisplayName(t *testing.T) {
	user := &User{
		GlobalName: "Global",
	}
	t.Run("no server nickname set", func(t *testing.T) {
		m := &Member{
			Nick: "",
			User: user,
		}
		want := user.DisplayName()
		if dn := m.DisplayName(); dn != want {
			t.Errorf("Member.DisplayName() = %v, want %v", dn, want)
		}
	})
	t.Run("server nickname set", func(t *testing.T) {
		m := &Member{
			Nick: "Server",
			User: user,
		}
		if dn := m.DisplayName(); dn != m.Nick {
			t.Errorf("Member.DisplayName() = %v, want %v", dn, m.Nick)
		}
	})
}

func TestMemberHelpersHandlePartialMember(t *testing.T) {
	member := &Member{
		Nick:   "Server",
		Avatar: "guild-avatar",
		Banner: "guild-banner",
	}

	if got := member.Mention(); got != "" {
		t.Fatalf("Mention = %q, want empty", got)
	}
	if got := member.AvatarURL(""); got != "" {
		t.Fatalf("AvatarURL = %q, want empty", got)
	}
	if got := member.BannerURL(""); got != "" {
		t.Fatalf("BannerURL = %q, want empty", got)
	}
	if got := member.DisplayName(); got != "Server" {
		t.Fatalf("DisplayName = %q, want %q", got, "Server")
	}

	member.Nick = ""
	if got := member.DisplayName(); got != "" {
		t.Fatalf("DisplayName without user or nickname = %q, want empty", got)
	}
}

func TestMemberHelpersHandleNilMember(t *testing.T) {
	var member *Member

	if got := member.Mention(); got != "" {
		t.Fatalf("Mention = %q, want empty", got)
	}
	if got := member.AvatarURL(""); got != "" {
		t.Fatalf("AvatarURL = %q, want empty", got)
	}
	if got := member.BannerURL(""); got != "" {
		t.Fatalf("BannerURL = %q, want empty", got)
	}
	if got := member.DisplayName(); got != "" {
		t.Fatalf("DisplayName = %q, want empty", got)
	}
}

func TestMemberHelpersPreserveCompleteMember(t *testing.T) {
	member := &Member{
		GuildID: "guild",
		Nick:    "Server",
		Avatar:  "guild-avatar",
		Banner:  "guild-banner",
		User:    &User{ID: "user"},
	}

	if got := member.Mention(); got != "<@!user>" {
		t.Fatalf("Mention = %q, want %q", got, "<@!user>")
	}
	if got, want := member.AvatarURL(""), EndpointGuildMemberAvatar("guild", "user", "guild-avatar"); got != want {
		t.Fatalf("AvatarURL = %q, want %q", got, want)
	}
	if got, want := member.BannerURL(""), EndpointGuildMemberBanner("guild", "user", "guild-banner"); got != want {
		t.Fatalf("BannerURL = %q, want %q", got, want)
	}
	if got := member.DisplayName(); got != "Server" {
		t.Fatalf("DisplayName = %q, want %q", got, "Server")
	}
}

func TestGuildIncidentsData(t *testing.T) {
	var guild Guild
	if err := json.Unmarshal([]byte(`{
		"id":"guild",
		"incidents_data":{
			"invites_disabled_until":"2026-07-11T10:00:00Z",
			"dms_disabled_until":null,
			"dm_spam_detected_at":"2026-07-10T09:30:00.123Z",
			"raid_detected_at":"2026-07-10T09:00:00+02:00"
		}
	}`), &guild); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if guild.IncidentsData == nil {
		t.Fatal("IncidentsData is nil")
	}
	if guild.IncidentsData.InvitesDisabledUntil == nil || !guild.IncidentsData.InvitesDisabledUntil.Equal(time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("InvitesDisabledUntil = %v", guild.IncidentsData.InvitesDisabledUntil)
	}
	if guild.IncidentsData.DMsDisabledUntil != nil {
		t.Fatalf("DMsDisabledUntil = %v, want nil", guild.IncidentsData.DMsDisabledUntil)
	}
	if guild.IncidentsData.DMSpamDetectedAt == nil || !guild.IncidentsData.DMSpamDetectedAt.Equal(time.Date(2026, 7, 10, 9, 30, 0, 123000000, time.UTC)) {
		t.Fatalf("DMSpamDetectedAt = %v", guild.IncidentsData.DMSpamDetectedAt)
	}
	if guild.IncidentsData.RaidDetectedAt == nil || !guild.IncidentsData.RaidDetectedAt.Equal(time.Date(2026, 7, 10, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("RaidDetectedAt = %v", guild.IncidentsData.RaidDetectedAt)
	}
}

func TestGuildSafetyAlertsChannelID(t *testing.T) {
	var guild Guild
	if err := json.Unmarshal([]byte(`{"id":"guild","safety_alerts_channel_id":"channel"}`), &guild); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if guild.SafetyAlertsChannelID != "channel" {
		t.Fatalf("SafetyAlertsChannelID = %q, want channel", guild.SafetyAlertsChannelID)
	}

	channelID := "channel"
	channelValue := &channelID
	encoded, err := json.Marshal(GuildParams{SafetyAlertsChannelID: &channelValue})
	if err != nil {
		t.Fatalf("json.Marshal set channel returned error: %v", err)
	}
	if !strings.Contains(string(encoded), `"safety_alerts_channel_id":"channel"`) {
		t.Fatalf("set channel JSON = %s", encoded)
	}

	disable := (*string)(nil)
	encoded, err = json.Marshal(GuildParams{SafetyAlertsChannelID: &disable})
	if err != nil {
		t.Fatalf("json.Marshal clear channel returned error: %v", err)
	}
	if !strings.Contains(string(encoded), `"safety_alerts_channel_id":null`) {
		t.Fatalf("clear channel JSON = %s", encoded)
	}

	encoded, err = json.Marshal(GuildParams{})
	if err != nil {
		t.Fatalf("json.Marshal omitted channel returned error: %v", err)
	}
	if strings.Contains(string(encoded), `"safety_alerts_channel_id"`) {
		t.Fatalf("omitted channel JSON = %s", encoded)
	}
}

func TestGuildMaxStageVideoChannelUsers(t *testing.T) {
	var guild Guild
	if err := json.Unmarshal([]byte(`{"id":"guild","max_stage_video_channel_users":300}`), &guild); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if guild.MaxStageVideoChannelUsers != 300 {
		t.Fatalf("MaxStageVideoChannelUsers = %d, want 300", guild.MaxStageVideoChannelUsers)
	}
}

func TestGuildPremiumProgressBarEnabled(t *testing.T) {
	var guild Guild
	if err := json.Unmarshal([]byte(`{"id":"guild","premium_progress_bar_enabled":true}`), &guild); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !guild.PremiumProgressBarEnabled {
		t.Fatal("PremiumProgressBarEnabled is false, want true")
	}
}

func TestGuildHubType(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    GuildHubType
	}{
		{"default", `{"id":"guild","hub_type":0}`, GuildHubTypeDefault},
		{"high school", `{"id":"guild","hub_type":1}`, GuildHubTypeHighSchool},
		{"college", `{"id":"guild","hub_type":2}`, GuildHubTypeCollege},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var guild Guild
			if err := json.Unmarshal([]byte(test.payload), &guild); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if guild.HubType == nil || *guild.HubType != test.want {
				t.Fatalf("HubType = %v, want %v", guild.HubType, test.want)
			}
		})
	}

	var guild Guild
	if err := json.Unmarshal([]byte(`{"id":"guild","hub_type":null}`), &guild); err != nil {
		t.Fatalf("json.Unmarshal null returned error: %v", err)
	}
	if guild.HubType != nil {
		t.Fatalf("HubType = %v, want nil", guild.HubType)
	}
}

func TestGuildTemplateIconHash(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"present", `{"code":"template","serialized_source_guild":{"id":"guild","icon_hash":"template-icon"}}`, "template-icon"},
		{"null", `{"code":"template","serialized_source_guild":{"id":"guild","icon_hash":null}}`, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var guildTemplate GuildTemplate
			if err := json.Unmarshal([]byte(test.payload), &guildTemplate); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if guildTemplate.SerializedSourceGuild == nil {
				t.Fatal("SerializedSourceGuild is nil")
			}
			if got := guildTemplate.SerializedSourceGuild.IconHash; got != test.want {
				t.Fatalf("IconHash = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGuildWelcomeScreenJSON(t *testing.T) {
	var invite Invite
	if err := json.Unmarshal([]byte(`{"code":"invite","guild":{"id":"guild","welcome_screen":{"description":"Welcome","welcome_channels":[{"channel_id":"channel","description":"Read the rules","emoji_id":null,"emoji_name":"👋"}]}}}`), &invite); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if invite.Guild == nil {
		t.Fatal("Guild is nil")
	}
	if invite.Guild.WelcomeScreen == nil {
		t.Fatal("WelcomeScreen is nil")
	}
	welcomeScreen := invite.Guild.WelcomeScreen
	if welcomeScreen.Description == nil || *welcomeScreen.Description != "Welcome" {
		t.Fatalf("Description = %v, want Welcome", welcomeScreen.Description)
	}
	if len(welcomeScreen.WelcomeChannels) != 1 {
		t.Fatalf("len(WelcomeChannels) = %d, want 1", len(welcomeScreen.WelcomeChannels))
	}
	channel := welcomeScreen.WelcomeChannels[0]
	if channel.ChannelID != "channel" || channel.Description != "Read the rules" || channel.EmojiID != nil || channel.EmojiName == nil || *channel.EmojiName != "👋" {
		t.Fatalf("WelcomeChannels[0] = %#v", channel)
	}

	var withoutWelcomeScreen Invite
	if err := json.Unmarshal([]byte(`{"code":"invite","guild":{"id":"guild","welcome_screen":null}}`), &withoutWelcomeScreen); err != nil {
		t.Fatalf("json.Unmarshal null returned error: %v", err)
	}
	if withoutWelcomeScreen.Guild == nil {
		t.Fatal("Guild is nil for null welcome screen")
	}
	if withoutWelcomeScreen.Guild.WelcomeScreen != nil {
		t.Fatalf("WelcomeScreen = %#v, want nil", withoutWelcomeScreen.Guild.WelcomeScreen)
	}
}

func TestGuildWelcomeScreenParamsJSON(t *testing.T) {
	enabled := true
	enabledValue := &enabled
	description := "Welcome"
	descriptionValue := &description
	emojiName := "👋"
	channels := []GuildWelcomeScreenChannel{{
		ChannelID:   "channel",
		Description: "Read the rules",
		EmojiName:   &emojiName,
	}}

	encoded, err := json.Marshal(GuildWelcomeScreenParams{
		Enabled:         &enabledValue,
		WelcomeChannels: &channels,
		Description:     &descriptionValue,
	})
	if err != nil {
		t.Fatalf("json.Marshal values returned error: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal values returned error: %v", err)
	}
	if got := string(payload["enabled"]); got != "true" {
		t.Fatalf("enabled = %s, want true", got)
	}
	if got := string(payload["description"]); got != `"Welcome"` {
		t.Fatalf("description = %s, want Welcome", got)
	}
	var encodedChannels []GuildWelcomeScreenChannel
	if err := json.Unmarshal(payload["welcome_channels"], &encodedChannels); err != nil {
		t.Fatalf("json.Unmarshal welcome_channels returned error: %v", err)
	}
	if len(encodedChannels) != 1 || encodedChannels[0].ChannelID != "channel" || encodedChannels[0].EmojiID != nil || encodedChannels[0].EmojiName == nil || *encodedChannels[0].EmojiName != "👋" {
		t.Fatalf("welcome_channels = %#v", encodedChannels)
	}

	clearEnabled := (*bool)(nil)
	var clearChannels []GuildWelcomeScreenChannel
	clearDescription := (*string)(nil)
	encoded, err = json.Marshal(GuildWelcomeScreenParams{
		Enabled:         &clearEnabled,
		WelcomeChannels: &clearChannels,
		Description:     &clearDescription,
	})
	if err != nil {
		t.Fatalf("json.Marshal nulls returned error: %v", err)
	}
	payload = nil
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal nulls returned error: %v", err)
	}
	for _, field := range []string{"enabled", "welcome_channels", "description"} {
		if got := string(payload[field]); got != "null" {
			t.Fatalf("%s = %s, want null", field, got)
		}
	}

	encoded, err = json.Marshal(GuildWelcomeScreenParams{})
	if err != nil {
		t.Fatalf("json.Marshal omitted fields returned error: %v", err)
	}
	payload = nil
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal omitted fields returned error: %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("omitted fields JSON = %s", encoded)
	}
}

func TestGuildCreateScheduledEvents(t *testing.T) {
	var event GuildCreate
	if err := json.Unmarshal([]byte(`{"id":"guild","guild_scheduled_events":[{"id":"event","guild_id":"guild","name":"Town Hall"}]}`), &event); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if event.Guild == nil {
		t.Fatal("Guild is nil")
	}
	if len(event.GuildScheduledEvents) != 1 {
		t.Fatalf("len(GuildScheduledEvents) = %d, want 1", len(event.GuildScheduledEvents))
	}
	if scheduledEvent := event.GuildScheduledEvents[0]; scheduledEvent == nil || scheduledEvent.ID != "event" || scheduledEvent.GuildID != "guild" || scheduledEvent.Name != "Town Hall" {
		t.Fatalf("GuildScheduledEvents[0] = %#v", scheduledEvent)
	}
}

func TestGuildScheduledEventRecurrenceRule(t *testing.T) {
	var event GuildScheduledEvent
	if err := json.Unmarshal([]byte(`{
		"id":"event",
		"guild_id":"guild",
		"scheduled_start_time":"2026-07-15T12:00:00Z",
		"recurrence_rule":{
			"start":"2026-07-15T12:00:00Z",
			"end":"2026-12-31T12:00:00Z",
			"frequency":1,
			"interval":2,
			"by_weekday":[0,4],
			"by_n_weekday":[{"n":4,"day":2}],
			"by_month":[1,7,12],
			"by_month_day":[1,24],
			"by_year_day":[100],
			"count":12
		}
	}`), &event); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	rule := event.RecurrenceRule
	if rule == nil {
		t.Fatal("RecurrenceRule is nil")
	}
	if want := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC); !rule.Start.Equal(want) {
		t.Fatalf("Start = %v, want %v", rule.Start, want)
	}
	if rule.End == nil || !rule.End.Equal(time.Date(2026, time.December, 31, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("End = %v", rule.End)
	}
	if rule.Frequency != GuildScheduledEventRecurrenceRuleFrequencyMonthly || rule.Interval != 2 {
		t.Fatalf("frequency and interval = %d and %d", rule.Frequency, rule.Interval)
	}
	if len(rule.ByWeekday) != 2 || rule.ByWeekday[0] != GuildScheduledEventRecurrenceRuleWeekdayMonday || rule.ByWeekday[1] != GuildScheduledEventRecurrenceRuleWeekdayFriday {
		t.Fatalf("ByWeekday = %#v", rule.ByWeekday)
	}
	if len(rule.ByNWeekday) != 1 || rule.ByNWeekday[0].N != 4 || rule.ByNWeekday[0].Day != GuildScheduledEventRecurrenceRuleWeekdayWednesday {
		t.Fatalf("ByNWeekday = %#v", rule.ByNWeekday)
	}
	if len(rule.ByMonth) != 3 || rule.ByMonth[0] != GuildScheduledEventRecurrenceRuleMonthJanuary || rule.ByMonth[1] != GuildScheduledEventRecurrenceRuleMonthJuly || rule.ByMonth[2] != GuildScheduledEventRecurrenceRuleMonthDecember {
		t.Fatalf("ByMonth = %#v", rule.ByMonth)
	}
	if len(rule.ByMonthDay) != 2 || rule.ByMonthDay[0] != 1 || rule.ByMonthDay[1] != 24 {
		t.Fatalf("ByMonthDay = %#v", rule.ByMonthDay)
	}
	if len(rule.ByYearDay) != 1 || rule.ByYearDay[0] != 100 {
		t.Fatalf("ByYearDay = %#v", rule.ByYearDay)
	}
	if rule.Count == nil || *rule.Count != 12 {
		t.Fatalf("Count = %v", rule.Count)
	}

	if GuildScheduledEventRecurrenceRuleFrequencyYearly != 0 || GuildScheduledEventRecurrenceRuleFrequencyDaily != 3 {
		t.Fatal("recurrence frequency constants do not match Discord values")
	}
	if GuildScheduledEventRecurrenceRuleWeekdayMonday != 0 || GuildScheduledEventRecurrenceRuleWeekdaySunday != 6 {
		t.Fatal("recurrence weekday constants do not match Discord values")
	}
	if GuildScheduledEventRecurrenceRuleMonthJanuary != 1 || GuildScheduledEventRecurrenceRuleMonthDecember != 12 {
		t.Fatal("recurrence month constants do not match Discord values")
	}
}

func TestGuildScheduledEventRecurrenceRuleOmitted(t *testing.T) {
	encoded, err := json.Marshal(GuildScheduledEventParams{})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if _, ok := payload["recurrence_rule"]; ok {
		t.Fatalf("recurrence_rule unexpectedly present in %s", encoded)
	}
}

func TestGuildCreateSoundboardSounds(t *testing.T) {
	var event GuildCreate
	if err := json.Unmarshal([]byte(`{"id":"guild","soundboard_sounds":[{"sound_id":"sound","name":"Airhorn","volume":1,"emoji_id":null,"emoji_name":"📣","guild_id":"guild","available":true}]}`), &event); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if event.Guild == nil {
		t.Fatal("Guild is nil")
	}
	if len(event.SoundboardSounds) != 1 {
		t.Fatalf("len(SoundboardSounds) = %d, want 1", len(event.SoundboardSounds))
	}
	if sound := event.SoundboardSounds[0]; sound == nil || sound.SoundID != "sound" || sound.Name != "Airhorn" || sound.GuildID != "guild" || !sound.Available {
		t.Fatalf("SoundboardSounds[0] = %#v", sound)
	}
}

func TestGuildAuditLogReferenceCollections(t *testing.T) {
	var auditLog GuildAuditLog
	if err := json.Unmarshal([]byte(`{
		"application_commands":[{"id":"command","application_id":"application","name":"ping","description":"Ping"}],
		"auto_moderation_rules":[{"id":"rule","guild_id":"guild","name":"Spam"}],
		"guild_scheduled_events":[{"id":"event","guild_id":"guild","name":"Town Hall"}],
		"threads":[{"id":"thread","guild_id":"guild","type":11,"name":"archived"}]
	}`), &auditLog); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if len(auditLog.ApplicationCommands) != 1 || auditLog.ApplicationCommands[0] == nil || auditLog.ApplicationCommands[0].ID != "command" {
		t.Fatalf("ApplicationCommands = %#v", auditLog.ApplicationCommands)
	}
	if len(auditLog.AutoModerationRules) != 1 || auditLog.AutoModerationRules[0] == nil || auditLog.AutoModerationRules[0].ID != "rule" {
		t.Fatalf("AutoModerationRules = %#v", auditLog.AutoModerationRules)
	}
	if len(auditLog.GuildScheduledEvents) != 1 || auditLog.GuildScheduledEvents[0] == nil || auditLog.GuildScheduledEvents[0].ID != "event" {
		t.Fatalf("GuildScheduledEvents = %#v", auditLog.GuildScheduledEvents)
	}
	if len(auditLog.Threads) != 1 || auditLog.Threads[0] == nil || auditLog.Threads[0].ID != "thread" {
		t.Fatalf("Threads = %#v", auditLog.Threads)
	}
}

func TestCurrentAuditLogActionsAndStatus(t *testing.T) {
	tests := []struct {
		name   string
		action AuditLogAction
		want   AuditLogAction
	}{
		{"soundboard create", AuditLogActionSoundboardSoundCreate, 130},
		{"soundboard update", AuditLogActionSoundboardSoundUpdate, 131},
		{"soundboard delete", AuditLogActionSoundboardSoundDelete, 132},
		{"auto moderation quarantine", AuditLogActionAutoModerationQuarantineUser, 146},
		{"voice channel status create", AuditLogActionVoiceChannelStatusCreate, 192},
		{"voice channel status delete", AuditLogActionVoiceChannelStatusDelete, 193},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.action != test.want {
				t.Fatalf("action = %d, want %d", test.action, test.want)
			}
		})
	}
	if AuditLogActionVoiceChannelStatusUpdate != AuditLogActionVoiceChannelStatusCreate {
		t.Fatalf("VoiceChannelStatusUpdate = %d, want alias of %d", AuditLogActionVoiceChannelStatusUpdate, AuditLogActionVoiceChannelStatusCreate)
	}

	var options AuditLogOptions
	if err := json.Unmarshal([]byte(`{"channel_id":"channel","status":"Town Hall"}`), &options); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if options.ChannelID != "channel" || options.Status != "Town Hall" {
		t.Fatalf("AuditLogOptions = %#v", options)
	}
}

func TestGuildIncidentActionsParamsJSON(t *testing.T) {
	invitesUntil := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	invitesAction := &invitesUntil
	disableDMs := (*time.Time)(nil)

	encoded, err := json.Marshal(GuildIncidentActionsParams{
		InvitesDisabledUntil: &invitesAction,
		DMsDisabledUntil:     &disableDMs,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if got := string(payload["invites_disabled_until"]); got != `"2026-07-11T10:00:00Z"` {
		t.Fatalf("invites_disabled_until = %s", got)
	}
	if got := string(payload["dms_disabled_until"]); got != "null" {
		t.Fatalf("dms_disabled_until = %s, want null", got)
	}

	encoded, err = json.Marshal(GuildIncidentActionsParams{InvitesDisabledUntil: &invitesAction})
	if err != nil {
		t.Fatalf("json.Marshal invite params returned error: %v", err)
	}
	payload = nil
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal invite params returned error: %v", err)
	}
	if _, ok := payload["dms_disabled_until"]; ok {
		t.Fatalf("invite-only params included dms_disabled_until: %s", encoded)
	}
}

func TestActivityUnmarshalApplicationID(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "string",
			data: `{"name":"Rocket League","type":0,"created_at":1511200066000,"application_id":"379286085710381999"}`,
			want: "379286085710381999",
		},
		{
			name: "number",
			data: `{"name":"Rocket League","type":0,"created_at":1511200066000,"application_id":379286085710381999}`,
			want: "379286085710381999",
		},
		{
			name: "null",
			data: `{"name":"Rocket League","type":0,"created_at":1511200066000,"application_id":null}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var activity Activity
			if err := json.Unmarshal([]byte(tt.data), &activity); err != nil {
				t.Fatalf("json.Unmarshal() returned error: %v", err)
			}
			if activity.ApplicationID != tt.want {
				t.Fatalf("ApplicationID = %q, want %q", activity.ApplicationID, tt.want)
			}
		})
	}
}

func TestActivityUnmarshalRichPresenceFields(t *testing.T) {
	const createdAt = int64(1511200066000)
	data := []byte(`{
		"name":"Rocket League",
		"type":0,
		"created_at":1511200066000,
		"application_id":"379286085710381999",
		"status_display_type":2,
		"details":"Ranked Duos",
		"details_url":"https://example.com/details",
		"state":"In a Match",
		"state_url":"https://example.com/state",
		"buttons":["Join","Website"],
		"assets":{
			"large_image":"large-image",
			"large_text":"DFH Stadium",
			"large_url":"https://example.com/large",
			"small_image":"small-image",
			"small_text":"Silver III",
			"small_url":"https://example.com/small",
			"invite_cover_image":"invite-cover"
		}
	}`)

	var activity Activity
	if err := json.Unmarshal(data, &activity); err != nil {
		t.Fatalf("json.Unmarshal() returned error: %v", err)
	}
	if activity.ApplicationID != "379286085710381999" {
		t.Fatalf("ApplicationID = %q", activity.ApplicationID)
	}
	if !activity.CreatedAt.Equal(time.UnixMilli(createdAt)) {
		t.Fatalf("CreatedAt = %v, want %v", activity.CreatedAt, time.UnixMilli(createdAt))
	}
	if activity.StatusDisplayType == nil || *activity.StatusDisplayType != ActivityStatusDisplayTypeDetails {
		t.Fatalf("StatusDisplayType = %v, want %d", activity.StatusDisplayType, ActivityStatusDisplayTypeDetails)
	}
	if activity.DetailsURL != "https://example.com/details" {
		t.Fatalf("DetailsURL = %q", activity.DetailsURL)
	}
	if activity.StateURL != "https://example.com/state" {
		t.Fatalf("StateURL = %q", activity.StateURL)
	}
	if len(activity.Buttons) != 2 || activity.Buttons[0] != "Join" || activity.Buttons[1] != "Website" {
		t.Fatalf("Buttons = %v", activity.Buttons)
	}
	if activity.Assets.LargeURL != "https://example.com/large" {
		t.Fatalf("Assets.LargeURL = %q", activity.Assets.LargeURL)
	}
	if activity.Assets.SmallURL != "https://example.com/small" {
		t.Fatalf("Assets.SmallURL = %q", activity.Assets.SmallURL)
	}
	if activity.Assets.InviteCoverImage != "invite-cover" {
		t.Fatalf("Assets.InviteCoverImage = %q", activity.Assets.InviteCoverImage)
	}
}

func TestForumTagUnmarshalID(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "string",
			data: `{"id":"123456789012345678","name":"open","moderated":true,"emoji_id":"987654321098765432"}`,
			want: "123456789012345678",
		},
		{
			name: "number",
			data: `{"id":1000,"name":"mod queue","moderated":false,"emoji_name":"tag"}`,
			want: "1000",
		},
		{
			name: "null",
			data: `{"id":null,"name":"missing","moderated":false}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tag ForumTag
			if err := json.Unmarshal([]byte(tt.data), &tag); err != nil {
				t.Fatalf("json.Unmarshal() returned error: %v", err)
			}
			if tag.ID != tt.want {
				t.Fatalf("ID = %q, want %q", tag.ID, tt.want)
			}
		})
	}
}

func TestApplicationCurrentFieldsAndIntegrationTypesConfig(t *testing.T) {
	data := []byte(`{
		"id":"app",
		"name":"Application",
		"description":"desc",
		"verify_key":"key",
		"flags":8192,
		"flags_new":"1099511635968",
		"approximate_guild_count":12,
		"approximate_user_install_count":34,
		"approximate_user_authorization_count":56,
		"redirect_uris":["https://example.com/callback"],
		"interactions_endpoint_url":"https://example.com/interactions",
		"role_connections_verification_url":"https://example.com/role",
		"event_webhooks_url":"https://example.com/events",
		"event_webhooks_status":2,
		"event_webhooks_types":["APPLICATION_AUTHORIZED"],
		"tags":["utility"],
		"custom_install_url":"https://example.com/install",
		"integration_types_config":{
			"0":{"oauth2_install_params":{"scopes":["bot"],"permissions":"2048"}},
			"1":{"oauth2_install_params":{"scopes":["applications.commands"],"permissions":"0"}}
		},
		"install_params":{"scopes":["bot"],"permissions":"8"},
		"bot":{"id":"bot","username":"Bot"},
		"guild":{"id":"guild","name":"Guild"}
	}`)

	var app Application
	if err := json.Unmarshal(data, &app); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if app.FlagsNew != "1099511635968" {
		t.Fatalf("FlagsNew = %q", app.FlagsNew)
	}
	if app.ApproximateUserInstallCount != 34 || app.ApproximateUserAuthorizationCount != 56 {
		t.Fatalf("approximate user counts = %d/%d", app.ApproximateUserInstallCount, app.ApproximateUserAuthorizationCount)
	}
	if app.Bot == nil || app.Bot.ID != "bot" || app.Guild == nil || app.Guild.ID != "guild" {
		t.Fatalf("bot/guild = %#v/%#v", app.Bot, app.Guild)
	}
	if app.IntegrationTypesConfig[ApplicationIntegrationGuildInstall].OAuth2InstallParams.Permissions != 2048 {
		t.Fatalf("guild install permissions = %d", app.IntegrationTypesConfig[ApplicationIntegrationGuildInstall].OAuth2InstallParams.Permissions)
	}

	encoded, err := json.Marshal(Application{
		Name:        "Application",
		Description: "desc",
		VerifyKey:   "key",
		IntegrationTypesConfig: map[ApplicationIntegrationType]*ApplicationIntegrationTypeConfig{
			ApplicationIntegrationUserInstall: {
				OAuth2InstallParams: &ApplicationInstallParams{
					Scopes:      []string{"applications.commands"},
					Permissions: 0,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	encodedText := string(encoded)
	if !strings.Contains(encodedText, `"integration_types_config"`) {
		t.Fatalf("encoded application missing integration_types_config: %s", encodedText)
	}
	if strings.Contains(encodedText, `"integration_types"`) {
		t.Fatalf("encoded application used old integration_types tag: %s", encodedText)
	}
}

func TestInviteCurrentFieldsAndTargetUsersJob(t *testing.T) {
	data := []byte(`{
		"type":0,
		"code":"invite",
		"target_type":2,
		"target_application_id":"app",
		"flags":1,
		"roles":[{"id":"role","name":"Role","position":1,"color":1,"colors":{"primary_color":1,"secondary_color":2,"tertiary_color":null},"icon":"icon","unicode_emoji":"ok"}],
		"role_ids":["role"],
		"guild_scheduled_event":{"id":"event","guild_id":"guild","channel_id":"channel","name":"Event","description":"desc","scheduled_start_time":"2026-01-01T00:00:00Z","privacy_level":2,"status":1,"entity_type":1}
	}`)

	var invite Invite
	if err := json.Unmarshal(data, &invite); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if invite.Type != InviteTypeGuild || invite.Flags != InviteFlagIsGuestInvite {
		t.Fatalf("invite type/flags = %d/%d", invite.Type, invite.Flags)
	}
	if len(invite.Roles) != 1 || invite.Roles[0].Colors == nil || invite.Roles[0].Colors.SecondaryColor == nil {
		t.Fatalf("roles = %#v", invite.Roles)
	}
	if invite.GuildScheduledEvent == nil || invite.GuildScheduledEvent.ID != "event" {
		t.Fatalf("GuildScheduledEvent = %#v", invite.GuildScheduledEvent)
	}

	var job InviteTargetUsersJob
	if err := json.Unmarshal([]byte(`{"status":3,"total_users":100,"processed_users":41,"created_at":"2025-01-08T12:00:00Z","completed_at":null,"error_message":"Failed"}`), &job); err != nil {
		t.Fatalf("json.Unmarshal job returned error: %v", err)
	}
	if job.Status != InviteTargetUsersJobStatusFailed || job.CompletedAt != nil || job.ErrorMessage != "Failed" {
		t.Fatalf("job = %#v", job)
	}
}

func TestApplicationEditParamsNullableFields(t *testing.T) {
	image := "data:image/png;base64,image"
	webhookURL := "https://example.com/events"
	empty := ""
	tests := []struct {
		name   string
		params ApplicationEditParams
		want   string
	}{
		{
			name: "omits unset images",
			want: `{}`,
		},
		{
			name:   "sets nullable fields",
			params: ApplicationEditParams{Icon: &image, CoverImage: &image, EventWebhooksURL: &webhookURL},
			want:   `{"icon":"data:image/png;base64,image","cover_image":"data:image/png;base64,image","event_webhooks_url":"https://example.com/events"}`,
		},
		{
			name:   "clears nullable fields",
			params: ApplicationEditParams{Icon: &empty, CoverImage: &empty, EventWebhooksURL: &empty},
			want:   `{"icon":null,"cover_image":null,"event_webhooks_url":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.params)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func TestApplicationWebhookEventStatusValues(t *testing.T) {
	tests := []struct {
		name string
		got  ApplicationWebhookEventStatus
		want ApplicationWebhookEventStatus
	}{
		{name: "disabled", got: ApplicationWebhookEventStatusDisabled, want: 1},
		{name: "enabled", got: ApplicationWebhookEventStatusEnabled, want: 2},
		{name: "disabled by Discord", got: ApplicationWebhookEventStatusDisabledByDiscord, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("status = %d, want %d", tt.got, tt.want)
			}
		})
	}
}

func TestApplicationEventWebhookStatusCompatibility(t *testing.T) {
	var status ApplicationEventWebhookStatus = ApplicationEventWebhookStatusEnabled
	if status != ApplicationWebhookEventStatusEnabled {
		t.Fatalf("legacy status = %d, want %d", status, ApplicationWebhookEventStatusEnabled)
	}
	if ApplicationEventWebhookStatusDisabled != ApplicationWebhookEventStatusDisabled ||
		ApplicationEventWebhookStatusDisabledByDiscord != ApplicationWebhookEventStatusDisabledByDiscord {
		t.Fatal("legacy application webhook status constants changed")
	}
}

func TestCurrentChannelObjectFields(t *testing.T) {
	if VideoQualityModeAuto != 1 || VideoQualityModeFull != 2 {
		t.Fatalf("video quality modes = %d/%d, want 1/2", VideoQualityModeAuto, VideoQualityModeFull)
	}

	const payload = `{
		"id":"channel",
		"type":2,
		"managed":true,
		"rtc_region":"us-central",
		"video_quality_mode":2,
		"default_auto_archive_duration":1440,
		"permissions":"4503599627370496",
		"total_message_sent":73,
		"thread_metadata":{
			"archived":false,
			"auto_archive_duration":1440,
			"archive_timestamp":"2026-07-12T12:00:00Z",
			"locked":false,
			"invitable":true,
			"create_timestamp":"2026-07-11T10:30:00.123Z"
		}
	}`

	var channel Channel
	if err := json.Unmarshal([]byte(payload), &channel); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !channel.Managed {
		t.Fatal("Managed = false, want true")
	}
	if channel.RTCRegion == nil || *channel.RTCRegion != "us-central" {
		t.Fatalf("RTCRegion = %v, want us-central", channel.RTCRegion)
	}
	if channel.VideoQualityMode != VideoQualityModeFull {
		t.Fatalf("VideoQualityMode = %d, want %d", channel.VideoQualityMode, VideoQualityModeFull)
	}
	if channel.DefaultAutoArchiveDuration != 1440 {
		t.Fatalf("DefaultAutoArchiveDuration = %d, want 1440", channel.DefaultAutoArchiveDuration)
	}
	if channel.Permissions != 1<<52 {
		t.Fatalf("Permissions = %d, want %d", channel.Permissions, int64(1<<52))
	}
	if channel.TotalMessageSent != 73 {
		t.Fatalf("TotalMessageSent = %d, want 73", channel.TotalMessageSent)
	}
	if channel.ThreadMetadata == nil || channel.ThreadMetadata.CreateTimestamp == nil {
		t.Fatalf("ThreadMetadata = %#v, want create timestamp", channel.ThreadMetadata)
	}
	wantCreated := time.Date(2026, 7, 11, 10, 30, 0, 123000000, time.UTC)
	if !channel.ThreadMetadata.CreateTimestamp.Equal(wantCreated) {
		t.Fatalf("CreateTimestamp = %v, want %v", channel.ThreadMetadata.CreateTimestamp, wantCreated)
	}

	encoded, err := json.Marshal(channel)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal round trip returned error: %v", err)
	}
	if got := string(roundTrip["permissions"]); got != `"4503599627370496"` {
		t.Fatalf("permissions JSON = %s, want string-encoded permissions", got)
	}

	var nullable Channel
	if err := json.Unmarshal([]byte(`{"rtc_region":null,"thread_metadata":{"create_timestamp":null}}`), &nullable); err != nil {
		t.Fatalf("json.Unmarshal nullable fields returned error: %v", err)
	}
	if nullable.RTCRegion != nil || nullable.ThreadMetadata == nil || nullable.ThreadMetadata.CreateTimestamp != nil {
		t.Fatalf("nullable channel = %#v", nullable)
	}
}

func TestCurrentChannelFlagValuesAndJSON(t *testing.T) {
	tests := []struct {
		name     string
		got      int
		want     int
		wantJSON string
	}{
		{
			name:     "hide media download options",
			got:      int(ChannelFlagHideMediaDownloadOptions),
			want:     1 << 15,
			wantJSON: `{"flags":32768}`,
		},
		{
			name:     "suppress role subscription purchase notifications",
			got:      int(SystemChannelFlagsSuppressRoleSubscriptionPurchaseNotifications),
			want:     1 << 4,
			wantJSON: `{"flags":16}`,
		},
		{
			name:     "suppress role subscription purchase notification replies",
			got:      int(SystemChannelFlagsSuppressRoleSubscriptionPurchaseNotificationReplies),
			want:     1 << 5,
			wantJSON: `{"flags":32}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("flag = %d, want %d", tt.got, tt.want)
			}
			encoded, err := json.Marshal(struct {
				Flags int `json:"flags"`
			}{Flags: tt.got})
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			if string(encoded) != tt.wantJSON {
				t.Fatalf("JSON = %s, want %s", encoded, tt.wantJSON)
			}
		})
	}
}

func TestCurrentAutoModerationVariants(t *testing.T) {
	constants := []struct {
		name string
		got  int
		want int
	}{
		{name: "member update event", got: int(AutoModerationEventMemberUpdate), want: 2},
		{name: "mention spam trigger", got: int(AutoModerationEventTriggerMentionSpam), want: 5},
		{name: "member profile trigger", got: int(AutoModerationEventTriggerMemberProfile), want: 6},
		{name: "block member interaction action", got: int(AutoModerationRuleActionBlockMemberInteraction), want: 4},
	}
	for _, tt := range constants {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("value = %d, want %d", tt.got, tt.want)
			}
		})
	}

	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("mention raid protection %t", enabled), func(t *testing.T) {
			metadata := AutoModerationTriggerMetadata{MentionRaidProtectionEnabled: &enabled}
			encoded, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatalf("json.Unmarshal payload returned error: %v", err)
			}
			if got := string(payload["mention_raid_protection_enabled"]); got != fmt.Sprint(enabled) {
				t.Fatalf("mention_raid_protection_enabled = %s, want %t", got, enabled)
			}

			var decoded AutoModerationTriggerMetadata
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if decoded.MentionRaidProtectionEnabled == nil || *decoded.MentionRaidProtectionEnabled != enabled {
				t.Fatalf("MentionRaidProtectionEnabled = %v, want %t", decoded.MentionRaidProtectionEnabled, enabled)
			}
		})
	}
}

func TestRoleColorsMemberParamsAndSubscriptionConstants(t *testing.T) {
	secondary := 0x112233
	tertiary := 0x445566
	encodedRole, err := json.Marshal(RoleParams{
		Colors: &RoleColors{
			PrimaryColor:   0x010203,
			SecondaryColor: &secondary,
			TertiaryColor:  &tertiary,
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal role returned error: %v", err)
	}
	if !strings.Contains(string(encodedRole), `"colors":{"primary_color":66051,"secondary_color":1122867,"tertiary_color":4478310}`) {
		t.Fatalf("role colors JSON = %s", encodedRole)
	}

	flags := MemberFlagBypassesVerification | MemberFlagAutomodQuarantinedGuildTag
	avatar := "data:image/png;base64,avatar"
	banner := "data:image/png;base64,banner"
	bio := "guild bio"
	encodedMember, err := json.Marshal(GuildMemberParams{
		Flags:  &flags,
		Avatar: &avatar,
		Banner: &banner,
		Bio:    &bio,
	})
	if err != nil {
		t.Fatalf("json.Marshal member returned error: %v", err)
	}
	encodedMemberText := string(encodedMember)
	for _, want := range []string{`"flags":1028`, `"avatar":"data:image/png;base64,avatar"`, `"banner":"data:image/png;base64,banner"`, `"bio":"guild bio"`} {
		if !strings.Contains(encodedMemberText, want) {
			t.Fatalf("member params JSON missing %s: %s", want, encodedMemberText)
		}
	}

	if SubscriptionStatusInactive != 1 || SubscriptionStatusEnding != 2 {
		t.Fatalf("subscription statuses inactive/ending = %d/%d", SubscriptionStatusInactive, SubscriptionStatusEnding)
	}
}

func TestSoundboardSoundStructures(t *testing.T) {
	var sound SoundboardSound
	if err := json.Unmarshal([]byte(`{"name":"Yay","sound_id":"1106714396018884649","volume":1,"emoji_id":"989193655938064464","emoji_name":null,"guild_id":"613425648685547541","available":true,"user":{"id":"user","username":"User"}}`), &sound); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if sound.SoundID != "1106714396018884649" || sound.User == nil || sound.User.ID != "user" {
		t.Fatalf("sound = %#v", sound)
	}

	volume := 0.5
	emojiName := "sound"
	encoded, err := json.Marshal(SoundboardSoundParams{
		Name:      "Yay",
		Sound:     "data:audio/ogg;base64,AAAA",
		Volume:    &volume,
		EmojiName: &emojiName,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	for _, want := range []string{`"name":"Yay"`, `"sound":"data:audio/ogg;base64,AAAA"`, `"volume":0.5`, `"emoji_name":"sound"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("sound params JSON missing %s: %s", want, encoded)
		}
	}
}

func TestGuildStickerEditParamsDescription(t *testing.T) {
	description := "A wave"
	tests := []struct {
		name        string
		params      GuildStickerEditParams
		wantPresent bool
		want        interface{}
	}{
		{name: "omitted", params: GuildStickerEditParams{}},
		{name: "value", params: GuildStickerEditParams{Description: &description}, wantPresent: true, want: description},
		{name: "null", params: GuildStickerEditParams{DescriptionNull: true}, wantPresent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.params)
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			got, present := payload["description"]
			if present != tt.wantPresent || got != tt.want {
				t.Fatalf("description = %#v, present = %t", got, present)
			}
		})
	}
}

func TestApplicationActivityInstance(t *testing.T) {
	var instance ApplicationActivityInstance
	if err := json.Unmarshal([]byte(`{"application_id":"app","instance_id":"instance","launch_id":"launch","location":{"id":"gc-guild-channel","kind":"gc","channel_id":"channel","guild_id":"guild"},"users":["user"]}`), &instance); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if instance.Location == nil || instance.Location.Kind != ApplicationActivityLocationGuildChannel {
		t.Fatalf("Location = %#v", instance.Location)
	}
	if len(instance.Users) != 1 || instance.Users[0] != "user" {
		t.Fatalf("Users = %#v", instance.Users)
	}
}
