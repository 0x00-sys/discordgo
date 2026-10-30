package discordgo

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/gorilla/websocket"
)

func TestQueuedVoiceReconnectDoesNotChangeReplacement(t *testing.T) {
	for _, test := range []struct {
		name         string
		closeCode    int
		reconnecting bool
		sameClose    bool
	}{
		{"resumable", 4015, false, false},
		{"new session", 4006, false, false},
		{"already reconnecting", 4006, true, false},
		{"resumed replacement", 4015, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldConn, oldPeer := newGatewayTestConnection(t)
			newConn, _ := newGatewayTestConnection(t)
			// Avoid a real network close reply using the bubble's fake clock.
			oldConn.SetCloseHandler(func(int, string) error { return nil })
			if err := oldPeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(test.closeCode, "test disconnect")); err != nil {
				t.Fatal(err)
			}
			udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = udp.Close() }()
			aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
			if err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				var dials atomic.Int32
				session := &Session{VoiceConnections: make(map[string]*VoiceConnection), Dialer: &websocket.Dialer{
					NetDialContext: func(context.Context, string, string) (net.Conn, error) {
						dials.Add(1)
						return nil, errors.New("unexpected resume dial")
					},
				}}
				oldClose := make(chan struct{})
				voice := &VoiceConnection{LogLevel: LogError, GuildID: "guild", wsConn: oldConn, close: oldClose, session: session,
					endpoint: "voice.invalid", sessionID: "session", token: "dummy-token"}
				session.VoiceConnections[voice.GuildID] = voice
				paused, release := make(chan struct{}), make(chan struct{})
				oldLogger := Logger
				Logger = func(_, _ int, format string, _ ...interface{}) {
					if format == "voice endpoint %s websocket closed unexpectedly, %s" {
						close(paused)
						<-release
					}
				}
				defer func() { Logger = oldLogger }()
				defer voice.Close()
				go voice.wsListen(oldConn, oldClose)
				<-paused
				replacementClose := oldClose
				// A successful resume replaces the websocket but retains this signal.
				if !test.sameClose {
					voice.Close()
					replacementClose = make(chan struct{})
				}
				backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
					// Stop fallback retries only after the erroneous teardown.
					session.Lock()
					delete(session.VoiceConnections, voice.GuildID)
					session.Unlock()
				}}
				installFakeDAVE(voice, backend, 1)
				voice.Lock()
				voice.wsConn, voice.close = newConn, replacementClose
				voice.udpConn, voice.aead = udp, aead
				voice.Ready = true
				voice.reconnecting = test.reconnecting
				voice.Unlock()
				close(release)
				synctest.Wait()
				voice.RLock()
				preserved := voice.wsConn == newConn && voice.close == replacementClose && voice.udpConn == udp && voice.aead == aead && voice.Ready
				reconnectPreserved := voice.reconnecting == test.reconnecting && !voice.reconnectAgain
				voice.RUnlock()
				if !preserved || !reconnectPreserved {
					t.Errorf("old listener changed replacement: transport preserved=%t, reconnect state preserved=%t", preserved, reconnectPreserved)
				}
				if got := dials.Load(); got != 0 {
					t.Errorf("old listener started %d resume dials", got)
				}
				backend.mu.Lock()
				closed := backend.closed
				backend.mu.Unlock()
				if closed != 0 {
					t.Errorf("old listener closed replacement DAVE backend %d times", closed)
				}
			})
		})
	}
}
