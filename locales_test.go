package discordgo

import (
	"runtime/debug"
	"testing"
)

func TestLocaleStringFallback(t *testing.T) {
	original := Locales
	t.Cleanup(func() { Locales = original })
	// Keep a recursive fallback regression from exhausting the process stack.
	stackLimit := debug.SetMaxStack(64 << 10)
	defer debug.SetMaxStack(stackLimit)
	tests := []struct {
		name    string
		locales map[Locale]string
		locale  Locale
		want    string
	}{
		{"known", original, EnglishUS, "English (United States)"},
		{"unknown", original, Locale("unrecognized"), "unknown"},
		{"custom unknown", map[Locale]string{Unknown: "not translated"}, Locale("unrecognized"), "not translated"},
		{"missing sentinel", map[Locale]string{EnglishUS: "English"}, Locale("unrecognized"), "unknown"},
		{"sentinel missing itself", map[Locale]string{}, Unknown, "unknown"},
		{"nil map", nil, EnglishUS, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Locales = tt.locales
			if got := tt.locale.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
