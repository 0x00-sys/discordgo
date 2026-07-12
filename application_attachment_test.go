package discordgo

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApplicationAttachmentUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/applications/app/attachment" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/applications/app/attachment")
		}
		if got := r.Header.Get("X-Test-Request-Option"); got != "present" {
			t.Fatalf("X-Test-Request-Option = %q, want present", got)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}
		if _, ok := r.MultipartForm.Value["payload_json"]; ok {
			t.Fatal("payload_json was included in application attachment upload")
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile returned error: %v", err)
		}
		defer file.Close()
		contents, err := ioutil.ReadAll(file)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if header.Filename != "activity.png" {
			t.Fatalf("filename = %q, want activity.png", header.Filename)
		}
		if got := header.Header.Get("Content-Type"); got != "image/png" {
			t.Fatalf("file Content-Type = %q, want image/png", got)
		}
		if string(contents) != "png" {
			t.Fatalf("file contents = %q, want png", contents)
		}

		_, _ = w.Write([]byte(`{
			"attachment": {
				"id": "attachment",
				"filename": "activity.png",
				"title": null,
				"description": "Activity image",
				"content_type": "image/png",
				"size": 3,
				"url": "https://cdn.discordapp.com/attachment",
				"proxy_url": "https://media.discordapp.net/attachment",
				"width": 640,
				"height": 480,
				"placeholder": "hash",
				"placeholder_version": 1,
				"ephemeral": true,
				"duration_secs": 1.5,
				"waveform": "wave",
				"flags": 1,
				"clip_participants": [{"id":"user","username":"tester"}],
				"clip_created_at": "2026-01-02T03:04:05Z",
				"application": {"id":"app","name":"Activity"}
			}
		}`))
	}))
	t.Cleanup(server.Close)

	oldEndpointApplications := EndpointApplications
	EndpointApplications = server.URL + "/applications"
	t.Cleanup(func() {
		EndpointApplications = oldEndpointApplications
	})

	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client = server.Client()

	response, err := session.ApplicationAttachmentUpload("app", &File{
		Name:        "activity.png",
		ContentType: "image/png",
		Reader:      strings.NewReader("png"),
		FieldName:   "ignored",
	}, WithHeader("X-Test-Request-Option", "present"))
	if err != nil {
		t.Fatalf("ApplicationAttachmentUpload returned error: %v", err)
	}

	attachment := response.Attachment
	if attachment.ID != "attachment" || attachment.Filename != "activity.png" || attachment.Title != "" {
		t.Fatalf("attachment identity = %#v", attachment)
	}
	if attachment.Description != "Activity image" || attachment.ContentType != "image/png" || attachment.Size != 3 {
		t.Fatalf("attachment metadata = %#v", attachment)
	}
	if attachment.URL != "https://cdn.discordapp.com/attachment" || attachment.ProxyURL != "https://media.discordapp.net/attachment" {
		t.Fatalf("attachment URLs = %#v", attachment)
	}
	if attachment.Width != 640 || attachment.Height != 480 || attachment.Placeholder != "hash" || attachment.PlaceholderVersion != 1 {
		t.Fatalf("attachment dimensions = %#v", attachment)
	}
	if !attachment.Ephemeral || attachment.DurationSecs != 1.5 || attachment.Waveform != "wave" || attachment.Flags != MessageAttachmentFlagsIsClip {
		t.Fatalf("attachment media data = %#v", attachment)
	}
	if len(attachment.ClipParticipants) != 1 || attachment.ClipParticipants[0].ID != "user" {
		t.Fatalf("clip participants = %#v", attachment.ClipParticipants)
	}
	if attachment.ClipCreatedAt == nil || !attachment.ClipCreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("clip created at = %#v", attachment.ClipCreatedAt)
	}
	if attachment.Application == nil || attachment.Application.ID != "app" || attachment.Application.Name != "Activity" {
		t.Fatalf("application = %#v", attachment.Application)
	}
}

func TestApplicationAttachmentUploadInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{`},
		{name: "null response", body: `null`},
		{name: "missing attachment", body: `{}`},
		{name: "null attachment", body: `{"attachment":null}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := New("Bot test")
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       ioutil.NopCloser(strings.NewReader(tt.body)),
					Request:    r,
				}, nil
			})

			response, err := session.ApplicationAttachmentUpload("app", &File{
				Name:   "activity.png",
				Reader: strings.NewReader("png"),
			})
			if response != nil {
				t.Fatalf("response = %#v, want nil", response)
			}
			if !errors.Is(err, ErrJSONUnmarshal) {
				t.Fatalf("error = %v, want ErrJSONUnmarshal", err)
			}
		})
	}
}

func TestApplicationAttachmentUploadRESTError(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     make(http.Header),
			Body:       ioutil.NopCloser(strings.NewReader(`{"code":50046,"message":"Invalid file uploaded"}`)),
			Request:    r,
		}, nil
	})

	response, err := session.ApplicationAttachmentUpload("app", &File{
		Name:   "activity.png",
		Reader: strings.NewReader("png"),
	})
	if response != nil {
		t.Fatalf("response = %#v, want nil", response)
	}
	var restErr *RESTError
	if !errors.As(err, &restErr) {
		t.Fatalf("error = %T %v, want *RESTError", err, err)
	}
	if restErr.Response.StatusCode != http.StatusBadRequest || restErr.Message == nil || restErr.Message.Code != ErrCodeInvalidFileUploaded {
		t.Fatalf("RESTError = %#v", restErr)
	}
}

func TestApplicationAttachmentUploadInvalidFile(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, err := session.ApplicationAttachmentUpload("app", nil); err == nil {
		t.Fatal("ApplicationAttachmentUpload returned nil error for nil file")
	}
	if _, err := session.ApplicationAttachmentUpload("app", &File{Name: "activity.png"}); err == nil {
		t.Fatal("ApplicationAttachmentUpload returned nil error for nil file reader")
	}
}
