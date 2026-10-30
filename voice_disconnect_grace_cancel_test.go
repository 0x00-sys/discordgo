package discordgo

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceDisconnectCancelsServerUpdateGrace(t *testing.T) {
	for _, tc := range []struct {
		name       string
		disconnect bool
	}{
		{name: "active"},
		{name: "disconnect", disconnect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := newGatewayTestConnection(t)
			// Avoid a close reply whose deadline would use the bubble's fake clock.
			conn.SetCloseHandler(func(int, string) error { return nil })
			if err := peer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4014, "test disconnect")); err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				session := &Session{LogLevel: -1, VoiceConnections: make(map[string]*VoiceConnection)}
				voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session,
					wsConn: conn, close: make(chan struct{})}
				session.VoiceConnections[voice.GuildID] = voice
				defer voice.Close()
				closeSignal := voice.close
				done := make(chan struct{})
				go func() {
					defer close(done)
					voice.wsListen(conn, closeSignal)
				}()
				synctest.Wait()
				voice.RLock()
				detached := voice.wsConn == nil
				voice.RUnlock()
				if !detached {
					t.Fatal("listener did not reach the server-update grace wait")
				}
				started := time.Now()
				if tc.disconnect {
					if err := voice.Disconnect(); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					select {
					case <-done:
					default:
						t.Error("Disconnect left the voice listener in its server-update wait")
					}
				}
				// Join even on failure, including the listener's deferred socket close.
				<-done
				want := 5 * time.Second
				if tc.disconnect {
					want = 0
				}
				if elapsed := time.Since(started); elapsed != want {
					t.Errorf("listener exited after %v, want %v", elapsed, want)
				}
			})
		})
	}
}
