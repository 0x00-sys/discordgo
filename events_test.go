package discordgo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

func TestVoiceChannelEffectSendSoundIDUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "number",
			data: `{"channel_id":"channel","guild_id":"guild","user_id":"user","sound_id":123,"sound_volume":0.75}`,
			want: "123",
		},
		{
			name: "string",
			data: `{"channel_id":"channel","guild_id":"guild","user_id":"user","sound_id":"456","sound_volume":0.5}`,
			want: "456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event VoiceChannelEffectSend
			if err := json.Unmarshal([]byte(tt.data), &event); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if event.SoundID != tt.want {
				t.Fatalf("SoundID = %q, want %q", event.SoundID, tt.want)
			}
		})
	}
}

func TestPresenceUpdateRecordsSuppliedUserFields(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		payload               string
		avatar, globalName    bool
		wantAvatar, wantError bool
	}{
		{name: "id_only", payload: `{"user":{"id":"1"},"status":"online"}`},
		{name: "null_avatar", payload: `{"user":{"id":"1","avatar":null},"status":"online"}`, avatar: true},
		{name: "avatar_and_name", payload: `{"user":{"id":"1","avatar":"a","global_name":"g"},"status":"online"}`, avatar: true, globalName: true, wantAvatar: true},
		{name: "escaped_key", payload: `{"user":{"id":"1","\u0061vatar":null},"status":"online"}`, avatar: true},
		{name: "key_text_elsewhere", payload: `{"user":{"id":"1"},"status":"online","activities":[{"name":"\"avatar\" global_name","type":0}]}`},
		{name: "activity_key", payload: `{"user":{"id":"1"},"status":"online","activities":[{"name":"x","type":0,"avatar":"y"}]}`},
		{name: "malformed_avatar", payload: `{"user":{"id":"1","avatar":123},"status":"online"}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var update PresenceUpdate
			err := json.Unmarshal([]byte(tc.payload), &update)
			if (err != nil) != tc.wantError {
				t.Fatalf("Unmarshal error = %v, want error %t", err, tc.wantError)
			}
			if update.User == nil || update.User.ID != "1" || update.Status != StatusOnline {
				t.Fatalf("decoded update = %+v", update)
			}
			if update.avatarPresent != tc.avatar || update.globalNamePresent != tc.globalName {
				t.Fatalf("avatar/global_name present = %t/%t, want %t/%t", update.avatarPresent, update.globalNamePresent, tc.avatar, tc.globalName)
			}
			if (update.User.Avatar == "a") != tc.wantAvatar {
				t.Fatalf("avatar = %q", update.User.Avatar)
			}
		})
	}
}

// presenceUpdateTwoPass is PresenceUpdate.UnmarshalJSON before the fast
// path: it always runs the second pass over the payload.
func presenceUpdateTwoPass(data []byte) (PresenceUpdate, error) {
	type presenceUpdate PresenceUpdate
	var decoded presenceUpdate
	err := json.Unmarshal(data, &decoded)
	p := PresenceUpdate(decoded)
	if err != nil {
		return p, err
	}
	var fields struct {
		User struct {
			Avatar     json.RawMessage `json:"avatar"`
			GlobalName json.RawMessage `json:"global_name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return p, err
	}
	p.avatarPresent = len(fields.User.Avatar) != 0
	p.globalNamePresent = len(fields.User.GlobalName) != 0
	return p, nil
}

// caseVariants returns every ASCII upper/lower case spelling of name.
func caseVariants(name string) []string {
	variants := []string{""}
	for _, c := range name {
		var next []string
		for _, v := range variants {
			next = append(next, v+strings.ToLower(string(c)))
			if upper := strings.ToUpper(string(c)); upper != strings.ToLower(string(c)) {
				next = append(next, v+upper)
			}
		}
		variants = next
	}
	return variants
}

