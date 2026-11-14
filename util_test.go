package discordgo

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
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

// Readers without WriteTo are copied in 32 KiB chunks, so an unsized body
// buffer doubles repeatedly and keeps up to half of its capacity unused.
func BenchmarkMultipartBodyWithJSONFile(b *testing.B) {
	for _, size := range []int{1 << 10, 256 << 10, 8 << 20} {
		data := bytes.Repeat([]byte{'a'}, size)
		path := filepath.Join(b.TempDir(), "upload.png")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			b.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { file.Close() })

		for _, tc := range []struct {
			name   string
			reader func() io.Reader
		}{
			{"section", func() io.Reader { return io.NewSectionReader(bytes.NewReader(data), 0, int64(size)) }},
			{"os.File", func() io.Reader {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					b.Fatal(err)
				}
				return file
			}},
			{"bytes.Reader", func() io.Reader { return bytes.NewReader(data) }},
		} {
			b.Run(fmt.Sprintf("%s/%dKiB", tc.name, size>>10), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				var spare int
				for b.Loop() {
					_, body, err := MultipartBodyWithJSON(&MessageSend{Content: "upload"}, []*File{{Name: "upload.png", ContentType: "image/png", Reader: tc.reader()}})
					if err != nil {
						b.Fatal(err)
					}
					spare = cap(body) - len(body)
				}
				b.ReportMetric(float64(spare), "spare-B")
			})
		}
	}
}

func TestReaderSizeHintCountsFromCurrentOffset(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, 100)
	path := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	openAt := func(offset int64) io.Reader {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { file.Close() })
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		return file
	}
	readerAt := func(offset int64) io.Reader {
		r := bytes.NewReader(data)
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		return r
	}
	sectionAt := func(offset int64) io.Reader {
		r := io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data)))
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		return r
	}
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pipeReader.Close(); pipeWriter.Close() })

	tests := []struct {
		name   string
		reader io.Reader
		want   int64
	}{
		{"bytes.Reader", readerAt(10), 90},
		{"strings.Reader", strings.NewReader("hello"), 5},
		{"os.File", openAt(30), 70},
		{"os.File past end", openAt(150), -1},
		{"SectionReader", sectionAt(40), 60},
		{"LimitReader", io.LimitReader(bytes.NewReader(data), 50), -1},
		{"MultiReader", io.MultiReader(bytes.NewReader(data)), -1},
		{"pipe", pipeReader, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readerSizeHint(tt.reader); got != tt.want {
				t.Fatalf("readerSizeHint = %d, want %d", got, tt.want)
			}
		})
	}
}

// A presized body must hold the whole upload without growing again, and the
// file bytes must still start at the reader's current offset.
func TestMultipartBodyPresizesKnownFileSizes(t *testing.T) {
	const size = 256 << 10
	const offset = 1000
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	path := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })

	// Names that escaping lengthens and an invalid content type that gets
	// replaced must still fit in the estimate.
	longName := strings.Repeat("\r\n\"", 200)
	tests := []struct {
		name    string
		reader  func() io.Reader
		presize bool
	}{
		{"os.File", func() io.Reader {
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			return file
		}, true},
		{"SectionReader", func() io.Reader {
			r := io.NewSectionReader(bytes.NewReader(data), 0, size)
			if _, err := r.Seek(offset, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			return r
		}, true},
		{"unknown size", func() io.Reader {
			return io.MultiReader(bytes.NewReader(data[offset:]))
		}, false},
	}
	for _, tt := range tests {
		for _, includePayload := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/payload=%v", tt.name, includePayload), func(t *testing.T) {
				files := []*File{
					{Name: "small.txt", ContentType: "text/plain", Reader: strings.NewReader("small")},
					{Name: longName, FieldName: longName, ContentType: "bad\r\ntype", Reader: tt.reader()},
				}
				fields := map[string]string{longName: "value"}
				contentType, body, err := multipartBody(&MessageSend{Content: "upload"}, fields, files, includePayload)
				if err != nil {
					t.Fatal(err)
				}

				spare := cap(body) - len(body)
				if tt.presize && spare > 4<<10 {
					t.Fatalf("spare capacity = %d bytes for a %d byte body, want the estimate to fit without regrowing", spare, len(body))
				}

				_, params, err := mime.ParseMediaType(contentType)
				if err != nil {
					t.Fatal(err)
				}
				reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
				var got []byte
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if part.FileName() != "" && part.FormName() != "files[0]" {
						if got, err = io.ReadAll(part); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !bytes.Equal(got, data[offset:]) {
					t.Fatalf("file part has %d bytes, want the %d bytes after offset %d", len(got), size-offset, offset)
				}
			})
		}
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
