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

func TestPreviewModalComponents(t *testing.T) {
	t.Run("radio group", func(t *testing.T) {
		raw := []byte(`{
			"type": 18,
			"id": 1,
			"label": "Priority",
			"component": {
				"type": 21,
				"id": 2,
				"custom_id": "priority",
				"required": false,
				"options": [
					{"label": "High", "value": "high", "description": "urgent", "default": true},
					{"label": "Low", "value": "low", "description": "later", "default": false}
				],
				"value": "high"
			}
		}`)

		label := componentLabelFromJSON(t, raw)
		group, ok := label.Component.(*RadioGroup)
		if !ok {
			t.Fatalf("label.Component = %T, want *RadioGroup", label.Component)
		}
		if group.ID != 2 || group.CustomID != "priority" || group.Value == nil || *group.Value != "high" {
			t.Fatalf("radio group = %#v, want id, custom id, and response value", group)
		}
		if group.Required == nil || *group.Required {
			t.Fatalf("radio group Required = %v, want pointer to false", group.Required)
		}
		if len(group.Options) != 2 || group.Options[0].Value != "high" || !group.Options[0].Default {
			t.Fatalf("radio group options = %#v, want parsed options", group.Options)
		}

		assertComponentMarshalType(t, group, RadioGroupComponent)
	})

	t.Run("checkbox group", func(t *testing.T) {
		raw := []byte(`{
			"type": 18,
			"id": 1,
			"label": "Tags",
			"component": {
				"type": 22,
				"id": 2,
				"custom_id": "tags",
				"min_values": 0,
				"max_values": 2,
				"required": true,
				"options": [
					{"label": "Bug", "value": "bug", "description": "broken", "default": true},
					{"label": "Docs", "value": "docs", "description": "writing", "default": false}
				],
				"values": ["bug"]
			}
		}`)

		label := componentLabelFromJSON(t, raw)
		group, ok := label.Component.(*CheckboxGroup)
		if !ok {
			t.Fatalf("label.Component = %T, want *CheckboxGroup", label.Component)
		}
		if group.ID != 2 || group.CustomID != "tags" || group.MaxValues != 2 {
			t.Fatalf("checkbox group = %#v, want id, custom id, and max values", group)
		}
		if group.MinValues == nil || *group.MinValues != 0 {
			t.Fatalf("checkbox group MinValues = %v, want pointer to 0", group.MinValues)
		}
		if group.Required == nil || !*group.Required {
			t.Fatalf("checkbox group Required = %v, want pointer to true", group.Required)
		}
		if len(group.Values) != 1 || group.Values[0] != "bug" {
			t.Fatalf("checkbox group Values = %#v, want [bug]", group.Values)
		}
		if len(group.Options) != 2 || group.Options[0].Value != "bug" || !group.Options[0].Default {
			t.Fatalf("checkbox group options = %#v, want parsed options", group.Options)
		}

		assertComponentMarshalType(t, group, CheckboxGroupComponent)
	})

	t.Run("checkbox", func(t *testing.T) {
		raw := []byte(`{
			"type": 18,
			"id": 1,
			"label": "Confirm",
			"component": {
				"type": 23,
				"id": 2,
				"custom_id": "confirm",
				"default": true,
				"value": false
			}
		}`)

		label := componentLabelFromJSON(t, raw)
		checkbox, ok := label.Component.(*Checkbox)
		if !ok {
			t.Fatalf("label.Component = %T, want *Checkbox", label.Component)
		}
		if checkbox.ID != 2 || checkbox.CustomID != "confirm" || !checkbox.Default {
			t.Fatalf("checkbox = %#v, want id, custom id, and default true", checkbox)
		}
		if checkbox.Value == nil || *checkbox.Value {
			t.Fatalf("checkbox Value = %v, want pointer to false", checkbox.Value)
		}

		assertComponentMarshalType(t, checkbox, CheckboxComponent)
	})
}

func componentLabelFromJSON(t *testing.T, raw []byte) *Label {
	t.Helper()

	component, err := MessageComponentFromJSON(raw)
	if err != nil {
		t.Fatalf("MessageComponentFromJSON returned error: %v", err)
	}
	label, ok := component.(*Label)
	if !ok {
		t.Fatalf("component = %T, want *Label", component)
	}
	return label
}

func assertComponentMarshalType(t *testing.T, component MessageComponent, want ComponentType) {
	t.Helper()

	data, err := json.Marshal(component)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var got struct {
		Type ComponentType `json:"type"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal marshaled component returned error: %v", err)
	}
	if got.Type != want {
		t.Fatalf("marshaled type = %d, want %d", got.Type, want)
	}
}
