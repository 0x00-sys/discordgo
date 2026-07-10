package discordgo

import (
	"encoding/json"
	"testing"
)

func TestUser_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		u    *User
		want string
	}{
		{
			name: "User with a discriminator",
			u: &User{
				Username:      "bob",
				Discriminator: "8192",
			},
			want: "bob#8192",
		},
		{
			name: "User with discriminator set to 0",
			u: &User{
				Username:      "aldiwildan",
				Discriminator: "0",
			},
			want: "aldiwildan",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.String(); got != tc.want {
				t.Errorf("User.String() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUser_DisplayName(t *testing.T) {
	t.Run("no global name set", func(t *testing.T) {
		u := &User{
			GlobalName: "",
			Username:   "username",
		}
		if dn := u.DisplayName(); dn != u.Username {
			t.Errorf("User.DisplayName() = %v, want %v", dn, u.Username)
		}
	})
	t.Run("global name set", func(t *testing.T) {
		u := &User{
			GlobalName: "global",
			Username:   "username",
		}
		if dn := u.DisplayName(); dn != u.GlobalName {
			t.Errorf("User.DisplayName() = %v, want %v", dn, u.GlobalName)
		}
	})
}

func TestAvatarDecorationDataUnmarshal(t *testing.T) {
	payload := []byte(`{"avatar_decoration_data":{"sku_id":"1144058844004233369","asset":"a_fed43ab12698df65902ba06727e20c0e"}}`)

	var user User
	if err := json.Unmarshal(payload, &user); err != nil {
		t.Fatalf("json.Unmarshal user returned error: %v", err)
	}
	if user.AvatarDecorationData == nil {
		t.Fatal("user avatar decoration data = nil, want non-nil")
	}
	want := AvatarDecorationData{
		Asset: "a_fed43ab12698df65902ba06727e20c0e",
		SKUID: "1144058844004233369",
	}
	if *user.AvatarDecorationData != want {
		t.Fatalf("user avatar decoration data = %#v, want %#v", user.AvatarDecorationData, want)
	}
	wantURL := "https://cdn.discordapp.com/avatar-decoration-presets/a_fed43ab12698df65902ba06727e20c0e.png"
	if got := user.AvatarDecorationURL(); got != wantURL {
		t.Fatalf("user avatar decoration URL = %q, want %q", got, wantURL)
	}
	if got := EndpointAvatarDecoration(want.Asset); got != wantURL {
		t.Fatalf("avatar decoration endpoint = %q, want %q", got, wantURL)
	}

	var member Member
	if err := json.Unmarshal(payload, &member); err != nil {
		t.Fatalf("json.Unmarshal member returned error: %v", err)
	}
	if member.AvatarDecorationData == nil || *member.AvatarDecorationData != want {
		t.Fatalf("member avatar decoration data = %#v, want %#v", member.AvatarDecorationData, want)
	}
	if got := member.AvatarDecorationURL(); got != wantURL {
		t.Fatalf("member avatar decoration URL = %q, want %q", got, wantURL)
	}

	var update GuildMemberUpdate
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatalf("json.Unmarshal guild member update returned error: %v", err)
	}
	if update.Member == nil || update.AvatarDecorationData == nil || *update.AvatarDecorationData != want {
		t.Fatalf("guild member update avatar decoration data = %#v, want %#v", update.Member, want)
	}
}

func TestAvatarDecorationURLFallbackAndNilSafety(t *testing.T) {
	userDecoration := &AvatarDecorationData{Asset: "user-decoration", SKUID: "user-sku"}
	member := &Member{User: &User{AvatarDecorationData: userDecoration}}
	if got, want := member.AvatarDecorationURL(), EndpointAvatarDecoration(userDecoration.Asset); got != want {
		t.Fatalf("member avatar decoration fallback URL = %q, want %q", got, want)
	}

	memberDecoration := &AvatarDecorationData{Asset: "member-decoration", SKUID: "member-sku"}
	member.AvatarDecorationData = memberDecoration
	if got, want := member.AvatarDecorationURL(), EndpointAvatarDecoration(memberDecoration.Asset); got != want {
		t.Fatalf("member avatar decoration URL = %q, want %q", got, want)
	}

	var user User
	if err := json.Unmarshal([]byte(`{"avatar_decoration_data":null}`), &user); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if user.AvatarDecorationData != nil {
		t.Fatalf("avatar decoration data = %#v, want nil", user.AvatarDecorationData)
	}
	if got := user.AvatarDecorationURL(); got != "" {
		t.Fatalf("avatar decoration URL = %q, want empty", got)
	}
	if got := (*User)(nil).AvatarDecorationURL(); got != "" {
		t.Fatalf("nil user avatar decoration URL = %q, want empty", got)
	}
	if got := (&Member{}).AvatarDecorationURL(); got != "" {
		t.Fatalf("empty member avatar decoration URL = %q, want empty", got)
	}
	if got := (*Member)(nil).AvatarDecorationURL(); got != "" {
		t.Fatalf("nil member avatar decoration URL = %q, want empty", got)
	}
}

func TestCollectiblesUnmarshal(t *testing.T) {
	payload := []byte(`{"collectibles":{"nameplate":{"sku_id":"2247558840304243311","asset":"nameplates/nameplates/twilight/","label":"Twilight","palette":"cobalt"}}}`)

	var user User
	if err := json.Unmarshal(payload, &user); err != nil {
		t.Fatalf("json.Unmarshal user returned error: %v", err)
	}
	if user.Collectibles == nil || user.Collectibles.Nameplate == nil {
		t.Fatalf("user collectibles = %#v", user.Collectibles)
	}
	nameplate := user.Collectibles.Nameplate
	if nameplate.SKUID != "2247558840304243311" || nameplate.Asset != "nameplates/nameplates/twilight/" || nameplate.Label != "Twilight" || nameplate.Palette != NameplatePaletteCobalt {
		t.Fatalf("user nameplate = %#v", nameplate)
	}

	var member Member
	if err := json.Unmarshal(payload, &member); err != nil {
		t.Fatalf("json.Unmarshal member returned error: %v", err)
	}
	if member.Collectibles == nil || member.Collectibles.Nameplate == nil {
		t.Fatalf("member collectibles = %#v", member.Collectibles)
	}
	if *member.Collectibles.Nameplate != *nameplate {
		t.Fatalf("member nameplate = %#v, want %#v", member.Collectibles.Nameplate, nameplate)
	}

	var update GuildMemberUpdate
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatalf("json.Unmarshal guild member update returned error: %v", err)
	}
	if update.Member == nil || update.Collectibles == nil || update.Collectibles.Nameplate == nil {
		t.Fatalf("guild member update collectibles = %#v", update.Member)
	}
	if *update.Collectibles.Nameplate != *nameplate {
		t.Fatalf("guild member update nameplate = %#v, want %#v", update.Collectibles.Nameplate, nameplate)
	}
}

func TestCollectiblesNullableFields(t *testing.T) {
	var user User
	if err := json.Unmarshal([]byte(`{"collectibles":null}`), &user); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if user.Collectibles != nil {
		t.Fatalf("collectibles = %#v, want nil", user.Collectibles)
	}

	if err := json.Unmarshal([]byte(`{"collectibles":{}}`), &user); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if user.Collectibles == nil {
		t.Fatal("collectibles = nil, want non-nil")
	}
	if user.Collectibles.Nameplate != nil {
		t.Fatalf("nameplate = %#v, want nil", user.Collectibles.Nameplate)
	}
}

func TestNameplatePaletteValues(t *testing.T) {
	tests := []struct {
		palette NameplatePalette
		want    string
	}{
		{palette: NameplatePaletteCrimson, want: "crimson"},
		{palette: NameplatePaletteBerry, want: "berry"},
		{palette: NameplatePaletteSky, want: "sky"},
		{palette: NameplatePaletteTeal, want: "teal"},
		{palette: NameplatePaletteForest, want: "forest"},
		{palette: NameplatePaletteBubbleGum, want: "bubble_gum"},
		{palette: NameplatePaletteViolet, want: "violet"},
		{palette: NameplatePaletteCobalt, want: "cobalt"},
		{palette: NameplatePaletteClover, want: "clover"},
		{palette: NameplatePaletteLemon, want: "lemon"},
		{palette: NameplatePaletteWhite, want: "white"},
	}

	for _, tt := range tests {
		if got := string(tt.palette); got != tt.want {
			t.Errorf("palette = %q, want %q", got, tt.want)
		}
	}
}