// encoding/json matches keys case-insensitively, so "AVATAR" or "Global_Name"
// in the user object must still be recorded as supplied, exactly as the two
// pass decode does.
func TestPresenceUpdateMatchesTwoPassDecode(t *testing.T) {
	var payloads []string
	for _, key := range caseVariants("avatar") {
		payloads = append(payloads,
			`{"user":{"id":"1","`+key+`":null},"status":"online"}`,
			`{"user":{"id":"1","`+key+`":"a"},"status":"online"}`,
			`{"user":{"id":"1","avatar":"a","`+key+`":null},"status":"online"}`,
			`{"user":{"id":"1"},"status":"online","activities":[{"name":"x","type":0,"`+key+`":"y"}]}`,
		)
	}
	for _, key := range caseVariants("global_name") {
		payloads = append(payloads,
			`{"user":{"id":"1","`+key+`":null},"status":"online"}`,
			`{"user":{"id":"1","`+key+`":"g"},"status":"online"}`,
		)
	}
	for _, user := range caseVariants("user") {
		payloads = append(payloads,
			`{"`+user+`":{"id":"1","Avatar":null,"GLOBAL_NAME":null},"status":"online"}`,
			`{"`+user+`":{"id":"1"},"status":"online"}`,
		)
	}
	payloads = append(payloads,
		`{"user":{"id":"1"},"status":"online"}`,
		`{"user":{"id":"1","\u0041VATAR":null},"status":"online"}`,
		`{"user":{"id":"1","glob\u0061l_name":null},"status":"online"}`,
		`{"user":{"id":"1","avatar\u0000":null},"status":"online"}`,
		`{"user":{"id":"1","avatarx":null,"xglobal_name":null},"status":"online"}`,
		`{"user":{"id":"1","name":"\"AVATAR\""},"status":"online"}`,
		`{"user":{"id":"1"},"status":"online","activities":[{"name":"\"Avatar\" Global_Name","type":0}]}`,
		`{"user":{"id":"1","AVATAR":123},"status":"online"}`,
		`{"user":{"id":"1","avatar":null}}`,
		`{"user":{"id":"1"}}`,
		`{"user":{"id":"1"},"status":5}`,
		`{"user":{"id":"1","avatar":null},"status":5}`,
	)

	for _, payload := range payloads {
		want, wantErr := presenceUpdateTwoPass([]byte(payload))
		var got PresenceUpdate
		gotErr := json.Unmarshal([]byte(payload), &got)
		if (gotErr != nil) != (wantErr != nil) {
			t.Fatalf("%s: error = %v, two pass error = %v", payload, gotErr, wantErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\ngot       %+v (avatar %t, global_name %t)\ntwo pass  %+v (avatar %t, global_name %t)",
				payload, got, got.avatarPresent, got.globalNamePresent, want, want.avatarPresent, want.globalNamePresent)
		}
	}
}

// The fast path only looks for ASCII case variants of the key names. That is
// only exact if no other rune folds to one of their letters, which is true for
// these names but not for every name (K and S have non-ASCII folds).
func TestPresenceUserFieldNamesHaveOnlyASCIIFolds(t *testing.T) {
	for _, c := range "avatarglobal_name" {
		for r := unicode.SimpleFold(c); r != c; r = unicode.SimpleFold(r) {
			if r >= utf8.RuneSelf {
				t.Fatalf("%q folds to non-ASCII %q", c, r)
			}
		}
	}
}

