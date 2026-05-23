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
