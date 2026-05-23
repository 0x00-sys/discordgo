package discordgo

import (
	"strings"
	"testing"
)

func TestRedactedVoiceData(t *testing.T) {
	data := []byte(`{"op":4,"d":{"token":"voice-token","secret_key":[1,2,3],"nested":{"access_token":"access-token","refresh_token":"refresh-token","safe":"ok"}}}`)

	got := redactedVoiceData(data)
	for _, secret := range []string{"voice-token", "access-token", "refresh-token", "1,2,3"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedVoiceData() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedVoiceData() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedVoiceData() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedVoiceDataInvalidJSON(t *testing.T) {
	data := []byte("not-json")

	if got := redactedVoiceData(data); got != string(data) {
		t.Fatalf("redactedVoiceData() = %q, want %q", got, string(data))
	}
}
