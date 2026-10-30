package discordgo

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/gorilla/websocket"
)

func TestVoiceTerminalCloseConcurrentJoin(t *testing.T) {
	for i := 0; i < 32; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			conn, peer := newGatewayTestConnection(t)
			session := &Session{LogLevel: -1, VoiceConnections: make(map[string]*VoiceConnection)}
			voice := &VoiceConnection{
				LogLevel: -1, GuildID: "guild", session: session,
				wsConn: conn, close: make(chan struct{}),
			}
			session.VoiceConnections["guild"] = voice
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					// The public join configures the registered voice connection
					// before its send notices that the gateway is unavailable.
					if _, err := session.ChannelVoiceJoin("guild", "channel", false, false); !errors.Is(err, ErrWSNotFound) {
						t.Errorf("ChannelVoiceJoin error = %v, want ErrWSNotFound", err)
						return
					}
					runtime.Gosched()
				}
			}()
			defer func() { close(stop); <-done }()
			if err := peer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
				t.Fatal(err)
			}
			voice.wsListen(conn, voice.close)
			voice.RLock()
			defer voice.RUnlock()
			if voice.wsConn != nil || voice.close != nil {
				t.Error("terminal listener retained its transport")
			}
		})
	}
}

func TestVoiceTerminalClosePreservesNewGeneration(t *testing.T) {
	for _, code := range []int{4014, 4017, 4021, 4022} {
		for _, pause := range []string{"received %d manual disconnection", "disconnect due to %d manual disconnection"} {
			t.Run(fmt.Sprintf("%d/%s", code, pause), func(t *testing.T) {
				oldConn, peer := newGatewayTestConnection(t)
				replacement, _ := newGatewayTestConnection(t)
				session := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
				voice := &VoiceConnection{
					GuildID: "guild", LogLevel: LogInformational,
					session: session, wsConn: oldConn, close: make(chan struct{}),
				}
				session.VoiceConnections[voice.GuildID] = voice
				oldClose := voice.close
				defer voice.Close()
				// Model a region update replacing the transport after the listener
				// checks its close signal, or after it detaches the old websocket.
				previousLogger := Logger
				defer func() { Logger = previousLogger }()
				replaced := false
				Logger = func(_, _ int, format string, _ ...interface{}) {
					if format != pause || replaced {
						return
					}
					replaced = true
					voice.Close()
					voice.Lock()
					voice.wsConn = replacement
					voice.close = make(chan struct{})
					voice.Unlock()
				}
				if err := peer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, "")); err != nil {
					t.Fatal(err)
				}
				voice.wsListen(oldConn, oldClose)
				if !replaced {
					t.Fatal("replacement interleaving was not exercised")
				}
				if voice.wsConn != replacement {
					t.Fatal("stale close terminated replacement websocket")
				}
				if session.VoiceConnections[voice.GuildID] != voice {
					t.Fatal("stale close unregistered replacement generation")
				}
				select {
				case <-voice.close:
					t.Fatal("replacement close signal was closed")
				default:
				}
			})
		}
	}
}

func TestVoiceTerminalClosePreservesOpeningReplacement(t *testing.T) {
	oldConn, peer := newGatewayTestConnection(t)
	session := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	voice := &VoiceConnection{
		GuildID: "guild", LogLevel: LogInformational,
		session: session, wsConn: oldConn, close: make(chan struct{}),
	}
	session.VoiceConnections[voice.GuildID] = voice
	defer voice.Close()
	previousLogger := Logger
	defer func() { Logger = previousLogger }()
	attempt := &voiceOpenAttempt{cancel: func() {}, canceled: make(chan struct{})}
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format != "disconnect due to %d manual disconnection" {
			return
		}
		// open() registers its attempt before a new websocket is installed.
		voice.lifecycleMu.Lock()
		voice.Lock()
		voice.opening = attempt
		voice.Unlock()
		voice.lifecycleMu.Unlock()
	}
	if err := peer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
		t.Fatal(err)
	}
	voice.wsListen(oldConn, voice.close)
	if voice.opening != attempt {
		t.Fatal("stale close canceled replacement opening")
	}
	if session.VoiceConnections[voice.GuildID] != voice {
		t.Fatal("stale close unregistered opening connection")
	}
	select {
	case <-attempt.canceled:
		t.Fatal("replacement opening was canceled")
	default:
	}
}
