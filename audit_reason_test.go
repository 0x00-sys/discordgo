package discordgo

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestWithAuditLogReasonRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, reason, header string }{
		{"plain", "moderation", "moderation"},
		{"multiline", "first line\nsecond line\r\n", "first%20line%0Asecond%20line%0D%0A"},
		{"unicode", "café 🚫", "caf%C3%A9%20%F0%9F%9A%AB"},
		{"percent and plus", "100% + %20", "100%25%20%2B%20%2520"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("X-Audit-Log-Reason")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			s, err := New("Bot dummy-token")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Request(http.MethodDelete, server.URL, nil, WithAuditLogReason(tc.reason)); err != nil {
				t.Fatalf("request failed before reason could be received: %v", err)
			}
			got := <-received
			if got != tc.header {
				t.Errorf("wire reason = %q, want %q", got, tc.header)
			}
			decoded, err := url.PathUnescape(got)
			if err != nil || decoded != tc.reason {
				t.Errorf("decoded reason = %q, error %v; want %q", decoded, err, tc.reason)
			}
		})
	}
}
