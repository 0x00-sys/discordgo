package discordgo

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceEventsDoNotChangeReplacementState(t *testing.T) {
	for _, test := range []struct {
		name string
		kind int
		data []byte
	}{
		{"speaking", websocket.TextMessage, []byte(`{"op":5,"seq":7,"d":{"user_id":"stale","ssrc":55,"speaking":1}}`)},
		{"connect", websocket.TextMessage, []byte(`{"op":11,"seq":7,"d":{"user_ids":["stale"]}}`)},
		{"disconnect", websocket.TextMessage, []byte(`{"op":13,"seq":7,"d":{"user_id":"replacement"}}`)},
		{"prepare transition", websocket.TextMessage, []byte(`{"op":21,"seq":7,"d":{"transition_id":7,"protocol_version":1}}`)},
		{"execute transition", websocket.TextMessage, []byte(`{"op":22,"seq":7,"d":{"transition_id":7}}`)},
		{"prepare epoch", websocket.TextMessage, []byte(`{"op":24,"seq":7,"d":{"epoch":2,"protocol_version":0}}`)},
		{"external sender", websocket.BinaryMessage, daveBinaryFrame(7, 25, 1)},
		{"proposals", websocket.BinaryMessage, daveBinaryFrame(7, 27, 0, 1)},
		{"commit", websocket.BinaryMessage, daveBinaryFrame(7, 29, 0, 7, 1)},
		{"welcome", websocket.BinaryMessage, daveBinaryFrame(7, 30, 0, 7, 1)},
	} {
		for _, phase := range []string{"dispatch", "handler"} {
			if phase == "handler" && test.kind == websocket.BinaryMessage {
				continue
			}
			t.Run(test.name+"/"+phase, func(t *testing.T) {
				oldConn, _ := newGatewayTestConnection(t)
				newConn, _ := newGatewayTestConnection(t)
				oldClose := make(chan struct{})
				voice := &VoiceConnection{LogLevel: -1, wsConn: oldConn, close: oldClose}
				paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				if phase == "handler" {
					voice.LogLevel = LogDebug
					oldLogger := Logger
					Logger = func(_, _ int, format string, _ ...interface{}) {
						if format == "received: %s" {
							close(paused)
							<-release
						}
					}
					defer func() {
						closeChannel(release)
						<-done
						Logger = oldLogger
					}()
					go func() {
						voice.dispatchVoiceMessage(oldConn, oldClose, test.kind, test.data)
						close(done)
					}()
					select {
					case <-paused:
					case <-time.After(time.Second):
						t.Fatal("voice handler did not reach the logging barrier")
					}
				}
				voice.Close()
				defer voice.Close()
				backend := &fakeVoiceDAVESession{maxVersion: 2}
				installFakeDAVE(voice, backend, 1)
				voice.Lock()
				voice.wsConn = newConn
				voice.close = make(chan struct{})
				voice.sequence, voice.sequenceSet = 42, true
				voice.daveSSRCToUserID[55] = "replacement"
				voice.daveUsers["replacement"] = struct{}{}
				voice.davePendingTransitions[7] = 0
				voice.davePreparedProtocolVersion = 2
				voice.Unlock()

				// The old reader already received a frame before Close, but was
				// descheduled before handing that frame to the dispatcher.
				if phase == "handler" {
					close(release)
					<-done
				} else {
					voice.dispatchVoiceMessage(oldConn, oldClose, test.kind, test.data)
				}

				voice.RLock()
				if voice.sequence != 42 {
					t.Errorf("old event changed replacement sequence to %d", voice.sequence)
				}
				if voice.daveSSRCToUserID[55] != "replacement" || len(voice.daveUsers) != 1 {
					t.Error("old event changed replacement participants")
				}
				pending, exists := voice.davePendingTransitions[7]
				if voice.daveProtocolVersion != 1 || voice.davePreparedProtocolVersion != 2 || !exists || pending != 0 {
					t.Error("old event changed replacement DAVE transitions")
				}
				voice.RUnlock()
				backend.mu.Lock()
				calls := len(backend.addedUsers) + len(backend.removedUsers) + len(backend.preparedTransitions) + len(backend.executedTransitions) + len(backend.preparedEpochs) + len(backend.externalSenderPackages) + len(backend.proposals) + len(backend.commits) + len(backend.welcomeMessages)
				backend.mu.Unlock()
				if calls != 0 {
					t.Errorf("old event called the replacement DAVE backend %d times", calls)
				}
			})
		}
	}
}
