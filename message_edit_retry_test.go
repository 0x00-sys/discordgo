package discordgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMessageEditUploadRetrySafety(t *testing.T) {
	for _, api := range []struct {
		name string
		path string
		edit func(*Session, []*File) (*Message, error)
	}{
		{
			name: "channel", path: "/channels/channel/messages/message",
			edit: func(s *Session, files []*File) (*Message, error) {
				return s.ChannelMessageEditComplex(&MessageEdit{Channel: "channel", ID: "message", Files: files})
			},
		},
		{
			name: "webhook", path: "/webhooks/webhook/test-token/messages/message",
			edit: func(s *Session, files []*File) (*Message, error) {
				return s.WebhookMessageEdit("webhook", "test-token", "message", &WebhookEdit{Files: files})
			},
		},
		{
			name: "interaction", path: "/webhooks/application/test-token/messages/@original",
			edit: func(s *Session, files []*File) (*Message, error) {
				return s.InteractionResponseEdit(&Interaction{AppID: "application", Token: "test-token"}, &WebhookEdit{Files: files})
			},
		},
	} {
		for _, firstStatus := range []int{http.StatusBadGateway, http.StatusTooManyRequests} {
			t.Run(fmt.Sprintf("%s/%d", api.name, firstStatus), func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				attempts := 0
				var attachments []*MessageAttachment
				var responseClosures []chan struct{}
				session.Client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					defer func() { _ = r.Body.Close() }()
					attempts++
					if r.Method != http.MethodPatch || r.URL.Path != "/api/v"+APIVersion+api.path {
						t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					reader, err := r.MultipartReader()
					if err != nil {
						t.Fatal(err)
					}
					files, payloads := 0, 0
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(part)
						if err != nil {
							t.Fatal(err)
						}
						switch part.FormName() {
						case "payload_json":
							payloads++
							var payload map[string]json.RawMessage
							if err := json.Unmarshal(body, &payload); err != nil {
								t.Fatal(err)
							}
							if _, replacing := payload["attachments"]; replacing {
								t.Fatal("fixture must append uploads without replacing existing attachments")
							}
						case "files[0]":
							files++
							if part.FileName() != "upload.txt" || string(body) != "file contents" {
								t.Fatal("upload filename or contents changed")
							}
						default:
							t.Fatalf("unexpected multipart field %q", part.FormName())
						}
						if err := part.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if files != 1 || payloads != 1 {
						t.Fatalf("multipart files=%d payloads=%d, want one of each", files, payloads)
					}
					// Model the documented append semantics. A gateway error can
					// follow a committed upload; a rate limit rejects it outright.
					if attempts != 1 || firstStatus != http.StatusTooManyRequests {
						attachments = append(attachments, &MessageAttachment{ID: fmt.Sprint(len(attachments) + 1), Filename: "upload.txt"})
					}
					status := http.StatusOK
					body, err := json.Marshal(&Message{ID: "message", ChannelID: "channel", Attachments: attachments})
					if err != nil {
						t.Fatal(err)
					}
					if attempts == 1 {
						status = firstStatus
						body = []byte(`{"message":"upstream response unavailable"}`)
						if status == http.StatusTooManyRequests {
							body = []byte(`{"message":"rate limited","retry_after":0.001,"global":false}`)
						}
					}
					closed := make(chan struct{})
					responseClosures = append(responseClosures, closed)
					return &http.Response{
						StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
						Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r,
						Body: &closeNotifyReadCloser{reader: strings.NewReader(string(body)), closed: closed},
					}, nil
				})
				message, err := api.edit(session, []*File{{Name: "upload.txt", Reader: strings.NewReader("file contents")}})
				if firstStatus == http.StatusBadGateway {
					var restErr *RESTError
					if !errors.As(err, &restErr) || restErr.Response.StatusCode != firstStatus || message != nil || attempts != 1 {
						t.Errorf("ambiguous upload result: attempts=%d message=%v error=%v; want one attempt and original RESTError", attempts, message, err)
					}
				} else if err != nil || attempts != 2 || message == nil || len(message.Attachments) != 1 {
					t.Errorf("rate-limited upload result: attempts=%d message=%v error=%v; want one successful retry", attempts, message, err)
				}
				if len(attachments) != 1 {
					t.Errorf("uploaded attachments = %d, want 1; an ambiguous failure replayed the append", len(attachments))
				}
				for _, closed := range responseClosures {
					select {
					case <-closed:
					default:
						t.Error("response body was not closed")
					}
				}
			})
		}
	}
}
