package discordgo

import (
	"encoding/json"
	"testing"
)

func TestComponentsV2PublicResponseFields(t *testing.T) {
	raw := []byte(`{
		"type": 17,
		"id": 1,
		"components": [
			{
				"type": 10,
				"id": 2,
				"content": "hello"
			},
			{
				"type": 12,
				"id": 3,
				"items": [
					{
						"media": {
							"url": "attachment://clip.mp4",
							"proxy_url": "https://cdn.example/clip.mp4",
							"width": 1280,
							"height": 720,
							"content_type": "video/mp4",
							"attachment_id": "100",
							"loading_state": 2,
							"flags": 4,
							"placeholder": "blurhash"
						},
						"description": "demo clip",
						"spoiler": true
					}
				]
			},
			{
				"type": 13,
				"id": 4,
				"file": {
					"url": "attachment://report.pdf",
					"content_type": "application/pdf"
				},
				"name": "report.pdf",
				"size": 2048,
				"spoiler": true
			}
		]
	}`)

	component, err := MessageComponentFromJSON(raw)
	if err != nil {
		t.Fatalf("MessageComponentFromJSON returned error: %v", err)
	}

	container, ok := component.(*Container)
	if !ok {
		t.Fatalf("component = %T, want *Container", component)
	}
	if len(container.Components) != 3 {
		t.Fatalf("len(container.Components) = %d, want 3", len(container.Components))
	}

	text, ok := container.Components[0].(*TextDisplay)
	if !ok {
		t.Fatalf("container.Components[0] = %T, want *TextDisplay", container.Components[0])
	}
	if text.ID != 2 || text.Content != "hello" {
		t.Fatalf("text display = %#v, want ID 2 and content hello", text)
	}

	gallery, ok := container.Components[1].(*MediaGallery)
	if !ok {
		t.Fatalf("container.Components[1] = %T, want *MediaGallery", container.Components[1])
	}
	if len(gallery.Items) != 1 {
		t.Fatalf("len(gallery.Items) = %d, want 1", len(gallery.Items))
	}
	media := gallery.Items[0].Media
	if media.URL != "attachment://clip.mp4" ||
		media.ProxyURL != "https://cdn.example/clip.mp4" ||
		media.Width != 1280 ||
		media.Height != 720 ||
		media.ContentType != "video/mp4" ||
		media.AttachmentID != "100" ||
		media.LoadingState != UnfurledMediaItemLoadingStateLoadingSuccess ||
		media.Flags != MessageAttachmentFlagsIsRemix ||
		media.Placeholder != "blurhash" {
		t.Fatalf("media = %#v, want Discord response fields preserved", media)
	}

	file, ok := container.Components[2].(*FileComponent)
	if !ok {
		t.Fatalf("container.Components[2] = %T, want *FileComponent", container.Components[2])
	}
	if file.Name != "report.pdf" || file.Size != 2048 || file.File.ContentType != "application/pdf" {
		t.Fatalf("file = %#v, want response name, size, and content type preserved", file)
	}

	data, err := json.Marshal(component)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var remarshal struct {
		Components []struct {
			ID int `json:"id"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &remarshal); err != nil {
		t.Fatalf("Unmarshal remarshal returned error: %v", err)
	}
	if remarshal.Components[0].ID != 2 {
		t.Fatalf("remarshaled text display id = %d, want 2", remarshal.Components[0].ID)
	}
}
