package discordgo

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// SnowflakeTimestamp returns the creation time of a Snowflake ID relative to the creation of Discord.
func SnowflakeTimestamp(ID string) (t time.Time, err error) {
	i, err := strconv.ParseInt(ID, 10, 64)
	if err != nil {
		return
	}
	timestamp := (i >> 22) + 1420070400000
	t = time.Unix(0, timestamp*1000000)
	return
}

// MultipartBodyWithJSON returns the contentType and body for a discord request
// data  : The object to encode for payload_json in the multipart request
// files : Files to include in the request
func MultipartBodyWithJSON(data interface{}, files []*File) (requestContentType string, requestBody []byte, err error) {
	return multipartBody(data, nil, files, true)
}

func multipartBody(data interface{}, fields map[string]string, files []*File, includePayload bool) (requestContentType string, requestBody []byte, err error) {
	body := &bytes.Buffer{}
	bodywriter := multipart.NewWriter(body)
	var p io.Writer

	var payload []byte
	if includePayload {
		payload, err = Marshal(data)
		if err != nil {
			return
		}
	}
	if hint := multipartBodySizeHint(payload, fields, files); hint > 0 {
		body.Grow(hint)
	}

	if includePayload {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="payload_json"`)
		h.Set("Content-Type", "application/json")

		p, err = bodywriter.CreatePart(h)
		if err != nil {
			return
		}

		if _, err = p.Write(payload); err != nil {
			return
		}
	}

	for name, value := range fields {
		if err = bodywriter.WriteField(name, value); err != nil {
			return
		}
	}

	for i, file := range files {
		if file == nil {
			err = fmt.Errorf("file at index %d is nil", i)
			return
		}
		if file.Reader == nil {
			err = fmt.Errorf("file reader at index %d is nil", i)
			return
		}

		fieldName := file.FieldName
		if fieldName == "" {
			fieldName = fmt.Sprintf("files[%d]", i)
		}

		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quoteEscaper.Replace(fieldName), quoteEscaper.Replace(file.Name)))
		h.Set("Content-Type", safeFileContentType(file.ContentType))

		p, err = bodywriter.CreatePart(h)
		if err != nil {
			return
		}

		if _, err = io.Copy(p, file.Reader); err != nil {
			return
		}
	}

	err = bodywriter.Close()
	if err != nil {
		return
	}

	return bodywriter.FormDataContentType(), body.Bytes(), nil
}

func safeFileContentType(contentType string) string {
	if contentType == "" || strings.ContainsAny(contentType, "\r\n") {
		return "application/octet-stream"
	}

	return contentType
}

func avatarURL(avatarHash, defaultAvatarURL, staticAvatarURL, animatedAvatarURL, size string) string {
	var URL string
	if avatarHash == "" {
		URL = defaultAvatarURL
	} else if strings.HasPrefix(avatarHash, "a_") {
		URL = animatedAvatarURL
	} else {
		URL = staticAvatarURL
	}

	if size != "" {
		return URL + "?size=" + size
	}
	return URL
}

func bannerURL(bannerHash, staticBannerURL, animatedBannerURL, size string) string {
	var URL string
	if bannerHash == "" {
		return ""
	} else if strings.HasPrefix(bannerHash, "a_") {
		URL = animatedBannerURL
	} else {
		URL = staticBannerURL
	}

	if size != "" {
		return URL + "?size=" + size
	}
	return URL
}

func iconURL(iconHash, staticIconURL, animatedIconURL, size string) string {
	var URL string
	if iconHash == "" {
		return ""
	} else if strings.HasPrefix(iconHash, "a_") {
		URL = animatedIconURL
	} else {
		URL = staticIconURL
	}

	if size != "" {
		return URL + "?size=" + size
	}
	return URL
}

// Bounds for presizing a multipart body. Below the minimum the buffer only
// grows a few cheap times. The maximum keeps a wrong size from reserving more
// memory than the largest Discord upload needs.
const (
	minMultipartSizeHint = 64 << 10
	maxMultipartSizeHint = 512 << 20
)

// multipartPartOverhead covers one part's boundary line, header names and
// CRLFs. Variable header values are added separately.
const multipartPartOverhead = 256

// multipartBodySizeHint returns a capacity that holds the whole multipart
// body, or 0 when any file size is unknown or the body is small. It errs on
// the high side: a short estimate would make the buffer double at the end.
func multipartBodySizeHint(payload []byte, fields map[string]string, files []*File) int {
	total := int64(multipartPartOverhead) // closing boundary
	var fileBytes int64
	for _, file := range files {
		if file == nil || file.Reader == nil {
			return 0
		}
		n := readerSizeHint(file.Reader)
		if n < 0 {
			return 0
		}
		fileBytes += n
		// Escaping can triple the length of the field and file names.
		total += n + multipartPartOverhead + 3*int64(len(file.Name)+len(file.FieldName)) + int64(max(len(file.ContentType), len("application/octet-stream")))
		if total > maxMultipartSizeHint {
			return 0
		}
	}
	// Small uploads grow in a few cheap steps, and a Stat is all they pay.
	if fileBytes < minMultipartSizeHint {
		return 0
	}
	if payload != nil {
		total += int64(len(payload)) + multipartPartOverhead
	}
	for name, value := range fields {
		total += int64(3*len(name)+len(value)) + multipartPartOverhead
	}
	if total > maxMultipartSizeHint {
		return 0
	}
	return int(total)
}

// readerSizeHint returns the bytes left to read from r, or -1 if unknown.
func readerSizeHint(r io.Reader) int64 {
	switch v := r.(type) {
	case interface{ Len() int }:
		// bytes.Reader, bytes.Buffer, strings.Reader: unread bytes.
		return int64(v.Len())
	case interface {
		Stat() (fs.FileInfo, error)
		io.Seeker
	}:
		// *os.File. Pipes and other special files have no usable size.
		info, err := v.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return -1
		}
		return remainingFromOffset(v, info.Size())
	case interface {
		Size() int64
		io.Seeker
	}:
		// *io.SectionReader.
		return remainingFromOffset(v, v.Size())
	}
	return -1
}

func remainingFromOffset(s io.Seeker, size int64) int64 {
	offset, err := s.Seek(0, io.SeekCurrent)
	if err != nil || offset < 0 || offset > size {
		return -1
	}
	return size - offset
}
