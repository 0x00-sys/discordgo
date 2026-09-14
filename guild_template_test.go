package discordgo

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Discord's template example uses integer placeholder IDs and permissions.
// https://docs.discord.com/developers/resources/guild-template#guild-template-object
const guildTemplateSnapshotJSON = `{"code":"template","source_guild_id":"678070694164299796","serialized_source_guild":{"name":"Friends & Family","afk_channel_id":null,"system_channel_id":2,"roles":[{"id":0,"name":"@everyone","permissions":104324689}],"channels":[{"id":1,"name":"Text Channels","type":4,"parent_id":null},{"id":2,"name":"general","type":0,"parent_id":1,"permission_overwrites":[{"id":0,"type":0,"allow":9007199254740993,"deny":"1024"}]}]}}`

func TestGuildTemplatePlaceholderIDs(t *testing.T) {
	var template GuildTemplate
	if err := json.Unmarshal([]byte(guildTemplateSnapshotJSON), &template); err != nil {
		t.Fatal(err)
	}
	guild := template.SerializedSourceGuild
	if template.Code != "template" || template.SourceGuildID != "678070694164299796" || guild == nil {
		t.Fatalf("template = %#v", template)
	}
	if guild.Name != "Friends & Family" || guild.AfkChannelID != "" || guild.SystemChannelID != "2" {
		t.Fatalf("guild = %#v", guild)
	}
	if len(guild.Roles) != 1 || guild.Roles[0].ID != "0" || guild.Roles[0].Permissions != 104324689 {
		t.Fatalf("roles = %#v", guild.Roles)
	}
	if len(guild.Channels) != 2 || guild.Channels[0].ID != "1" || guild.Channels[0].ParentID != "" || guild.Channels[1].ID != "2" || guild.Channels[1].ParentID != "1" {
		t.Fatalf("channels = %#v", guild.Channels)
	}
	overwrite := guild.Channels[1].PermissionOverwrites[0]
	if overwrite.ID != "0" || overwrite.Allow != 9007199254740993 || overwrite.Deny != 1024 {
		t.Fatalf("overwrite = %#v", overwrite)
	}
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip GuildTemplate
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.SerializedSourceGuild.Channels[1].PermissionOverwrites[0].Allow != overwrite.Allow {
		t.Fatal("round trip lost permission precision")
	}
}

func TestGuildTemplateRESTPlaceholderIDs(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := guildTemplateSnapshotJSON
		if strings.HasSuffix(r.URL.Path, "/templates") && r.Method == http.MethodGet {
			body = "[" + body + "]"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	checks := map[string]func() (*GuildTemplate, error){
		"get": func() (*GuildTemplate, error) { return session.GuildTemplate("template") },
		"create": func() (*GuildTemplate, error) {
			return session.GuildTemplateCreateWithError("guild", &GuildTemplateParams{Name: "test"})
		},
		"edit": func() (*GuildTemplate, error) {
			return session.GuildTemplateEdit("guild", "template", &GuildTemplateParams{Name: "test"})
		},
		"list": func() (*GuildTemplate, error) {
			templates, err := session.GuildTemplates("guild")
			if err != nil {
				return nil, err
			}
			if len(templates) != 1 {
				t.Fatalf("templates = %#v", templates)
			}
			return templates[0], nil
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			template, err := check()
			if err != nil {
				t.Fatal(err)
			}
			if template.SerializedSourceGuild.SystemChannelID != "2" {
				t.Fatal("placeholder was not decoded")
			}
		})
	}
}

func TestGuildTemplateSnapshotValidation(t *testing.T) {
	for _, snapshot := range []string{
		`null`, `{}`, `{"roles":null,"channels":[null]}`,
		`{"system_channel_id":"2","roles":[{"id":"0","permissions":"104324689"}]}`,
	} {
		var template GuildTemplate
		if err := json.Unmarshal([]byte(`{"serialized_source_guild":`+snapshot+`}`), &template); err != nil {
			t.Errorf("valid snapshot %s: %v", snapshot, err)
		}
	}
	for _, snapshot := range []string{
		`[]`, `{"id":true}`, `{"id":1.5}`, `{"id":-1}`,
		`{"id":18446744073709551616}`, `{"roles":{}}`,
		`{"roles":[{"permissions":9223372036854775808}]}`,
		`{"channels":[{"permission_overwrites":[{"allow":false}]}]}`,
	} {
		var template GuildTemplate
		if err := json.Unmarshal([]byte(`{"serialized_source_guild":`+snapshot+`}`), &template); err == nil {
			t.Errorf("invalid snapshot accepted: %s", snapshot)
		}
	}
	var guild Guild
	if err := json.Unmarshal([]byte(`{"roles":[{"id":0,"permissions":104324689}]}`), &guild); err == nil {
		t.Fatal("ordinary guild decoding unexpectedly accepts template placeholders")
	}
}
