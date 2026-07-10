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
