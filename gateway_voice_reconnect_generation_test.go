package discordgo

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCanceledGatewayReconnectPreservesNewVoice(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":60000}}`)); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":0,"s":1,"t":"READY","d":{"session_id":"session","user":{"id":"user"},"guilds":[]}}`)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	session, err := newGatewayOpenTestSession(server, "Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = LogInformational
	session.ShouldReconnectVoiceOnSessionError = true
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	workersStarted := make(chan struct{}, 4)
	var scheduled atomic.Int32
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		switch format {
		case "called":
			pc, _, _, _ := runtime.Caller(3)
			name := runtime.FuncForPC(pc).Name()
			if strings.HasSuffix(name, ".(*Session).heartbeat") || strings.HasSuffix(name, ".(*Session).listen") {
				workersStarted <- struct{}{}
			}
		case "successfully reconnected to gateway":
			close(paused)
			<-release
		case "reconnecting voice connection to guild %s":
			scheduled.Add(1)
		}
	}
	defer func() { Logger = oldLogger }()
	defer func() { _ = session.Close() }()
	defer closeChannel(release)
	go func() {
		defer close(done)
		session.reconnect(session.reconnectCancelSignal())
	}()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway recovery did not reach voice scheduling")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Open(); err != nil {
		t.Fatal(err)
	}
	// Both generations' heartbeat/listener startup logs must finish before
	// cleanup restores the global logger. Manual Close then stops them silently.
	for i := 0; i < 4; i++ {
		select {
		case <-workersStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("gateway workers did not start")
		}
	}
	transportClose := make(chan struct{})
	voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session, Ready: true, close: transportClose}
	defer voice.Close()
	backendClosed := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
		// Stop fallback retries only after the unwanted teardown has happened.
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
		close(backendClosed)
	}}
	installFakeDAVE(voice, backend, 1)
	session.Lock()
	session.VoiceConnections["guild"] = voice
	session.Unlock()
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("old gateway recovery did not finish")
	}
	if got := scheduled.Load(); got != 0 {
		t.Errorf("canceled gateway recovery scheduled %d new voice reconnects", got)
		select {
		case <-backendClosed:
		case <-time.After(time.Second):
			t.Fatal("scheduled voice recovery did not reach teardown")
		}
		// Wait for recovery to clear its state before inspecting and cleaning up.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(time.Second)
		for {
			voice.RLock()
			reconnecting := voice.reconnecting
			voice.RUnlock()
			if !reconnecting {
				break
			}
			select {
			case <-ticker.C:
			case <-timeout:
				t.Fatal("voice recovery did not finish")
			}
		}
	}
	voice.RLock()
	preserved := voice.Ready && voice.close == transportClose
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("old gateway recovery changed new voice: transport preserved=%t, backend closes=%d", preserved, closed)
	}
}
