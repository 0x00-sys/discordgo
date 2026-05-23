package discordgo

import (
	"strings"
	"testing"
)

func TestRedactedGatewayData(t *testing.T) {
	data := []byte(`{"id":"interaction","token":"interaction-token","nested":{"access_token":"access-token","refresh_token":"refresh-token","safe":"ok"},"items":[{"token":"voice-token"}]}`)

	got := redactedGatewayData(data)
	for _, secret := range []string{"interaction-token", "access-token", "refresh-token", "voice-token"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedGatewayData() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedGatewayData() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedGatewayData() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedGatewayDataInvalidJSON(t *testing.T) {
	data := []byte("not-json")

	if got := redactedGatewayData(data); got != string(data) {
		t.Fatalf("redactedGatewayData() = %q, want %q", got, string(data))
	}
}
