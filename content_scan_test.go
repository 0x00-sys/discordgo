package discordgo

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestContentScanningFlagsJSON(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wire       int
		attachment MessageAttachmentFlags
		embed      MessageEmbedFlags
		mediaWire  int
		media      ContentScanMetadataFlags
	}{
		{"explicit", 16, MessageAttachmentFlagsContainsExplicitContent, MessageEmbedFlagsContainsExplicitContent, 1, ContentScanMetadataFlagsIsExplicitContent},
		{"gore", 64, MessageAttachmentFlagsContainsGoreContent, MessageEmbedFlagsContainsGoreContent, 2, ContentScanMetadataFlagsIsGoreContent},
		{"self harm", 128, MessageAttachmentFlagsContainsSelfHarmContent, MessageEmbedFlagsContainsSelfHarmContent, 4, ContentScanMetadataFlagsIsSelfHarmContent},
		{"combined", 208, MessageAttachmentFlagsContainsExplicitContent | MessageAttachmentFlagsContainsGoreContent | MessageAttachmentFlagsContainsSelfHarmContent,
			MessageEmbedFlagsContainsExplicitContent | MessageEmbedFlagsContainsGoreContent | MessageEmbedFlagsContainsSelfHarmContent, 7,
			ContentScanMetadataFlagsIsExplicitContent | ContentScanMetadataFlagsIsGoreContent | ContentScanMetadataFlagsIsSelfHarmContent},
		{"other flags", 32, MessageAttachmentFlagsIsAnimated, MessageEmbedFlagsIsContentInventoryEntry, 0, 0},
		{"unknown bits", 1048592, MessageAttachmentFlagsContainsExplicitContent | 1<<20, MessageEmbedFlagsContainsExplicitContent | 1<<20, 1048577, ContentScanMetadataFlagsIsExplicitContent | 1<<20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type payload struct {
				Attachment MessageAttachment         `json:"attachment"`
				Embed      MessageEmbed              `json:"embed"`
				Media      UnfurledMediaItem         `json:"media"`
				Resolved   ResolvedUnfurledMediaItem `json:"resolved"`
			}
			raw := fmt.Sprintf(`{"attachment":{"flags":%d},"embed":{"flags":%d},"media":{"content_scan_metadata":{"flags":%d}},"resolved":{"content_scan_metadata":{"flags":%d}}}`, tc.wire, tc.wire, tc.mediaWire, tc.mediaWire)
			var got payload
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			if got.Attachment.Flags != tc.attachment || got.Embed.Flags != tc.embed {
				t.Fatalf("attachment/embed flags = %d/%d, want %d/%d", got.Attachment.Flags, got.Embed.Flags, tc.attachment, tc.embed)
			}
			for _, metadata := range []*ContentScanMetadata{got.Media.ContentScanMetadata, got.Resolved.ContentScanMetadata} {
				if metadata == nil || metadata.Flags != tc.media {
					t.Fatalf("scan metadata = %+v, want flags %d", metadata, tc.media)
				}
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var roundTrip payload
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, roundTrip) {
				t.Fatal("content scanning fields changed after JSON round trip")
			}
		})
	}
}

func TestContentScanMetadataPresence(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want *ContentScanMetadata
	}{
		{"absent", `{"url":"attachment://image.png"}`, nil},
		{"null", `{"url":"attachment://image.png","content_scan_metadata":null}`, nil},
		{"zero flags", `{"url":"attachment://image.png","content_scan_metadata":{"flags":0}}`, &ContentScanMetadata{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var media UnfurledMediaItem
			var resolved ResolvedUnfurledMediaItem
			for _, target := range []interface{}{&media, &resolved} {
				if err := json.Unmarshal([]byte(tc.raw), target); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(target)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				metadata, present := fields["content_scan_metadata"]
				if present != (tc.want != nil) || (present && string(metadata) != `{"flags":0}`) {
					t.Fatalf("%T marshaled scan metadata = %s, present = %t", target, metadata, present)
				}
			}
			if !reflect.DeepEqual(media.ContentScanMetadata, tc.want) || !reflect.DeepEqual(resolved.ContentScanMetadata, tc.want) {
				t.Fatalf("unexpected metadata: media %+v, resolved %+v", media.ContentScanMetadata, resolved.ContentScanMetadata)
			}
		})
	}
	data, err := json.Marshal(MessageEmbed{Title: "existing embed"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"title":"existing embed"}` {
		t.Fatalf("zero embed flags changed existing JSON: %s", data)
	}
}

func TestMessageUpdateContentScanning(t *testing.T) {
	var initial MessageCreate
	if err := json.Unmarshal([]byte(`{
		"id":"message","channel_id":"channel","content":"media",
		"attachments":[{"id":"attachment"}],"embeds":[{"type":"image"}],
		"components":[{"type":12,"items":[{"media":{"url":"attachment://image.png"}}]}]
	}`), &initial); err != nil {
		t.Fatal(err)
	}
	state := newMessageUpdateState(t, initial.Message)
	update := unmarshalMessageUpdate(t, `{
		"id":"message","channel_id":"channel",
		"attachments":[{"id":"attachment","flags":208}],
		"embeds":[{"type":"components","flags":208,"components":[
			{"type":17,"components":[{"type":13,"file":{"url":"attachment://image.png","content_scan_metadata":{"flags":4}}}]}]}],
		"components":[
			{"type":12,"items":[{"media":{"url":"attachment://image.png","content_scan_metadata":{"flags":1}}}]},
			{"type":9,"components":[{"type":10,"content":"preview"}],"accessory":{"type":11,"media":{"url":"attachment://image.png","content_scan_metadata":{"flags":2}}}}
		]
	}`)
	if err := state.OnInterface(&Session{StateEnabled: true}, update); err != nil {
		t.Fatal(err)
	}
	cached, err := state.Message("channel", "message")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []*Message{update.Message, cached} {
		if len(message.Attachments) != 1 || message.Attachments[0].Flags != 208 || len(message.Embeds) != 1 || message.Embeds[0].Flags != 208 {
			t.Fatalf("message did not retain attachment/embed flags: %+v", message)
		}
		gallery := message.Components[0].(*MediaGallery)
		thumbnail := message.Components[1].(*Section).Accessory.(*Thumbnail)
		file := message.Embeds[0].Components[0].(*Container).Components[0].(*FileComponent)
		for _, tc := range []struct {
			metadata *ContentScanMetadata
			want     ContentScanMetadataFlags
		}{
			{gallery.Items[0].Media.ContentScanMetadata, ContentScanMetadataFlagsIsExplicitContent},
			{thumbnail.Media.ContentScanMetadata, ContentScanMetadataFlagsIsGoreContent},
			{file.File.ContentScanMetadata, ContentScanMetadataFlagsIsSelfHarmContent},
		} {
			if tc.metadata == nil || tc.metadata.Flags != tc.want {
				t.Fatalf("nested scan metadata = %+v, want flags %d", tc.metadata, tc.want)
			}
		}
	}
	before := update.BeforeUpdate
	if cached.Content != "media" || before == nil || before.Attachments[0].Flags != 0 || before.Embeds[0].Flags != 0 || before.Components[0].(*MediaGallery).Items[0].Media.ContentScanMetadata != nil {
		t.Fatal("scan update changed omitted content or the pre-update message")
	}
}