func BenchmarkPresenceUpdateUnmarshal(b *testing.B) {
	payload := []byte(`{"user":{"id":"80351110224678912"},"status":"online","guild_id":"613425648685547541","client_status":{"desktop":"online"},"activities":[{"type":0,"timestamps":{"start":1700000000000},"state":"In a match","session_id":"aa2f8a9d6f1c4b0e","name":"Some Game","id":"5cb2b8ab0bcd0b0","details":"Ranked","created_at":1700000000123,"assets":{"large_text":"Map","large_image":"mp:external/abc/def.png"},"application_id":"356875570916753438"}]}`)

	b.ReportAllocs()
	for b.Loop() {
		var update PresenceUpdate
		if err := json.Unmarshal(payload, &update); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMessageReactionGatewayFieldsUnmarshal(t *testing.T) {
	t.Run("add burst reaction", func(t *testing.T) {
		var event MessageReactionAdd
		data := `{"user_id":"user","channel_id":"channel","message_id":"message","guild_id":"guild","member":{},"emoji":{"name":"sparkle"},"message_author_id":"author","burst":true,"burst_colors":["#ff00ff"],"type":1}`
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.MessageReaction == nil {
			t.Fatal("MessageReaction is nil")
		}
		if event.MessageAuthorID != "author" || !event.Burst || event.Type != ReactionTypeBurst {
			t.Fatalf("MessageReaction = %#v", event.MessageReaction)
		}
		if len(event.BurstColors) != 1 || event.BurstColors[0] != "#ff00ff" {
			t.Fatalf("BurstColors = %#v", event.BurstColors)
		}
	})

	t.Run("remove burst reaction", func(t *testing.T) {
		var event MessageReactionRemove
		data := `{"user_id":"user","channel_id":"channel","message_id":"message","guild_id":"guild","emoji":{"name":"sparkle"},"burst":true,"type":1}`
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.MessageReaction == nil {
			t.Fatal("MessageReaction is nil")
		}
		if !event.Burst || event.Type != ReactionTypeBurst {
			t.Fatalf("MessageReaction = %#v", event.MessageReaction)
		}
	})

	t.Run("omitted fields default to normal", func(t *testing.T) {
		var event MessageReactionAdd
		data := `{"user_id":"user","channel_id":"channel","message_id":"message","emoji":{"name":"wave"}}`
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if event.MessageReaction == nil {
			t.Fatal("MessageReaction is nil")
		}
		if event.Burst || event.Type != ReactionTypeNormal || event.BurstColors != nil {
			t.Fatalf("MessageReaction = %#v", event.MessageReaction)
		}
	})
}

func TestCurrentGatewayEventDispatch(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		handle  func(*testing.T, *Session, *bool)
	}{
		{
			name:    "rate limited",
			payload: `{"op":0,"s":1,"t":"RATE_LIMITED","d":{"opcode":31,"retry_after":2.5,"meta":{"guild_id":"guild","nonce":"nonce"}}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *RateLimited) {
					*called = true
					if event.Opcode != 31 || event.RetryAfter != 2.5 {
						t.Fatalf("RateLimited = %#v", event)
					}
					if event.Meta == nil || event.Meta.GuildID != "guild" || event.Meta.Nonce != "nonce" {
						t.Fatalf("RateLimited meta = %#v", event.Meta)
					}
				})
			},
		},
		{
			name:    "channel info",
			payload: `{"op":0,"s":2,"t":"CHANNEL_INFO","d":{"guild_id":"guild","channels":[{"id":"channel","status":"Focus","voice_start_time":1770000000}]}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *ChannelInfo) {
					*called = true
					if event.GuildID != "guild" || len(event.Channels) != 1 {
						t.Fatalf("ChannelInfo = %#v", event)
					}
					if event.Channels[0].Status == nil || *event.Channels[0].Status != "Focus" {
						t.Fatalf("ChannelInfo status = %#v", event.Channels[0].Status)
					}
					if event.Channels[0].VoiceStartTime == nil || *event.Channels[0].VoiceStartTime != 1770000000 {
						t.Fatalf("ChannelInfo voice start time = %#v", event.Channels[0].VoiceStartTime)
					}
				})
			},
		},
		{
			name:    "voice channel status update",
			payload: `{"op":0,"s":3,"t":"VOICE_CHANNEL_STATUS_UPDATE","d":{"id":"channel","guild_id":"guild","status":"Live"}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *VoiceChannelStatusUpdate) {
					*called = true
					if event.ID != "channel" || event.GuildID != "guild" || event.Status == nil || *event.Status != "Live" {
						t.Fatalf("VoiceChannelStatusUpdate = %#v", event)
					}
				})
			},
		},
		{
			name:    "voice channel start time update",
			payload: `{"op":0,"s":4,"t":"VOICE_CHANNEL_START_TIME_UPDATE","d":{"id":"channel","guild_id":"guild","voice_start_time":1770000001}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *VoiceChannelStartTimeUpdate) {
					*called = true
					if event.ID != "channel" || event.GuildID != "guild" || event.VoiceStartTime == nil || *event.VoiceStartTime != 1770000001 {
						t.Fatalf("VoiceChannelStartTimeUpdate = %#v", event)
					}
				})
			},
		},
		{
			name:    "soundboard sounds",
			payload: `{"op":0,"s":5,"t":"SOUNDBOARD_SOUNDS","d":{"guild_id":"guild","soundboard_sounds":[{"sound_id":"sound","name":"Airhorn","volume":1}]}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *SoundboardSounds) {
					*called = true
					if event.GuildID != "guild" || len(event.SoundboardSounds) != 1 || event.SoundboardSounds[0].SoundID != "sound" {
						t.Fatalf("SoundboardSounds = %#v", event)
					}
				})
			},
		},
		{
			name:    "voice channel effect send",
			payload: `{"op":0,"s":6,"t":"VOICE_CHANNEL_EFFECT_SEND","d":{"channel_id":"channel","guild_id":"guild","user_id":"user","sound_id":123,"sound_volume":0.75}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *VoiceChannelEffectSend) {
					*called = true
					if event.ChannelID != "channel" || event.GuildID != "guild" || event.SoundID != "123" {
						t.Fatalf("VoiceChannelEffectSend = %#v", event)
					}
				})
			},
		},
		{
			name:    "stage instance create",
			payload: `{"op":0,"s":7,"t":"STAGE_INSTANCE_CREATE","d":{"id":"stage","guild_id":"guild","channel_id":"channel","topic":"Topic","privacy_level":2}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *StageInstanceEventCreate) {
					*called = true
					if event.StageInstance == nil || event.ID != "stage" || event.GuildID != "guild" {
						t.Fatalf("StageInstanceEventCreate = %#v", event)
					}
				})
			},
		},
		{
			name:    "stage instance update",
			payload: `{"op":0,"s":8,"t":"STAGE_INSTANCE_UPDATE","d":{"id":"stage","guild_id":"guild","channel_id":"channel","topic":"Updated","privacy_level":2}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *StageInstanceEventUpdate) {
					*called = true
					if event.StageInstance == nil || event.ID != "stage" || event.Topic != "Updated" {
						t.Fatalf("StageInstanceEventUpdate = %#v", event)
					}
				})
			},
		},
		{
			name:    "stage instance delete",
			payload: `{"op":0,"s":9,"t":"STAGE_INSTANCE_DELETE","d":{"id":"stage","guild_id":"guild","channel_id":"channel","topic":"Topic","privacy_level":2}}`,
			handle: func(t *testing.T, session *Session, called *bool) {
				session.AddHandler(func(_ *Session, event *StageInstanceEventDelete) {
					*called = true
					if event.StageInstance == nil || event.ID != "stage" || event.ChannelID != "channel" {
						t.Fatalf("StageInstanceEventDelete = %#v", event)
					}
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{
				SyncEvents: true,
				sequence:   new(int64),
			}
			called := false
			tt.handle(t, session, &called)

			event, err := session.onEvent(websocket.TextMessage, []byte(tt.payload))
			if err != nil {
				t.Fatalf("onEvent returned error: %v", err)
			}
			if event.Struct == nil {
				t.Fatal("onEvent returned nil Struct")
			}
			if !called {
				t.Fatal("handler was not called")
			}
		})
	}
}
