package discordgo

import (
	"encoding/json"
	"testing"

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
