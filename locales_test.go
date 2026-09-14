package discordgo

import (
	"encoding/json"
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

func TestIndonesianLocale(t *testing.T) {
	locale := Indonesian
	if string(locale) != "id" {
		t.Fatalf("locale code = %q", string(locale))
	}
	if got := locale.String(); got != "Indonesian" {
		t.Fatalf("String() = %q, want Indonesian", got)
	}
	data, err := json.Marshal(map[Locale]string{locale: "halo"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"id":"halo"}` {
		t.Fatalf("localization JSON = %s", data)
	}
	var decoded struct {
		Locale Locale `json:"locale"`
	}
	if err := json.Unmarshal([]byte(`{"locale":"id"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Locale != locale {
		t.Fatalf("decoded locale = %q", string(decoded.Locale))
	}
}
