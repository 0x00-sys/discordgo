package discordgo

import (
	"encoding/json"
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/gorilla/websocket"
)

func TestVoiceStateAppliedBeforeCallbacks(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%v", gateway), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				session.SyncEvents = true
				session.State.User = &User{ID: "bot"}
				if err := session.State.GuildAdd(&Guild{ID: "guild"}); err != nil {
					t.Fatal(err)
				}
				voice := &VoiceConnection{GuildID: "guild", session: session}
				session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
				callbacks := 0
				session.AddHandler(func(s *Session, update *VoiceStateUpdate) {
					// Application handlers must still run outside the session lock.
					s.Lock()
					callbacks++
					s.Unlock()
					voice.RLock()
					defer voice.RUnlock()
					if voice.UserID != update.UserID || voice.ChannelID != update.ChannelID || voice.sessionID != update.SessionID {
						t.Errorf("callback saw voice state (%q, %q, %q), want (%q, %q, %q)", voice.UserID, voice.ChannelID, voice.sessionID, update.UserID, update.ChannelID, update.SessionID)
					}
				})
				for i := 0; i < 8; i++ {
					update := &VoiceStateUpdate{VoiceState: &VoiceState{
						GuildID: "guild", UserID: "bot", ChannelID: fmt.Sprint("channel-", i), SessionID: fmt.Sprint("session-", i),
					}}
					if gateway {
						message, err := json.Marshal(map[string]interface{}{"op": 0, "s": i + 1, "t": "VOICE_STATE_UPDATE", "d": update})
						if err != nil {
							t.Fatal(err)
						}
						if _, err := session.onEvent(websocket.TextMessage, message); err != nil {
							t.Fatal(err)
						}
					} else {
						session.handleEvent(voiceStateUpdateEventType, update)
					}
				}
				synctest.Wait()
				if callbacks != 8 {
					t.Errorf("callbacks = %d, want 8", callbacks)
				}
				if voice.ChannelID != "channel-7" || voice.sessionID != "session-7" {
					t.Errorf("voice state after all workers finished = (%q, %q), want latest dispatch", voice.ChannelID, voice.sessionID)
				}
			})
		})
	}
}
