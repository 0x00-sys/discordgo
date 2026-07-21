package discordgo

import (
	"fmt"
	"strings"
	"testing"
)

func TestVoiceOnEventSkipsRedactionWhenDebugDisabled(t *testing.T) {
	payload := strings.Builder{}
	payload.WriteString(`{"op":99,"d":{`)
	for i := 0; i < 1000; i++ {
		if i > 0 {
			payload.WriteString(",")
		}
		fmt.Fprintf(&payload, `"field%d":"value%d"`, i, i)
	}
	payload.WriteString(`}}`)
	message := []byte(payload.String())

	voice := &VoiceConnection{} // default LogLevel is LogError
	allocs := testing.AllocsPerRun(20, func() {
		voice.onEvent(message)
	})

	// Redacting the payload unmarshals and re-marshals every field.
	// The plain envelope decode stays far below that.
	if allocs > 500 {
		t.Fatalf("onEvent() allocations = %v with debug logging disabled, want <= 500", allocs)
	}
}

func TestVoiceOnEventLogsRedactedDataWhenDebugEnabled(t *testing.T) {
	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	var logged []string
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, a...))
	}

	voice := &VoiceConnection{LogLevel: LogDebug}
	voice.onEvent([]byte(`{"op":99,"d":{"token":"secret-token","safe":"ok"}}`))

	found := false
	for _, line := range logged {
		if strings.Contains(line, "REDACTED") {
			found = true
		}
		if strings.Contains(line, "secret-token") {
			t.Fatalf("debug log leaked token: %q", line)
		}
	}
	if !found {
		t.Fatalf("debug log did not include redacted voice data: %q", logged)
	}
}

func BenchmarkVoiceEventRedaction(b *testing.B) {
	oldLogger := Logger
	Logger = func(int, int, string, ...interface{}) {}
	b.Cleanup(func() { Logger = oldLogger })

	message := []byte(`{"op":5,"d":{"user_id":"123456789012345678","ssrc":42,"speaking":1}}`)
	for _, level := range []struct {
		name string
		log  int
	}{
		{name: "debug-disabled", log: LogError},
		{name: "debug-enabled", log: LogDebug},
	} {
		b.Run(level.name, func(b *testing.B) {
			voice := &VoiceConnection{LogLevel: level.log}
			b.ReportAllocs()
			for b.Loop() {
				voice.onEvent(message)
			}
		})
	}
}
