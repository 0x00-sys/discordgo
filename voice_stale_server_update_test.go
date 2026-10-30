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

func TestQueuedVoiceServerUpdateDoesNotChangeReplacement(t *testing.T) {
	oldConn, _ := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	voiceConn, _ := newGatewayTestConnection(t)
	synctest.Test(t, func(t *testing.T) {
		var dials atomic.Int32
		session := &Session{LogLevel: LogInformational, wsConn: oldConn,
			listening: make(chan interface{}), sequence: new(int64), State: NewState(),
			VoiceConnections: make(map[string]*VoiceConnection), Dialer: &websocket.Dialer{
				NetDialContext: func(context.Context, string, string) (net.Conn, error) {
					dials.Add(1)
					return nil, errors.New("unexpected old server-update dial")
				},
			}}
		defer func() { _ = session.Close() }()
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
		_, err := session.onEventForConnection(oldConn, websocket.TextMessage,
			[]byte(`{"op":0,"s":1,"t":"VOICE_SERVER_UPDATE","d":{"guild_id":"guild","token":"old-token","endpoint":"old.invalid"}}`))
		if err != nil {
			t.Fatal(err)
		}
		<-paused
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", UserID: "bot", ChannelID: "replacement-channel",
			session: session, sessionID: "replacement-session", token: "replacement-token", endpoint: "replacement.invalid",
			wsConn: voiceConn, close: make(chan struct{}), Ready: true}
		backend := &fakeVoiceDAVESession{maxVersion: 1}
		installFakeDAVE(voice, backend, 1)
		session.Lock()
		session.wsConn, session.listening = newConn, make(chan interface{})
		session.VoiceConnections[voice.GuildID] = voice
		session.Unlock()
		close(release)
		synctest.Wait()
		voice.RLock()
		preserved := voice.wsConn == voiceConn && voice.Ready && voice.token == "replacement-token" && voice.endpoint == "replacement.invalid"
		voice.RUnlock()
		if !preserved {
			t.Error("queued old server update changed replacement voice transport or credentials")
		}
		if got := dials.Load(); got != 0 {
			t.Errorf("queued old server update started %d voice dials", got)
		}
		backend.mu.Lock()
		closed := backend.closed
		backend.mu.Unlock()
		if closed != 0 {
			t.Errorf("queued old server update closed replacement DAVE backend %d times", closed)
		}
	})
}

func TestCurrentVoiceServerUpdateAppliesCredentials(t *testing.T) {
	conn, _ := newGatewayTestConnection(t)
	synctest.Test(t, func(t *testing.T) {
		var dials atomic.Int32
		session := &Session{LogLevel: -1, wsConn: conn, sequence: new(int64), State: NewState(),
			Dialer: &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("stop after observing the requested dial")
			}}}
		voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", UserID: "bot", ChannelID: "channel",
			session: session, sessionID: "session"}
		session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
		defer func() { _ = session.Close() }()
		_, err := session.onEventForConnection(conn, websocket.TextMessage,
			[]byte(`{"op":0,"s":1,"t":"VOICE_SERVER_UPDATE","d":{"guild_id":"guild","token":"current-token","endpoint":"current.invalid"}}`))
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		voice.RLock()
		defer voice.RUnlock()
		if voice.token != "current-token" || voice.endpoint != "current.invalid" || dials.Load() != 1 {
			t.Fatal("current server update did not configure and start the voice connection")
		}
	})
}
