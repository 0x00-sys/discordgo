package discordgo

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestChannelEditMarshalNullableFields(t *testing.T) {
	guildText := ChannelTypeGuildText
	guildNews := ChannelTypeGuildNews
	icon := "data:image/png;base64,aWNvbg=="
	iconValue := &icon
	rtcRegion := "us-central"
	rtcRegionValue := &rtcRegion
	var nullString *string
	videoQualityMode := VideoQualityModeFull
	defaultAutoArchiveDuration := 1440

	tests := []struct {
		name string
		edit ChannelEdit
		want string
	}{
		{
			name: "omits unset nullable fields",
			edit: ChannelEdit{Name: "ticket"},
			want: `{"name":"ticket"}`,
		},
		{
			name: "sets zero channel type",
			edit: ChannelEdit{Type: &guildText},
			want: `{"type":0}`,
		},
		{
			name: "sets nonzero channel type",
			edit: ChannelEdit{Type: &guildNews},
			want: `{"type":5}`,
		},
		{
			name: "sets group dm icon",
			edit: ChannelEdit{Icon: &iconValue},
			want: `{"icon":"data:image/png;base64,aWNvbg=="}`,
		},
		{
			name: "clears group dm icon",
			edit: ChannelEdit{Icon: &nullString},
			want: `{"icon":null}`,
		},
		{
			name: "keeps non-empty topic",
			edit: ChannelEdit{Topic: "updates"},
			want: `{"topic":"updates"}`,
		},
		{
			name: "clears topic",
			edit: ChannelEdit{Topic: "ignored", TopicNull: true},
			want: `{"topic":null}`,
		},
		{
			name: "keeps nonzero user limit",
			edit: ChannelEdit{UserLimit: 25},
			want: `{"user_limit":25}`,
		},
		{
			name: "omits unset zero user limit",
			edit: ChannelEdit{UserLimit: 0},
			want: `{}`,
		},
		{
			name: "sets zero user limit",
			edit: ChannelEdit{UserLimitSet: true},
			want: `{"user_limit":0}`,
		},
		{
			name: "sets rtc region",
			edit: ChannelEdit{RTCRegion: &rtcRegionValue},
			want: `{"rtc_region":"us-central"}`,
		},
		{
			name: "clears rtc region",
			edit: ChannelEdit{RTCRegion: &nullString},
			want: `{"rtc_region":null}`,
		},
		{
			name: "sets video quality mode",
			edit: ChannelEdit{VideoQualityMode: &videoQualityMode},
			want: `{"video_quality_mode":2}`,
		},
		{
			name: "sets default auto archive duration",
			edit: ChannelEdit{DefaultAutoArchiveDuration: &defaultAutoArchiveDuration},
			want: `{"default_auto_archive_duration":1440}`,
		},
		{
			name: "keeps non-empty parent id",
			edit: ChannelEdit{ParentID: "category"},
			want: `{"parent_id":"category"}`,
		},
		{
			name: "clears parent id",
			edit: ChannelEdit{ParentID: "ignored", ParentIDNull: true},
			want: `{"parent_id":null}`,
		},
		{
			name: "keeps empty permission overwrites",
			edit: ChannelEdit{PermissionOverwrites: []*PermissionOverwrite{}},
			want: `{"permission_overwrites":[]}`,
		},
		{
			name: "clears parent and keeps empty permission overwrites",
			edit: ChannelEdit{ParentIDNull: true, PermissionOverwrites: []*PermissionOverwrite{}},
			want: `{"parent_id":null,"permission_overwrites":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.edit)
			if err != nil {
				t.Fatalf("Marshal returned error: %v", err)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func TestChannelEditRequestAndResponse(t *testing.T) {
	guildText := ChannelTypeGuildText
	videoQualityMode := VideoQualityModeFull
	defaultAutoArchiveDuration := 1440
	var rtcRegion *string
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPatch)
		}
		if r.URL.Path != "/channels/channel" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/channels/channel")
		}
		if reason := r.Header.Get("X-Audit-Log-Reason"); reason != "channel parity" {
			t.Fatalf("X-Audit-Log-Reason = %q, want %q", reason, "channel parity")
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		assertJSONEqual(t, body, []byte(`{
			"type":0,
			"topic":null,
			"user_limit":0,
			"rtc_region":null,
			"video_quality_mode":2,
			"default_auto_archive_duration":1440
		}`))

		_, _ = w.Write([]byte(`{
			"id":"channel",
			"guild_id":"guild",
			"name":"general",
			"type":0,
			"topic":null,
			"user_limit":0,
			"rtc_region":null,
			"video_quality_mode":2,
			"default_auto_archive_duration":1440
		}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointChannels := EndpointChannels
	EndpointChannels = server.URL + "/channels/"
	t.Cleanup(func() {
		EndpointChannels = oldEndpointChannels
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	channel, err := session.ChannelEdit("channel", &ChannelEdit{
		Type:                       &guildText,
		TopicNull:                  true,
		UserLimitSet:               true,
		RTCRegion:                  &rtcRegion,
		VideoQualityMode:           &videoQualityMode,
		DefaultAutoArchiveDuration: &defaultAutoArchiveDuration,
	}, WithAuditLogReason("channel parity"))
	if err != nil {
		t.Fatalf("ChannelEdit returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if channel == nil || channel.ID != "channel" || channel.GuildID != "guild" || channel.Type != ChannelTypeGuildText {
		t.Fatalf("channel = %#v, want edited guild text channel", channel)
	}
	if channel.RTCRegion != nil || channel.VideoQualityMode != VideoQualityModeFull {
		t.Fatalf("voice settings = %#v/%d, want automatic region and full video", channel.RTCRegion, channel.VideoQualityMode)
	}
	if channel.UserLimit != 0 || channel.DefaultAutoArchiveDuration != 1440 {
		t.Fatalf("channel limits = %d/%d, want 0/1440", channel.UserLimit, channel.DefaultAutoArchiveDuration)
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotJSON interface{}
	if err := json.Unmarshal(got, &gotJSON); err != nil {
		t.Fatalf("got is invalid JSON: %v", err)
	}

	var wantJSON interface{}
	if err := json.Unmarshal(want, &wantJSON); err != nil {
		t.Fatalf("want is invalid JSON: %v", err)
	}

	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
