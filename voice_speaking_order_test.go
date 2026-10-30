package discordgo

import (
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/gorilla/websocket"
)

func TestVoiceSpeakingDispatchDoesNotRestoreDisconnectedUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		voice := &VoiceConnection{LogLevel: LogDebug, daveSSRCToUserID: make(map[uint32]string)}
		callback := make(chan struct{})
		voice.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) { close(callback) })
		paused, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		oldLogger := Logger
		Logger = func(_, _ int, format string, args ...interface{}) {
			if format == "received: %s" && strings.Contains(args[0].(string), `"op":5`) {
				once.Do(func() { close(paused) })
				<-release
			}
		}
		defer func() { Logger = oldLogger }()
		dispatched := make(chan struct{})
		go func() {
			// The same reader receives these two messages in wire order.
			voice.dispatchVoiceMessage(websocket.TextMessage, []byte(`{"op":5,"seq":1,"d":{"user_id":"remote","ssrc":55,"speaking":1}}`))
			voice.dispatchVoiceMessage(websocket.TextMessage, []byte(`{"op":13,"seq":2,"d":{"user_id":"remote"}}`))
			close(dispatched)
		}()
		<-paused
		// Let any separate dispatcher process the disconnect while the speaking
		// worker is paused. Internal state must still follow wire order.
		synctest.Wait()
		close(release)
		<-dispatched
		<-callback
		synctest.Wait()
		voice.RLock()
		defer voice.RUnlock()
		if user := voice.daveSSRCToUserID[55]; user != "" {
			t.Fatalf("disconnected user's SSRC was restored: %q", user)
		}
	})
}

func TestVoiceSpeakingCallbacksDoNotBlockDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		voice := &VoiceConnection{LogLevel: -1, daveSSRCToUserID: make(map[uint32]string)}
		started, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		voice.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {
			close(started)
			<-release
		})
		dispatched := make(chan struct{})
		go func() {
			voice.dispatchVoiceMessage(websocket.TextMessage, []byte(`{"op":5,"seq":1,"d":{"user_id":"remote","ssrc":55,"speaking":1}}`))
			voice.dispatchVoiceMessage(websocket.TextMessage, []byte(`{"op":13,"seq":2,"d":{"user_id":"remote"}}`))
			close(dispatched)
		}()
		<-started
		synctest.Wait()
		select {
		case <-dispatched:
		default:
			t.Error("speaking callback blocked the later disconnect event")
		}
		voice.RLock()
		defer voice.RUnlock()
		if user := voice.daveSSRCToUserID[55]; user != "" {
			t.Errorf("disconnected user's SSRC remains: %q", user)
		}
	})
}
