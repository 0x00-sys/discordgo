package discordgo

import (
	"net"
	"testing"
	"testing/synctest"

	"github.com/gorilla/websocket"
)

func TestQueuedVoiceHandshakeDoesNotChangeReplacement(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
	}{
		{"READY", `{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":1,"modes":["aead_aes256_gcm_rtpsize"]}}`},
		{"malformed READY", `{"op":2,"d":{"ssrc":"invalid"}}`},
		{"unsupported READY", `{"op":2,"d":{"modes":["unsupported"]}}`},
		{"SESSION DESCRIPTION", string(voiceSessionDescriptionMessage(t, voiceModeAES256GCMRTPSize, make([]int, 32)))},
		{"malformed SESSION DESCRIPTION", `{"op":4,"d":{"secret_key":"invalid"}}`},
		{"mismatched SESSION DESCRIPTION", `{"op":4,"d":{"mode":"unsupported"}}`},
		{"HELLO", `{"op":8,"d":{"heartbeat_interval":1000}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			testQueuedVoiceHandshake(t, test.message)
		})
	}
}

func testQueuedVoiceHandshake(t *testing.T, message string) {
	t.Helper()
	oldConn, _ := newGatewayTestConnection(t)
	newConn, _ := newGatewayTestConnection(t)
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		voice := &VoiceConnection{LogLevel: LogDebug, wsConn: oldConn, close: make(chan struct{})}
		paused, release := make(chan struct{}), make(chan struct{})
		heartbeatStarted := false
		oldLogger := Logger
		Logger = func(_, _ int, format string, _ ...interface{}) {
			if format == "received: %s" {
				close(paused)
				<-release
			}
			if format == "sending heartbeat packet" {
				heartbeatStarted = true
			}
		}
		defer func() { Logger = oldLogger }()
		defer voice.Close()
		go voice.dispatchVoiceMessage(voice.wsConn, voice.close, websocket.TextMessage, []byte(message))
		<-paused
		voice.Close()
		// A new connection has already completed its handshake while the old
		// handshake worker was delayed by the scheduler or application logger.
		voice.Lock()
		voice.wsConn = newConn
		voice.close = make(chan struct{})
		voice.udpConn = udp
		voice.aead = aead
		voice.encryptionMode = voiceModeAES256GCMRTPSize
		voice.op2.SSRC = 42
		voice.Ready = true
		voice.Unlock()
		close(release)
		synctest.Wait()
		voice.RLock()
		defer voice.RUnlock()
		if !voice.Ready || voice.aead != aead || voice.op2.SSRC != 42 || voice.wsConn != newConn {
			t.Errorf("old handshake overwrote replacement: ready=%t, cipher preserved=%t, SSRC=%d, socket preserved=%t", voice.Ready, voice.aead == aead, voice.op2.SSRC, voice.wsConn == newConn)
		}
		if heartbeatStarted {
			t.Error("old HELLO started a heartbeat on the replacement connection")
		}
	})
}
