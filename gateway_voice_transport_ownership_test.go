package discordgo

import (
	"testing"
	"testing/synctest"
)

func TestGatewayVoiceReconnectPreservesNewTransport(t *testing.T) {
	oldConn, _ := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	synctest.Test(t, func(t *testing.T) {
		session, err := New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		session.LogLevel = LogInformational
		voice := &VoiceConnection{
			LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
			wsConn: oldConn, close: make(chan struct{}), Ready: true,
		}
		session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
		paused, release := make(chan struct{}), make(chan struct{})
		oldLogger := Logger
		Logger = func(_, _ int, format string, _ ...interface{}) {
			if format == "reconnecting voice connection to guild %s" {
				close(paused)
				<-release
			}
		}
		defer func() { Logger = oldLogger }()
		defer voice.Close()
		defer closeChannel(release)
		done := make(chan struct{})
		go func() {
			defer close(done)
			session.reconnectVoiceConnections(session.reconnectCancelSignal())
		}()
		<-paused

		// A voice server update can replace this transport independently of
		// the gateway's recovery batch. Model its completed installation.
		voice.Close()
		replacementClose := make(chan struct{})
		backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
			// Stop fallback retries only after the unwanted teardown.
			session.Lock()
			delete(session.VoiceConnections, "guild")
			session.Unlock()
		}}
		installFakeDAVE(voice, backend, 1)
		voice.Lock()
		voice.wsConn = newConn
		voice.close = replacementClose
		voice.Ready = true
		voice.Unlock()

		close(release)
		<-done
		synctest.Wait()
		voice.RLock()
		preserved := voice.wsConn == newConn && voice.close == replacementClose && voice.Ready
		reconnecting := voice.reconnecting || voice.reconnectAgain
		voice.RUnlock()
		backend.mu.Lock()
		closed := backend.closed
		backend.mu.Unlock()
		if !preserved || reconnecting || closed != 0 {
			t.Errorf("queued gateway recovery changed replacement: transport preserved=%t, reconnecting=%t, backend closes=%d", preserved, reconnecting, closed)
		}
		select {
		case <-replacementClose:
			t.Error("queued gateway recovery canceled the replacement transport")
		default:
		}
	})
}
