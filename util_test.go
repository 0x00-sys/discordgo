package discordgo

import (
	"strings"
	"testing"
	"time"
)

func TestSnowflakeTimestamp(t *testing.T) {
	// #discordgo channel ID :)
	id := "155361364909621248"
	parsedTimestamp, err := SnowflakeTimestamp(id)

	if err != nil {
		t.Errorf("returned error incorrect: got %v, want nil", err)
	}

	correctTimestamp := time.Date(2016, time.March, 4, 17, 10, 35, 869*1000000, time.UTC)
	if !parsedTimestamp.Equal(correctTimestamp) {
		t.Errorf("parsed time incorrect: got %v, want %v", parsedTimestamp, correctTimestamp)
	}
}

func TestMultipartBodyWithJSONEscapesFileNameCRLF(t *testing.T) {
	_, body, err := MultipartBodyWithJSON(
		map[string]string{"content": "hello"},
		[]*File{
			{
				Name:        "avatar\r\nX-Injected: yes\n.png",
				ContentType: "image/png",
				Reader:      strings.NewReader("file"),
			},
		},
	)
	if err != nil {
		t.Fatalf("MultipartBodyWithJSON returned error: %v", err)
	}

	bodyText := string(body)
	if strings.Contains(bodyText, "\r\nX-Injected: yes") {
		t.Fatalf("filename CRLF was written as a multipart header: %q", bodyText)
	}
	if !strings.Contains(bodyText, `filename="avatar%0D%0AX-Injected: yes%0A.png"`) {
		t.Fatalf("filename was not CRLF escaped: %q", bodyText)
	}
}

func TestMultipartBodyWithJSONDefaultsInvalidFileContentType(t *testing.T) {
	_, body, err := MultipartBodyWithJSON(
		map[string]string{"content": "hello"},
		[]*File{
			{
				Name:        "avatar.png",
				ContentType: "image/png\r\nX-Injected: yes",
				Reader:      strings.NewReader("file"),
			},
		},
	)
	if err != nil {
		t.Fatalf("MultipartBodyWithJSON returned error: %v", err)
	}

	bodyText := string(body)
	if strings.Contains(bodyText, "\r\nX-Injected: yes") {
		t.Fatalf("content type CRLF was written as a multipart header: %q", bodyText)
	}
	if !strings.Contains(bodyText, "Content-Type: application/octet-stream\r\n") {
		t.Fatalf("invalid content type did not use safe default: %q", bodyText)
	}
}

func TestMultipartBodyWithJSONUsesCustomFileFieldName(t *testing.T) {
	_, body, err := MultipartBodyWithJSON(
		map[string]string{"content": "hello"},
		[]*File{
			{
				Name:        "users.csv",
				ContentType: "text/csv",
				Reader:      strings.NewReader("user_id\n1\n"),
				FieldName:   "target_users_file",
			},
		},
	)
	if err != nil {
		t.Fatalf("MultipartBodyWithJSON returned error: %v", err)
	}

	bodyText := string(body)
	if !strings.Contains(bodyText, `name="target_users_file"; filename="users.csv"`) {
		t.Fatalf("custom field name was not used: %q", bodyText)
	}
	if strings.Contains(bodyText, `name="files[0]"`) {
		t.Fatalf("default field name was used despite custom field name: %q", bodyText)
	}
}

func TestMultipartBodyWithoutJSONOmitsPayload(t *testing.T) {
	_, body, err := multipartBody(
		nil,
		[]*File{
			{
				Name:        "users.csv",
				ContentType: "text/csv",
				Reader:      strings.NewReader("user_id\n1\n"),
				FieldName:   "target_users_file",
			},
		},
		false,
	)
	if err != nil {
		t.Fatalf("multipartBody returned error: %v", err)
	}

	bodyText := string(body)
	if strings.Contains(bodyText, "payload_json") {
		t.Fatalf("multipart body included payload_json: %q", bodyText)
	}
	if !strings.Contains(bodyText, `name="target_users_file"; filename="users.csv"`) {
		t.Fatalf("target user file field was not present: %q", bodyText)
	}
}

func TestMultipartBodyWithJSONRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name  string
		files []*File
		want  string
	}{
		{
			name:  "nil file",
			files: []*File{nil},
			want:  "file at index 0 is nil",
		},
		{
			name: "nil file reader",
			files: []*File{
				{
					Name: "transcript.html",
				},
			},
			want: "file reader at index 0 is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := MultipartBodyWithJSON(map[string]string{"content": "hello"}, tt.files)
			if err == nil {
				t.Fatal("MultipartBodyWithJSON returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("MultipartBodyWithJSON error = %q, want %q", err, tt.want)
			}
		})
	}
}
