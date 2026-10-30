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

func TestVoiceServerUpdatesPreserveReceiveOrder(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		name := "interface"
		if gateway {
			name = "gateway"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var oldDials atomic.Int32
				session := &Session{LogLevel: LogInformational, sequence: new(int64), State: NewState(),
					Dialer: &websocket.Dialer{NetDialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
						if addr == "old.invalid:443" {
							oldDials.Add(1)
						}
						return nil, errors.New("stop after observing the requested dial")
					}}}
				voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", UserID: "bot", ChannelID: "channel",
					session: session, sessionID: "session"}
				session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
				defer voice.Close()
				paused, release := make(chan struct{}), make(chan struct{})
				var pauseOnce atomic.Bool
				oldLogger := Logger
				Logger = func(_, _ int, format string, _ ...interface{}) {
					if format == "called" && pauseOnce.CompareAndSwap(false, true) {
						close(paused)
						<-release
					}
				}
				defer func() { Logger = oldLogger }()
				dispatch := func(old bool) {
					if gateway {
						payload := `{"op":0,"s":2,"t":"VOICE_SERVER_UPDATE","d":{"guild_id":"guild","token":"new-token","endpoint":"new.invalid"}}`
						if old {
							payload = `{"op":0,"s":1,"t":"VOICE_SERVER_UPDATE","d":{"guild_id":"guild","token":"old-token","endpoint":"old.invalid"}}`
						}
						if _, err := session.onEventForConnection(nil, websocket.TextMessage, []byte(payload)); err != nil {
							t.Fatal(err)
						}
					} else {
						update := &VoiceServerUpdate{GuildID: "guild", Token: "new-token", Endpoint: "new.invalid"}
						if old {
							update.Token, update.Endpoint = "old-token", "old.invalid"
						}
						session.onInterface(update)
					}
				}
				dispatch(true)
				<-paused
				dispatch(false)
				synctest.Wait()
				voice.RLock()
				newApplied := voice.token == "new-token" && voice.endpoint == "new.invalid"
				voice.RUnlock()
				close(release)
				synctest.Wait()
				if !newApplied {
					t.Fatal("newer server update did not apply while old worker was paused")
				}
				voice.RLock()
				preserved := voice.token == "new-token" && voice.endpoint == "new.invalid"
				voice.RUnlock()
				if !preserved || oldDials.Load() != 0 {
					t.Errorf("old server update rolled back credentials: latest preserved=%t, obsolete dials=%d", preserved, oldDials.Load())
				}
			})
		})
	}
}

func TestVoiceServerUpdateSupersededDuringTeardown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var oldDials atomic.Int32
		session := &Session{LogLevel: LogInformational, State: NewState(),
			Dialer: &websocket.Dialer{NetDialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
				if addr == "old.invalid:443" {
					oldDials.Add(1)
				}
				return nil, errors.New("stop after observing the requested dial")
			}}}
		voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", UserID: "bot", ChannelID: "channel",
			session: session, sessionID: "session"}
		session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
		defer voice.Close()
		backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
			// A new event can arrive while the previous worker closes its backend.
			session.onInterface(&VoiceServerUpdate{GuildID: "guild", Token: "new-token", Endpoint: "new.invalid"})
		}}
		installFakeDAVE(voice, backend, 1)
		paused, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		oldLogger := Logger
		Logger = func(_, _ int, format string, _ ...interface{}) {
			if format == "called" && calls.Add(1) == 2 {
				close(paused)
				<-release
			}
		}
		defer func() { Logger = oldLogger }()
		session.onInterface(&VoiceServerUpdate{GuildID: "guild", Token: "old-token", Endpoint: "old.invalid"})
		<-paused
		synctest.Wait()
		if got := oldDials.Load(); got != 0 {
			t.Errorf("superseded server update started %d obsolete dials after teardown", got)
		}
		close(release)
		synctest.Wait()
		voice.RLock()
		preserved := voice.token == "new-token" && voice.endpoint == "new.invalid"
		voice.RUnlock()
		if !preserved {
			t.Error("latest server update did not apply after teardown")
		}
	})
}
