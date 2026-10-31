package discordgo

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

func TestOpusSenderNilAEAD(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP returned error: %v", err)
	}
	defer udpConn.Close()

	opus := make(chan []byte, 1)
	opus <- []byte{0x01, 0x02}
	close(opus)

	v := &VoiceConnection{LogLevel: -1}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("opusSender panicked with nil aead: %v", r)
		}
	}()

	v.opusSender(udpConn, make(chan struct{}), opus, 48000, 960)
}

func TestOpusSenderOnlyClearsCurrentGenerationReady(t *testing.T) {
	oldUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer oldUDP.Close()
	replacementUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer replacementUDP.Close()

	oldClose := make(chan struct{})
	close(oldClose)
	replacementClose := make(chan struct{})
	voice := &VoiceConnection{
		LogLevel: -1,
		Ready:    true,
		udpConn:  replacementUDP,
		close:    replacementClose,
	}
	voice.opusSender(oldUDP, oldClose, make(chan []byte), 48000, 960)
	voice.RLock()
	ready := voice.Ready
	voice.RUnlock()
	if !ready {
		t.Fatal("stale opus sender cleared the replacement transport's Ready state")
	}

	currentClose := make(chan struct{})
	close(currentClose)
	voice.Lock()
	voice.Ready = true
	voice.udpConn = oldUDP
	voice.close = currentClose
	voice.Unlock()
	voice.opusSender(oldUDP, currentClose, make(chan []byte), 48000, 960)
	voice.RLock()
	ready = voice.Ready
	voice.RUnlock()
	if ready {
		t.Fatal("current opus sender did not clear its transport's Ready state")
	}
}

func TestOpusSenderWriteErrorStartsRecovery(t *testing.T) {
	udpConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9})
	if err != nil {
		t.Fatalf("net.DialUDP() error = %v", err)
	}
	if err = udpConn.Close(); err != nil {
		t.Fatalf("udpConn.Close() error = %v", err)
	}

	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
	if err != nil {
		t.Fatalf("newVoiceAEAD() error = %v", err)
	}
	closeChan := make(chan struct{})
	voice := &VoiceConnection{
		LogLevel: -1,
		Ready:    true,
		speaking: true,
		aead:     aead,
		udpConn:  udpConn,
		close:    closeChan,
		op2:      voiceOP2{SSRC: 42},
	}
	defer voice.Close()
	opus := make(chan []byte, 1)
	opus <- []byte{0x01, 0x02, 0x03}
	done := make(chan struct{})
	go func() {
		voice.opusSender(udpConn, closeChan, opus, 48000, 960)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opusSender did not return after UDP write error")
	}
	deadline := time.Now().Add(time.Second)
	for {
		voice.RLock()
		current := voice.udpConn
		voice.RUnlock()
		if current == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("UDP write error did not start voice recovery")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUDPKeepAliveWriteErrorStartsRecovery(t *testing.T) {
	udpConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9})
	if err != nil {
		t.Fatalf("net.DialUDP() error = %v", err)
	}
	if err = udpConn.Close(); err != nil {
		t.Fatalf("udpConn.Close() error = %v", err)
	}

	closeChan := make(chan struct{})
	voice := &VoiceConnection{
		LogLevel: -1,
		Ready:    true,
		udpConn:  udpConn,
		close:    closeChan,
	}
	defer voice.Close()
	done := make(chan struct{})
	go func() {
		voice.udpKeepAlive(udpConn, closeChan, time.Hour)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("udpKeepAlive did not return after UDP write error")
	}
	deadline := time.Now().Add(time.Second)
	for {
		voice.RLock()
		current := voice.udpConn
		voice.RUnlock()
		if current == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("UDP keepalive write error did not start voice recovery")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUDPKeepAliveWriteErrorSkipsReplacement(t *testing.T) {
	staleUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer staleUDP.Close()
	replacementUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer replacementUDP.Close()

	staleClose := make(chan struct{})
	voice := &VoiceConnection{
		LogLevel: -1,
		Ready:    true,
		udpConn:  staleUDP,
		close:    staleClose,
	}
	voice.lifecycleMu.Lock()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		voice.reconnectUDPIfCurrent(staleUDP, staleClose)
		close(done)
	}()
	<-started

	replacementClose := make(chan struct{})
	voice.Lock()
	voice.udpConn = replacementUDP
	voice.close = replacementClose
	voice.Unlock()
	voice.lifecycleMu.Unlock()
	<-done

	voice.RLock()
	current := voice.udpConn
	ready := voice.Ready
	voice.RUnlock()
	if current != replacementUDP || !ready {
		t.Fatal("stale UDP keepalive write error disrupted the replacement transport")
	}
}

func TestUDPKeepAliveWriteErrorInterruptsReconnect(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}

	closeChan := make(chan struct{})
	voice := &VoiceConnection{
		LogLevel:     -1,
		Ready:        true,
		joinVersion:  1,
		reconnecting: true,
		udpConn:      udpConn,
		close:        closeChan,
	}
	voice.reconnectUDPIfCurrent(udpConn, closeChan)

	voice.RLock()
	current := voice.udpConn
	currentClose := voice.close
	ready := voice.Ready
	reconnecting := voice.reconnecting
	reconnectAgain := voice.reconnectAgain
	voice.RUnlock()
	if current != nil || currentClose != nil || ready || !reconnecting || !reconnectAgain {
		t.Fatal("UDP failure did not interrupt the in-flight resumable reconnect")
	}
	if _, again := voice.continueReconnect(); !again {
		t.Fatal("in-flight reconnect did not consume the queued UDP recovery")
	}
	if _, again := voice.continueReconnect(); again {
		t.Fatal("queued UDP recovery was consumed more than once")
	}
}

func TestOpusReceiverCopiesPacketType(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		receiver.Close()
		t.Fatalf("net.DialUDP() error = %v", err)
	}
	defer sender.Close()

	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
	if err != nil {
		receiver.Close()
		t.Fatalf("newVoiceAEAD() error = %v", err)
	}
	voice := &VoiceConnection{LogLevel: -1, aead: aead}
	closeChan := make(chan struct{})
	packets := make(chan *Packet, 2)
	done := make(chan struct{})
	go func() {
		voice.opusReceiver(receiver, closeChan, packets)
		close(done)
	}()
	t.Cleanup(func() {
		close(closeChan)
		receiver.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("opusReceiver did not stop")
		}
	})

	sendPacket := func(payloadType byte, sequence uint16) {
		t.Helper()
		header := make([]byte, 12)
		header[0] = 0x80
		header[1] = payloadType
		binary.BigEndian.PutUint16(header[2:4], sequence)
		binary.BigEndian.PutUint32(header[8:12], 42)
		nonce := make([]byte, aead.NonceSize())
		setVoiceNonce(nonce, uint32(sequence))
		ciphertext := aead.Seal(nil, nonce, []byte{0xf8, 0xff, 0xfe}, header)
		packet := append(append(header, ciphertext...), nonce[:4]...)
		if _, err := sender.Write(packet); err != nil {
			t.Fatalf("sender.Write() error = %v", err)
		}
	}
	receivePacket := func() *Packet {
		t.Helper()
		select {
		case packet := <-packets:
			return packet
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for voice packet")
			return nil
		}
	}

	sendPacket(0x78, 1)
	first := receivePacket()
	sendPacket(0x79, 2)
	second := receivePacket()

	if !bytes.Equal(first.Type, []byte{0x80, 0x78}) {
		t.Fatalf("first packet Type = %x after second read, want 8078", first.Type)
	}
	if !bytes.Equal(second.Type, []byte{0x80, 0x79}) {
		t.Fatalf("second packet Type = %x, want 8079", second.Type)
	}
}

func TestInterpolateVoiceTimestamp(t *testing.T) {
	const (
		frameDuration = 20 * time.Millisecond
		frameSize     = 960
	)
	tests := []struct {
		name      string
		timestamp uint32
		elapsed   time.Duration
		want      uint32
	}{
		{name: "normal frame", timestamp: 960, elapsed: frameDuration, want: 960},
		{name: "sub-frame jitter", timestamp: 960, elapsed: 39 * time.Millisecond, want: 960},
		{name: "one missed frame", timestamp: 960, elapsed: 2 * frameDuration, want: 1920},
		{name: "four missed frames", timestamp: 960, elapsed: 5 * frameDuration, want: 4800},
		{name: "one second gap", timestamp: 960, elapsed: time.Second, want: 48000},
		{name: "wraps", timestamp: 4294967195, elapsed: 3 * frameDuration, want: 1819},
		{name: "invalid frame duration", timestamp: 960, elapsed: time.Second, want: 960},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			duration := frameDuration
			if test.name == "invalid frame duration" {
				duration = 0
			}
			got := interpolateVoiceTimestamp(
				test.timestamp,
				test.elapsed,
				duration,
				frameSize,
			)
			if got != test.want {
				t.Fatalf("interpolateVoiceTimestamp(%d, %s) = %d, want %d", test.timestamp, test.elapsed, got, test.want)
			}
		})
	}
}

func TestOpusSenderSendsTrailingSilence(t *testing.T) {
	wsConn, peer := newGatewayTestConnection(t)
	closeChan := make(chan struct{})
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer server.Close()

	client, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("net.DialUDP() error = %v", err)
	}
	defer client.Close()

	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
	if err != nil {
		t.Fatalf("newVoiceAEAD() error = %v", err)
	}
	opus := make(chan []byte, 1)
	opus <- []byte{0x01, 0x02, 0x03}

	voice := &VoiceConnection{
		LogLevel: -1,
		wsConn:   wsConn,
		udpConn:  client,
		close:    closeChan,
		aead:     aead,
		op2:      voiceOP2{SSRC: 42},
	}
	done := make(chan struct{})
	defer func() {
		closeChannel(closeChan)
		<-done
	}()
	go func() {
		voice.opusSender(client, closeChan, opus, 48000, 960)
		close(done)
	}()

	wantPayloads := [][]byte{
		{0x01, 0x02, 0x03},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0x04, 0x05, 0x06},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
		{0xf8, 0xff, 0xfe},
	}
	packet := make([]byte, 2048)
	var previousTimestamp uint32
	for i, wantPayload := range wantPayloads {
		if i == 6 {
			time.Sleep(80 * time.Millisecond)
			opus <- []byte{0x04, 0x05, 0x06}
			close(opus)
		}
		if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("SetReadDeadline() error = %v", err)
		}
		n, _, err := server.ReadFromUDP(packet)
		if err != nil {
			t.Fatalf("ReadFromUDP() packet %d error = %v", i, err)
		}
		wirePacket := packet[:n]
		if sequence := binary.BigEndian.Uint16(wirePacket[2:4]); sequence != uint16(i) {
			t.Fatalf("packet %d sequence = %d, want %d", i, sequence, i)
		}
		timestamp := binary.BigEndian.Uint32(wirePacket[4:8])
		if i > 0 {
			delta := timestamp - previousTimestamp
			if delta < 960 || delta%960 != 0 {
				t.Fatalf("packet %d timestamp delta = %d, want a positive frame multiple", i, delta)
			}
			if i == 6 && delta < 4*960 {
				t.Fatalf("packet %d timestamp delta = %d, want interpolation across the source gap", i, delta)
			}
		}
		previousTimestamp = timestamp

		nonce := make([]byte, aead.NonceSize())
		copy(nonce[:4], wirePacket[n-4:])
		payload, err := aead.Open(nil, nonce, wirePacket[12:n-4], wirePacket[:12])
		if err != nil {
			t.Fatalf("decrypt packet %d error = %v", i, err)
		}
		if !bytes.Equal(payload, wantPayload) {
			t.Fatalf("packet %d payload = %x, want %x", i, payload, wantPayload)
		}
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opusSender did not stop after trailing silence")
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, flags := range []VoiceSpeakingFlags{VoiceSpeakingFlagMicrophone, 0, VoiceSpeakingFlagMicrophone, 0} {
		var notification struct {
			Op   int                 `json:"op"`
			Data VoiceSpeakingUpdate `json:"d"`
		}
		if err := peer.ReadJSON(&notification); err != nil {
			t.Fatal(err)
		}
		if notification.Op != 5 || notification.Data.SpeakingFlags != flags || notification.Data.SSRC != 42 {
			t.Fatalf("speaking notification = %+v, want flags=%d, ssrc=42", notification, flags)
		}
	}
}

func TestSelectVoiceEncryptionMode(t *testing.T) {
	tests := []struct {
		name     string
		modes    []string
		expected string
		wantErr  bool
	}{
		{
			name: "aes preferred over xchacha",
			modes: []string{
				voiceModeXChaCha20Poly1305RTPSize,
				voiceModeAES256GCMRTPSize,
			},
			expected: voiceModeAES256GCMRTPSize,
		},
		{
			name:     "xchacha fallback",
			modes:    []string{voiceModeXChaCha20Poly1305RTPSize},
			expected: voiceModeXChaCha20Poly1305RTPSize,
		},
		{
			name:    "unsupported modes",
			modes:   []string{"xsalsa20_poly1305"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectVoiceEncryptionMode(test.modes)
			if test.wantErr {
				if err == nil {
					t.Fatalf("selectVoiceEncryptionMode(%v) = %q, want error", test.modes, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectVoiceEncryptionMode(%v) error = %v", test.modes, err)
			}
			if got != test.expected {
				t.Fatalf("selectVoiceEncryptionMode(%v) = %q, want %q", test.modes, got, test.expected)
			}
		})
	}
}

func TestVoiceReadyRejectsUnsupportedEncryptionModes(t *testing.T) {
	key := make([]int, 32)
	aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, key)
	if err != nil {
		t.Fatalf("newVoiceAEAD() error = %v", err)
	}
	vc := &VoiceConnection{
		LogLevel:       -1,
		aead:           aead,
		encryptionMode: voiceModeAES256GCMRTPSize,
	}
	defer vc.Close()

	vc.onEvent([]byte(`{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":1,"modes":["xsalsa20_poly1305"]}}`))

	vc.RLock()
	defer vc.RUnlock()
	if vc.encryptionMode != "" {
		t.Fatalf("encryptionMode = %q, want empty", vc.encryptionMode)
	}
	if vc.aead != nil {
		t.Fatal("unsupported Ready payload retained an AEAD")
	}
}

func TestVoiceSessionDescription(t *testing.T) {
	validKey := make([]int, 32)
	for i := range validKey {
		validKey[i] = i
	}
	invalidByteKey := append([]int(nil), validKey...)
	invalidByteKey[7] = 256

	tests := []struct {
		name          string
		selectedMode  string
		op4Mode       string
		key           []int
		expectedNonce int
	}{
		{
			name:          "matching aes mode",
			selectedMode:  voiceModeAES256GCMRTPSize,
			op4Mode:       voiceModeAES256GCMRTPSize,
			key:           validKey,
			expectedNonce: 12,
		},
		{
			name:          "matching xchacha mode",
			selectedMode:  voiceModeXChaCha20Poly1305RTPSize,
			op4Mode:       voiceModeXChaCha20Poly1305RTPSize,
			key:           validKey,
			expectedNonce: 24,
		},
		{
			name:         "mismatched mode",
			selectedMode: voiceModeAES256GCMRTPSize,
			op4Mode:      voiceModeXChaCha20Poly1305RTPSize,
			key:          validKey,
		},
		{
			name:         "short key",
			selectedMode: voiceModeAES256GCMRTPSize,
			op4Mode:      voiceModeAES256GCMRTPSize,
			key:          []int{1, 2, 3},
		},
		{
			name:         "invalid key byte",
			selectedMode: voiceModeAES256GCMRTPSize,
			op4Mode:      voiceModeAES256GCMRTPSize,
			key:          invalidByteKey,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vc := &VoiceConnection{LogLevel: -1, encryptionMode: test.selectedMode}
			vc.onEvent(voiceSessionDescriptionMessage(t, test.op4Mode, test.key))

			vc.RLock()
			defer vc.RUnlock()
			if test.expectedNonce == 0 {
				if vc.aead != nil {
					t.Fatal("invalid session description installed an AEAD")
				}
				if vc.op4.Mode != "" {
					t.Fatalf("invalid session description stored mode %q", vc.op4.Mode)
				}
				return
			}
			if vc.aead == nil {
				t.Fatal("valid session description did not install an AEAD")
			}
			if got := vc.aead.NonceSize(); got != test.expectedNonce {
				t.Fatalf("NonceSize() = %d, want %d", got, test.expectedNonce)
			}
			if vc.op4.Mode != test.op4Mode {
				t.Fatalf("stored mode = %q, want %q", vc.op4.Mode, test.op4Mode)
			}
		})
	}
}

func TestVoiceReadyRequiresValidSessionDescription(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}

	key := make([]int, 32)
	vc := &VoiceConnection{
		LogLevel:       -1,
		close:          make(chan struct{}),
		udpConn:        udpConn,
		deaf:           true,
		encryptionMode: voiceModeAES256GCMRTPSize,
	}
	defer vc.Close()

	vc.RLock()
	ready := vc.Ready
	vc.RUnlock()
	if ready {
		t.Fatal("voice connection was ready before Session Description")
	}

	vc.onEvent(voiceSessionDescriptionMessage(t, voiceModeAES256GCMRTPSize, key))

	vc.RLock()
	ready = vc.Ready
	aead := vc.aead
	vc.RUnlock()
	if !ready {
		t.Fatal("voice connection was not ready after valid Session Description")
	}
	if aead == nil {
		t.Fatal("valid Session Description did not install an AEAD")
	}

	vc.Close()
	vc.RLock()
	ready = vc.Ready
	vc.RUnlock()
	if ready {
		t.Fatal("voice connection remained ready after Close")
	}
}

func TestVoiceInvalidSessionDescriptionFailsClosed(t *testing.T) {
	key := make([]int, 32)
	tests := []struct {
		name   string
		mode   string
		secret []int
	}{
		{
			name:   "mismatched mode",
			mode:   voiceModeXChaCha20Poly1305RTPSize,
			secret: key,
		},
		{
			name:   "invalid key",
			mode:   voiceModeAES256GCMRTPSize,
			secret: []int{1, 2, 3},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			closeChan := make(chan struct{})
			opusSend := make(chan []byte, 1)
			opusSend <- []byte{0x01}
			vc := &VoiceConnection{
				LogLevel:       -1,
				Ready:          true,
				close:          closeChan,
				OpusSend:       opusSend,
				encryptionMode: voiceModeAES256GCMRTPSize,
			}

			vc.onEvent(voiceSessionDescriptionMessage(t, test.mode, test.secret))

			vc.RLock()
			ready := vc.Ready
			aead := vc.aead
			vc.RUnlock()
			if ready {
				t.Fatal("invalid Session Description left voice ready")
			}
			if aead != nil {
				t.Fatal("invalid Session Description retained an AEAD")
			}
			select {
			case <-closeChan:
			default:
				t.Fatal("invalid Session Description did not close the transport")
			}
			if len(opusSend) != 1 {
				t.Fatal("invalid Session Description consumed a queued Opus frame")
			}
		})
	}
}

func TestVoiceAEADVectors(t *testing.T) {
	key := make([]int, 32)
	for i := range key {
		key[i] = i
	}
	header := []byte{0x80, 0x78, 0x12, 0x34, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	opus := []byte{0xf8, 0xff, 0xfe, 0x01, 0x02, 0x03}
	const counter = uint32(0x01020304)

	tests := []struct {
		name        string
		mode        string
		expectedHex string
	}{
		{
			name:        "aes",
			mode:        voiceModeAES256GCMRTPSize,
			expectedHex: "d9aa235c15923492100db0e5ecb54fc68012861b231901020304",
		},
		{
			name:        "xchacha",
			mode:        voiceModeXChaCha20Poly1305RTPSize,
			expectedHex: "8e97ee4d9c69cb0a20bf07d083536831aa523a1138c201020304",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			aead, err := newVoiceAEAD(test.mode, key)
			if err != nil {
				t.Fatalf("newVoiceAEAD() error = %v", err)
			}
			nonce := make([]byte, aead.NonceSize())
			setVoiceNonce(nonce, counter)
			packet := encryptVoicePacket(aead, nonce, opus, header)
			if !bytes.Equal(packet[:len(header)], header) {
				t.Fatalf("RTP header = %x, want %x", packet[:len(header)], header)
			}
			payload := packet[len(header):]
			if !bytes.Equal(payload[len(payload)-4:], []byte{0x01, 0x02, 0x03, 0x04}) {
				t.Fatalf("nonce suffix = %x, want 01020304", payload[len(payload)-4:])
			}
			if got := hex.EncodeToString(payload); got != test.expectedHex {
				t.Fatalf("encrypted payload = %s, want %s", got, test.expectedHex)
			}

			counterBytes := payload[len(payload)-4:]
			decryptNonce := make([]byte, aead.NonceSize())
			copy(decryptNonce[:4], counterBytes)
			plain, err := aead.Open(nil, decryptNonce, payload[:len(payload)-4], header)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if !bytes.Equal(plain, opus) {
				t.Fatalf("decrypted payload = %x, want %x", plain, opus)
			}
		})
	}
}

func BenchmarkEncryptVoicePacket(b *testing.B) {
	for _, mode := range []string{voiceModeAES256GCMRTPSize, voiceModeXChaCha20Poly1305RTPSize} {
		b.Run(mode, func(b *testing.B) {
			aead, err := newVoiceAEAD(mode, make([]int, 32))
			if err != nil {
				b.Fatal(err)
			}
			header := []byte{0x80, 0x78, 0, 1, 0, 0, 3, 0, 0, 0, 0, 42}
			nonce := make([]byte, aead.NonceSize())
			setVoiceNonce(nonce, 7)
			opus := bytes.Repeat([]byte{0xf8, 0xff, 0xfe}, 54)

			b.ReportAllocs()
			for b.Loop() {
				voicePacketSink = encryptVoicePacket(aead, nonce, opus, header)
			}
		})
	}
}

var voicePacketSink []byte

func newVoiceUDPDiscoveryTestServer(t *testing.T, release <-chan struct{}) (*net.UDPAddr, <-chan struct{}, <-chan error) {
	t.Helper()

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	requestReceived := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		buf := make([]byte, 74)
		n, addr, err := server.ReadFromUDP(buf)
		if err != nil {
			result <- err
			return
		}
		if n != len(buf) {
			result <- fmt.Errorf("discovery request length %d", n)
			return
		}
		close(requestReceived)
		if release != nil {
			<-release
		}
		response := make([]byte, 74)
		copy(response[8:], []byte("203.0.113.10"))
		binary.BigEndian.PutUint16(response[72:], 4242)
		_, err = server.WriteToUDP(response, addr)
		result <- err
	}()

	return server.LocalAddr().(*net.UDPAddr), requestReceived, result
}

func newUDPOpenTestVoice(wsConn *websocket.Conn, addr *net.UDPAddr) *VoiceConnection {
	return &VoiceConnection{
		LogLevel:       -1,
		close:          make(chan struct{}),
		endpoint:       "voice.example.com",
		wsConn:         wsConn,
		encryptionMode: voiceModeAES256GCMRTPSize,
		op2: voiceOP2{
			IP:   addr.IP.String(),
			Port: addr.Port,
			SSRC: 1,
		},
	}
}

func TestUDPOpenDiscoveryDoesNotBlockClose(t *testing.T) {
	releaseDiscovery := make(chan struct{})
	addr, requestReceived, udpResult := newVoiceUDPDiscoveryTestServer(t, releaseDiscovery)
	wsConn, _ := newGatewayTestConnection(t)
	voice := newUDPOpenTestVoice(wsConn, addr)

	openDone := make(chan error, 1)
	go func() { openDone <- voice.udpOpen(voice.wsConn, voice.close) }()
	select {
	case <-requestReceived:
	case <-time.After(time.Second):
		t.Fatal("udpOpen did not send the discovery request")
	}
	closeDone := make(chan struct{})
	go func() {
		voice.Close()
		close(closeDone)
	}()
	closeReturned := false
	select {
	case <-closeDone:
		closeReturned = true
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseDiscovery)
	if err := <-udpResult; err != nil {
		t.Fatalf("UDP discovery error = %v", err)
	}
	select {
	case <-openDone:
	case <-time.After(time.Second):
		t.Fatal("udpOpen did not return after discovery was released")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after discovery was released")
	}
	if !closeReturned {
		t.Fatal("Close blocked behind UDP discovery")
	}
}

func TestUDPOpenSelectProtocolWriteDoesNotBlockClose(t *testing.T) {
	addr, _, udpResult := newVoiceUDPDiscoveryTestServer(t, nil)
	wsConn, blocking := newTextFrameBlockingWebsocket(t)
	voice := newUDPOpenTestVoice(wsConn, addr)

	openDone := make(chan error, 1)
	go func() { openDone <- voice.udpOpen(voice.wsConn, voice.close) }()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("udpOpen did not reach the Select Protocol write")
	}
	if err := <-udpResult; err != nil {
		t.Fatalf("UDP discovery error = %v", err)
	}
	closeDone := make(chan struct{})
	go func() {
		voice.Close()
		close(closeDone)
	}()
	closeReturned := false
	select {
	case <-closeDone:
		closeReturned = true
	case <-time.After(websocketWriteTimeout + 500*time.Millisecond):
	}

	blocking.releaseWrites()
	select {
	case <-openDone:
	case <-time.After(time.Second):
		t.Fatal("udpOpen did not return after the Select Protocol write was released")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the Select Protocol write was released")
	}
	if !closeReturned {
		t.Fatal("Close blocked behind the Select Protocol write")
	}
}

func TestUDPOpenSendsSelectedEncryptionMode(t *testing.T) {
	udpServer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer udpServer.Close()

	udpResult := make(chan error, 1)
	go func() {
		buf := make([]byte, 74)
		n, addr, readErr := udpServer.ReadFromUDP(buf)
		if readErr != nil {
			udpResult <- readErr
			return
		}
		if n != len(buf) {
			udpResult <- fmt.Errorf("discovery request length %d", n)
			return
		}
		response := make([]byte, 74)
		copy(response[8:], []byte("203.0.113.10"))
		binary.BigEndian.PutUint16(response[72:], 4242)
		_, writeErr := udpServer.WriteToUDP(response, addr)
		udpResult <- writeErr
	}()

	opResult := make(chan voiceUDPOp, 1)
	upgrader := websocket.Upgrader{}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			return
		}
		defer conn.Close()
		var op voiceUDPOp
		if readErr := conn.ReadJSON(&op); readErr == nil {
			opResult <- op
		}
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer wsConn.Close()

	closeChan := make(chan struct{})
	udpAddr := udpServer.LocalAddr().(*net.UDPAddr)
	vc := &VoiceConnection{
		LogLevel:       -1,
		close:          closeChan,
		endpoint:       "voice.example.com",
		wsConn:         wsConn,
		encryptionMode: voiceModeXChaCha20Poly1305RTPSize,
		op2: voiceOP2{
			IP:   udpAddr.IP.String(),
			Port: udpAddr.Port,
			SSRC: 1,
		},
	}
	defer func() {
		close(closeChan)
		vc.Lock()
		if vc.udpConn != nil {
			_ = vc.udpConn.Close()
		}
		vc.Unlock()
	}()

	if err = vc.udpOpen(vc.wsConn, vc.close); err != nil {
		t.Fatalf("udpOpen() error = %v", err)
	}
	if err = <-udpResult; err != nil {
		t.Fatalf("UDP discovery error = %v", err)
	}

	select {
	case op := <-opResult:
		if op.Op != 1 {
			t.Fatalf("OP = %d, want 1", op.Op)
		}
		if op.Data.Protocol != "udp" {
			t.Fatalf("protocol = %q, want udp", op.Data.Protocol)
		}
		if op.Data.Data.Mode != voiceModeXChaCha20Poly1305RTPSize {
			t.Fatalf("mode = %q, want %q", op.Data.Data.Mode, voiceModeXChaCha20Poly1305RTPSize)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive Select Protocol payload")
	}
}

func TestVoiceSessionDescriptionConcurrent(t *testing.T) {
	key := make([]int, 32)
	message := voiceSessionDescriptionMessage(t, voiceModeAES256GCMRTPSize, key)
	vc := &VoiceConnection{LogLevel: -1, encryptionMode: voiceModeAES256GCMRTPSize}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			vc.onEvent(message)
		}()
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				vc.RLock()
				if vc.aead != nil {
					_ = vc.aead.NonceSize()
				}
				vc.RUnlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	vc.RLock()
	defer vc.RUnlock()
	if vc.aead == nil {
		t.Fatal("valid concurrent session descriptions did not install an AEAD")
	}
}

func voiceSessionDescriptionMessage(t *testing.T, mode string, key []int) []byte {
	t.Helper()
	payload := struct {
		Op   int      `json:"op"`
		Data voiceOP4 `json:"d"`
	}{
		Op:   4,
		Data: voiceOP4{Mode: mode, SecretKey: key},
	}
	message, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return message
}

func TestVoiceClose4022DoesNotReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade returned error: %v", err)
			return
		}
		defer conn.Close()

		err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, ""))
		if err != nil {
			t.Errorf("WriteMessage returned error: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	transport := &activatedWriteBlockingConn{closed: make(chan struct{})}
	dialer := &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			transport.Conn = conn
			return transport, nil
		},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	s := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	v := &VoiceConnection{
		GuildID:  "guild",
		LogLevel: -1,
		close:    make(chan struct{}),
		session:  s,
		wsConn:   conn,
	}
	s.VoiceConnections[v.GuildID] = v

	v.wsListen(conn, v.close)

	if v.wsConn != nil {
		t.Fatal("wsConn is not nil")
	}
	if _, ok := s.VoiceConnections[v.GuildID]; ok {
		t.Fatal("voice connection was not removed")
	}
	if v.reconnecting {
		t.Fatal("voice connection started reconnecting")
	}
	select {
	case <-transport.closed:
	default:
		t.Fatal("voice listener left the terminal websocket transport open")
	}
}

func TestVoiceClose4022DoesNotRemoveReplacement(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade returned error: %v", err)
			return
		}
		defer conn.Close()

		if err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
			t.Errorf("WriteMessage returned error: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	session := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	stale := &VoiceConnection{
		GuildID:  "guild",
		LogLevel: -1,
		close:    make(chan struct{}),
		session:  session,
		wsConn:   conn,
	}
	replacement := &VoiceConnection{GuildID: stale.GuildID}
	session.VoiceConnections[stale.GuildID] = replacement

	stale.wsListen(conn, stale.close)

	if got := session.VoiceConnections[stale.GuildID]; got != replacement {
		t.Fatalf("VoiceConnections[%q] = %p, want replacement %p", stale.GuildID, got, replacement)
	}
}

func TestUDPOpenReadTimeout(t *testing.T) {
	oldTimeout := voiceUDPReadTimeout
	voiceUDPReadTimeout = 25 * time.Millisecond
	defer func() {
		voiceUDPReadTimeout = oldTimeout
	}()

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("error listening on udp: %v", err)
	}
	defer server.Close()

	addr := server.LocalAddr().(*net.UDPAddr)
	v := &VoiceConnection{
		wsConn:         &websocket.Conn{},
		close:          make(chan struct{}),
		endpoint:       "127.0.0.1",
		encryptionMode: voiceModeAES256GCMRTPSize,
		op2: voiceOP2{
			IP:   "127.0.0.1",
			Port: addr.Port,
			SSRC: 1,
		},
	}
	defer func() {
		if v.udpConn != nil {
			v.udpConn.Close()
		}
	}()

	start := time.Now()
	err = v.udpOpen(v.wsConn, v.close)
	if err == nil {
		t.Fatal("expected udpOpen to time out waiting for discovery response")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("udpOpen took too long to return: %s", elapsed)
	}
	if v.udpConn != nil {
		t.Fatal("udpOpen retained its UDP connection after discovery failed")
	}

	lockAcquired := make(chan struct{})
	go func() {
		v.RLock()
		v.RUnlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("udpOpen returned without releasing the voice lock")
	}
}

func TestVoiceCloseKeepsOpusChannels(t *testing.T) {
	// Close runs on every internal reconnect (voice server updates,
	// websocket errors, 4014 channel moves); user pipelines hold these
	// channels and must survive it.
	opusSend := make(chan []byte)
	opusRecv := make(chan *Packet)
	voice := &VoiceConnection{
		OpusSend: opusSend,
		OpusRecv: opusRecv,
	}

	voice.Close()

	if voice.OpusSend != opusSend {
		t.Fatal("OpusSend was replaced by Close")
	}
	if voice.OpusRecv != opusRecv {
		t.Fatal("OpusRecv was replaced by Close")
	}
}

func TestVoiceDisconnectClearsOpusChannels(t *testing.T) {
	session := &Session{VoiceConnections: map[string]*VoiceConnection{}}
	voice := &VoiceConnection{
		session:  session,
		GuildID:  "guild",
		OpusSend: make(chan []byte),
		OpusRecv: make(chan *Packet),
	}
	session.VoiceConnections["guild"] = voice

	close(voice.OpusSend)
	close(voice.OpusRecv)

	if err := voice.Disconnect(); err != nil {
		t.Fatalf("Disconnect returned error: %v", err)
	}

	if voice.OpusSend != nil {
		t.Fatal("OpusSend was not cleared")
	}
	if voice.OpusRecv != nil {
		t.Fatal("OpusRecv was not cleared")
	}
	if _, ok := session.VoiceConnections["guild"]; ok {
		t.Fatal("voice connection was not removed from session")
	}
}

func TestVoiceCloseWithLockedWSMutex(t *testing.T) {
	server := newCloseFrameTestServer(t)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	voice := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		wsConn:   conn,
	}

	voice.wsMutex.Lock()
	done := make(chan error, 1)
	go func() {
		voice.Close()
		done <- nil
	}()

	requireCloseDoneWhileWSMutexLocked(t, func() {
		voice.wsMutex.Unlock()
	}, done)
}

func TestVoiceCloseDoesNotSleepAfterCloseFrame(t *testing.T) {
	closeCodes := make(chan int, 1)
	server := newCloseCodeTestServer(t, closeCodes)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	voice := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		wsConn:   conn,
	}

	start := time.Now()
	voice.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close() took %v, want under 500ms", elapsed)
	}

	select {
	case code := <-closeCodes:
		if code != websocket.CloseNormalClosure {
			t.Fatalf("close code = %d, want %d", code, websocket.CloseNormalClosure)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive websocket close frame")
	}
}

func BenchmarkVoiceClose(b *testing.B) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			b.Fatalf("Dial() error = %v", err)
		}
		voice := &VoiceConnection{
			LogLevel: -1,
			close:    make(chan struct{}),
			wsConn:   conn,
		}
		b.StartTimer()

		voice.Close()
	}
}

func TestVoiceReconnectSnapshotsJoinState(t *testing.T) {
	joins := make(chan voiceChannelJoinData, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var op voiceChannelJoinOp
		if err = conn.ReadJSON(&op); err == nil {
			joins <- op.Data
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	session := &Session{
		DataReady:        true,
		LogLevel:         -1,
		VoiceConnections: make(map[string]*VoiceConnection),
		wsConn:           conn,
	}
	voice := &VoiceConnection{
		GuildID:   "guild",
		ChannelID: "channel-one",
		LogLevel:  -1,
		deaf:      true,
		session:   session,
	}
	session.VoiceConnections[voice.GuildID] = voice

	stopMutating := make(chan struct{})
	mutatorDone := make(chan struct{})
	go func() {
		defer close(mutatorDone)
		second := false
		for {
			select {
			case <-stopMutating:
				return
			default:
			}
			voice.Lock()
			voice.session = session
			voice.GuildID = "guild"
			if second {
				voice.ChannelID = "channel-two"
				voice.mute = true
				voice.deaf = false
			} else {
				voice.ChannelID = "channel-one"
				voice.mute = false
				voice.deaf = true
			}
			voice.Ready = true
			voice.Unlock()
			second = !second
		}
	}()
	reconnectDone := make(chan struct{})
	go func() {
		voice.reconnectWithResume(false, nil, nil)
		close(reconnectDone)
	}()
	defer func() {
		closeChannel(stopMutating)
		<-mutatorDone
		_ = voice.Disconnect()
		<-reconnectDone
	}()

	var join voiceChannelJoinData
	select {
	case join = <-joins:
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not send a voice join")
	}
	if join.ChannelID == nil {
		t.Fatal("reconnect sent an empty channel ID")
	}
	coherentFirst := *join.ChannelID == "channel-one" && !join.SelfMute && join.SelfDeaf
	coherentSecond := *join.ChannelID == "channel-two" && join.SelfMute && !join.SelfDeaf
	if !coherentFirst && !coherentSecond {
		t.Fatalf("reconnect sent mixed join state: %#v", join)
	}
	// The snapshot has reached the wire. Finish readiness on that channel
	// instead of continuing to move the fixture between unrelated channels.
	closeChannel(stopMutating)
	<-mutatorDone
	voice.Lock()
	voice.ChannelID = *join.ChannelID
	voice.Ready = true
	voice.Unlock()
	select {
	case <-reconnectDone:
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not return after joining")
	}
}

func TestVoiceReconnectStopsWhenUnregistered(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		ChannelID: "channel",
		LogLevel:  -1,
		session: &Session{
			VoiceConnections: make(map[string]*VoiceConnection),
		},
	}

	done := make(chan struct{})
	go func() {
		v.reconnectWithResume(true, nil, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not stop for an unregistered voice connection")
	}

	v.RLock()
	reconnecting := v.reconnecting
	v.RUnlock()
	if reconnecting {
		t.Fatal("reconnecting was not cleared")
	}
}

func TestVoiceReconnectResetSkipsReplacement(t *testing.T) {
	session := &Session{VoiceConnections: make(map[string]*VoiceConnection)}
	stale := &VoiceConnection{
		GuildID: "guild",
		session: session,
	}
	session.VoiceConnections[stale.GuildID] = stale

	session.voiceMutex.Lock()
	type resetResult struct {
		sent bool
		err  error
	}
	done := make(chan resetResult, 1)
	go func() {
		sent, err := stale.sendDisconnectIfCurrent(0)
		done <- resetResult{sent: sent, err: err}
	}()
	time.Sleep(50 * time.Millisecond)

	replacement := &VoiceConnection{GuildID: stale.GuildID, session: session}
	session.Lock()
	session.VoiceConnections[stale.GuildID] = replacement
	session.Unlock()
	session.voiceMutex.Unlock()

	result := <-done
	if result.sent {
		t.Fatal("stale reconnect sent a disconnect packet after replacement")
	}
	if result.err != nil {
		t.Fatalf("sendDisconnectIfCurrent() error = %v", result.err)
	}

	session.RLock()
	registered := session.VoiceConnections[stale.GuildID]
	session.RUnlock()
	if registered != replacement {
		t.Fatal("stale reconnect removed the replacement voice connection")
	}
}

func TestVoiceChangeChannelNilSessionWsConn(t *testing.T) {
	v := &VoiceConnection{
		GuildID: "guild",
		session: &Session{},
	}
	v.session.VoiceConnections = map[string]*VoiceConnection{v.GuildID: v}

	err := v.ChangeChannel("channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestVoiceChangeChannelNilSession(t *testing.T) {
	v := &VoiceConnection{GuildID: "guild"}

	err := v.ChangeChannel("channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestVoiceDisconnectNilSessionWsConn(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
		session:   &Session{},
	}
	v.session.VoiceConnections = map[string]*VoiceConnection{v.GuildID: v}

	err := v.Disconnect()
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("Disconnect() error = %v, want %v", err, ErrWSNotFound)
	}
	if v.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", v.sessionID)
	}
}

func TestVoiceDisconnectNilSession(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
	}

	err := v.Disconnect()
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("Disconnect() error = %v, want %v", err, ErrWSNotFound)
	}
	if v.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", v.sessionID)
	}
}

func TestVoiceDisconnectDoesNotAcquireGatewayWriteLockBeforeTeardown(t *testing.T) {
	conn, _ := newGatewayTestConnection(t)
	session := &Session{
		wsConn:           conn,
		VoiceConnections: make(map[string]*VoiceConnection),
	}
	voice := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
		session:   session,
	}
	session.VoiceConnections[voice.GuildID] = voice

	session.wsMutex.Lock()
	wsMutexLocked := true
	defer func() {
		if wsMutexLocked {
			session.wsMutex.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- voice.Disconnect()
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		voice.RLock()
		disconnecting := voice.disconnecting
		voice.RUnlock()
		if disconnecting {
			break
		}

		select {
		case err := <-done:
			session.wsMutex.Unlock()
			wsMutexLocked = false
			t.Fatalf("Disconnect returned before the gateway write lock was available: %v", err)
		case <-timer.C:
			session.wsMutex.Unlock()
			wsMutexLocked = false
			t.Fatal("Disconnect waited for the gateway write lock before tearing down local state")
		case <-time.After(time.Millisecond):
		}
	}

	session.RLock()
	registered := session.VoiceConnections[voice.GuildID]
	session.RUnlock()
	if registered != nil {
		t.Fatal("Disconnect did not unregister before the gateway write")
	}

	session.wsMutex.Unlock()
	wsMutexLocked = false
	if err := <-done; err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
}

func TestRedactedVoiceData(t *testing.T) {
	data := []byte(`{"op":4,"d":{"token":"voice-token","secret_key":[1,2,3],"nested":{"access_token":"access-token","refresh_token":"refresh-token","safe":"ok"}}}`)

	got := redactedVoiceData(data)
	for _, secret := range []string{"voice-token", "access-token", "refresh-token", "1,2,3"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedVoiceData() leaked %q in %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactedVoiceData() = %s, want redacted value", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redactedVoiceData() = %s, want non-secret fields preserved", got)
	}
}

func TestRedactedVoiceDataInvalidJSON(t *testing.T) {
	data := []byte("not-json")

	if got := redactedVoiceData(data); got != string(data) {
		t.Fatalf("redactedVoiceData() = %q, want %q", got, string(data))
	}
}

func TestVoiceSpeakingPayloadCurrentFlags(t *testing.T) {
	messages := make(chan []byte, 2)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			messages <- message
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	vc := &VoiceConnection{wsConn: conn, op2: voiceOP2{SSRC: 42}}
	flags := VoiceSpeakingFlagMicrophone | VoiceSpeakingFlagPriority
	if err := vc.SpeakingFlags(flags); err != nil {
		t.Fatalf("SpeakingFlags returned error: %v", err)
	}
	vc.RLock()
	if !vc.speaking {
		vc.RUnlock()
		t.Fatal("speaking = false after non-zero flags")
	}
	vc.RUnlock()

	if err := vc.Speaking(false); err != nil {
		t.Fatalf("Speaking returned error: %v", err)
	}
	vc.RLock()
	if vc.speaking {
		vc.RUnlock()
		t.Fatal("speaking = true after Speaking(false)")
	}
	vc.RUnlock()

	for _, want := range []VoiceSpeakingFlags{flags, 0} {
		select {
		case message := <-messages:
			var payload struct {
				Op   int `json:"op"`
				Data struct {
					Speaking VoiceSpeakingFlags `json:"speaking"`
					Delay    int                `json:"delay"`
					SSRC     uint32             `json:"ssrc"`
				} `json:"d"`
			}
			if err := json.Unmarshal(message, &payload); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if payload.Op != 5 || payload.Data.Speaking != want || payload.Data.Delay != 0 || payload.Data.SSRC != 42 {
				t.Fatalf("payload = %#v, want speaking=%d delay=0 ssrc=42", payload, want)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for speaking payload")
		}
	}
}

func TestVoiceSpeakingStaleWriteDoesNotChangeReplacementState(t *testing.T) {
	tests := []struct {
		name                string
		failWrite           bool
		replacementSpeaking bool
	}{
		{name: "successful old write", replacementSpeaking: false},
		{name: "failed old write", failWrite: true, replacementSpeaking: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockWrites := make(chan struct{})
			releaseWrite := make(chan struct{})
			server := newVoiceOpenTestServer(t, func() { close(blockWrites) })
			transport := &activatedWriteBlockingConn{
				active:  blockWrites,
				entered: make(chan struct{}),
				release: releaseWrite,
				closed:  make(chan struct{}),
			}
			dialer := &websocket.Dialer{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				NetDial: func(network, addr string) (net.Conn, error) {
					conn, err := net.Dial(network, addr)
					if err != nil {
						return nil, err
					}
					transport.Conn = conn
					return transport, nil
				},
			}
			conn, _, err := dialer.Dial("wss://"+strings.TrimPrefix(server.URL, "https://"), nil)
			if err != nil {
				t.Fatalf("Dial() error = %v", err)
			}
			defer conn.Close()

			voice := &VoiceConnection{LogLevel: -1, wsConn: conn, op2: voiceOP2{SSRC: 42}}
			done := make(chan error, 1)
			go func() { done <- voice.SpeakingFlags(VoiceSpeakingFlagMicrophone) }()
			select {
			case <-transport.entered:
			case <-time.After(time.Second):
				t.Fatal("SpeakingFlags did not reach the websocket write")
			}

			voice.Lock()
			voice.wsConn = &websocket.Conn{}
			voice.speaking = tt.replacementSpeaking
			voice.Unlock()
			if tt.failWrite {
				_ = conn.Close()
			} else {
				close(releaseWrite)
			}

			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("SpeakingFlags did not return")
			}
			voice.RLock()
			gotSpeaking := voice.speaking
			voice.RUnlock()
			if gotSpeaking != tt.replacementSpeaking {
				t.Fatalf("replacement speaking = %t, want %t", gotSpeaking, tt.replacementSpeaking)
			}
			if !errors.Is(err, ErrWSNotFound) {
				t.Fatalf("SpeakingFlags() error = %v, want %v", err, ErrWSNotFound)
			}
		})
	}
}

func TestVoiceSpeakingUpdateCurrentAndLegacyPayloads(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		wantFlags    VoiceSpeakingFlags
		wantSpeaking bool
	}{
		{name: "current bitmask", payload: `{"user_id":"user","ssrc":42,"speaking":5}`, wantFlags: VoiceSpeakingFlagMicrophone | VoiceSpeakingFlagPriority, wantSpeaking: true},
		{name: "legacy true", payload: `{"user_id":"user","ssrc":42,"speaking":true}`, wantFlags: VoiceSpeakingFlagMicrophone, wantSpeaking: true},
		{name: "legacy false", payload: `{"user_id":"user","ssrc":42,"speaking":false}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var update VoiceSpeakingUpdate
			if err := json.Unmarshal([]byte(tt.payload), &update); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if update.UserID != "user" || update.SSRC != 42 || update.SpeakingFlags != tt.wantFlags || update.Speaking != tt.wantSpeaking {
				t.Fatalf("update = %#v", update)
			}
		})
	}

	var update VoiceSpeakingUpdate
	if err := json.Unmarshal([]byte(`{"speaking":"invalid"}`), &update); err == nil {
		t.Fatal("json.Unmarshal returned nil error for invalid speaking flags")
	}
}

func TestVoiceSpeakingConcurrentClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	vc := &VoiceConnection{wsConn: conn}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = vc.Speaking(true)
			}
		}
	}()

	time.Sleep(time.Millisecond)
	vc.Close()
	close(stop)
	<-done
}

func TestVoiceSpeakingHandlerPanicDoesNotAbortDispatch(t *testing.T) {
	vc := &VoiceConnection{LogLevel: -1}
	vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {
		panic("boom")
	})

	called := make(chan struct{})
	vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {
		close(called)
	})

	vc.onEvent([]byte(`{"op":5,"d":{"user_id":"user","ssrc":1,"speaking":true}}`))

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("second speaking handler was not called")
	}
}

func TestVoiceSpeakingHandlersConcurrentDispatch(t *testing.T) {
	vc := &VoiceConnection{LogLevel: -1}
	vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {})

	payload := []byte(`{"op":5,"d":{"user_id":"user","ssrc":1,"speaking":true}}`)
	done := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					vc.onEvent(payload)
				}
			}
		}()
	}

	for i := 0; i < 1000; i++ {
		vc.AddHandler(func(*VoiceConnection, *VoiceSpeakingUpdate) {})
	}
	close(done)
	wg.Wait()
}

func TestVoiceHeartbeatIgnoresInvalidInterval(t *testing.T) {
	for name, interval := range map[string]time.Duration{
		"negative": -1,
		"overflow": maxGatewayHeartbeatIntervalMsec + 1,
		"zero":     0,
	} {
		t.Run(name, func(t *testing.T) {
			conn := &websocket.Conn{}
			closeChan := make(chan struct{})
			vc := &VoiceConnection{LogLevel: -1, wsConn: conn, close: closeChan}
			vc.wsHeartbeat(conn, closeChan, interval)
		})
	}
}

func TestVoiceHeartbeatAcceptsMaximumInterval(t *testing.T) {
	conn := &websocket.Conn{}
	closeChan := make(chan struct{})
	vc := &VoiceConnection{LogLevel: -1, wsConn: &websocket.Conn{}, close: closeChan}
	// A stale generation returns after safely constructing the maximum ticker,
	// before it can write through the deliberately inert connection.
	vc.wsHeartbeat(conn, closeChan, maxGatewayHeartbeatIntervalMsec)
}

func TestVoiceEventRejectsInvalidHeartbeatInterval(t *testing.T) {
	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	var logged string
	Logger = func(msgL, caller int, format string, a ...interface{}) {
		logged = fmt.Sprintf(format, a...)
	}

	vc := &VoiceConnection{LogLevel: LogError}
	vc.onEvent([]byte(fmt.Sprintf(`{"op":8,"d":{"heartbeat_interval":%d}}`, maxGatewayHeartbeatIntervalMsec+1)))
	if !strings.Contains(logged, "invalid voice heartbeat interval") {
		t.Fatalf("onEvent log = %q, want invalid heartbeat interval", logged)
	}
}

func newVoiceOpenTestServer(t *testing.T, beforeUpgradeResponse func()) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if beforeUpgradeResponse != nil {
			beforeUpgradeResponse()
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newVoiceOpenTestConnection(server *httptest.Server, dialer *websocket.Dialer) *VoiceConnection {
	return &VoiceConnection{
		LogLevel:  -1,
		GuildID:   "guild",
		UserID:    "user",
		ChannelID: "channel",
		sessionID: "session",
		token:     "token",
		endpoint:  strings.TrimPrefix(server.URL, "https://"),
		session:   &Session{Dialer: dialer},
	}
}

func testVoiceOpenClose(t *testing.T, voice *VoiceConnection, blocked, release chan struct{}, operation string) {
	t.Helper()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	openDone := make(chan error, 1)
	go func() { openDone <- voice.open() }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatalf("open did not reach the voice %s", operation)
	}
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- voice.open() }()
	select {
	case err := <-waiterDone:
		t.Fatalf("concurrent open returned before the first attempt completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	closeDone := make(chan struct{})
	go func() {
		voice.Close()
		close(closeDone)
	}()
	closeReturned := false
	select {
	case <-closeDone:
		closeReturned = true
	case <-time.After(200 * time.Millisecond):
	}
	waiterReturned := false
	var waiterErr error
	select {
	case waiterErr = <-waiterDone:
		waiterReturned = true
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	released = true
	var openErr error
	select {
	case openErr = <-openDone:
	case <-time.After(time.Second):
		t.Fatalf("open did not return after the voice %s was released", operation)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatalf("Close did not return after the voice %s was released", operation)
	}
	if !waiterReturned {
		select {
		case waiterErr = <-waiterDone:
		case <-time.After(time.Second):
			t.Fatal("concurrent open did not return after Close")
		}
	}

	if !closeReturned {
		t.Fatalf("Close blocked behind a stalled voice %s", operation)
	}
	if !waiterReturned {
		t.Fatal("concurrent open remained blocked after Close canceled the active attempt")
	}
	if !errors.Is(waiterErr, ErrWSNotFound) {
		t.Fatalf("concurrent open error = %v, want %v after Close", waiterErr, ErrWSNotFound)
	}
	if !errors.Is(openErr, ErrWSNotFound) {
		t.Fatalf("open error = %v, want %v after Close", openErr, ErrWSNotFound)
	}
	voice.RLock()
	defer voice.RUnlock()
	if voice.wsConn != nil || voice.close != nil {
		t.Fatal("the canceled voice open installed websocket state")
	}
}

func TestVoiceOpenDialDoesNotBlockClose(t *testing.T) {
	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	var dialStartedOnce sync.Once

	server := newVoiceOpenTestServer(t, nil)
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		NetDial: func(network, addr string) (net.Conn, error) {
			dialStartedOnce.Do(func() { close(dialStarted) })
			<-releaseDial
			return net.Dial(network, addr)
		},
	}
	voice := newVoiceOpenTestConnection(server, dialer)
	testVoiceOpenClose(t, voice, dialStarted, releaseDial, "dial")
}

func TestConcurrentVoiceOpenRetriesAfterFailedAttempt(t *testing.T) {
	firstDialStarted := make(chan struct{})
	releaseFirstDial := make(chan struct{})
	var dialMu sync.Mutex
	dialCount := 0

	server := newVoiceOpenTestServer(t, nil)
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		NetDial: func(network, addr string) (net.Conn, error) {
			dialMu.Lock()
			dialCount++
			call := dialCount
			dialMu.Unlock()
			if call == 1 {
				close(firstDialStarted)
				<-releaseFirstDial
				return nil, errors.New("first dial failed")
			}
			return net.Dial(network, addr)
		},
	}
	voice := newVoiceOpenTestConnection(server, dialer)
	firstDone := make(chan error, 1)
	go func() { firstDone <- voice.open() }()
	select {
	case <-firstDialStarted:
	case <-time.After(time.Second):
		t.Fatal("first open did not reach the voice dial")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- voice.open() }()
	select {
	case err := <-secondDone:
		t.Fatalf("second open returned before the first attempt completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirstDial)

	select {
	case err := <-firstDone:
		if err == nil {
			t.Fatal("first open unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("first open did not return after the dial failed")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second open error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second open did not retry after the first attempt failed")
	}
	defer voice.Close()

	dialMu.Lock()
	gotDialCount := dialCount
	dialMu.Unlock()
	if gotDialCount != 2 {
		t.Fatalf("voice dial count = %d, want 2", gotDialCount)
	}
}

type activatedWriteBlockingConn struct {
	net.Conn
	active      <-chan struct{}
	entered     chan struct{}
	release     chan struct{}
	closed      chan struct{}
	writeErr    error
	deadlines   chan time.Time
	enteredOnce sync.Once
	closedOnce  sync.Once
}

func (c *activatedWriteBlockingConn) Write(p []byte) (int, error) {
	select {
	case <-c.active:
		c.enteredOnce.Do(func() { close(c.entered) })
		if c.writeErr != nil {
			return 0, c.writeErr
		}
		select {
		case <-c.release:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	default:
	}
	return c.Conn.Write(p)
}

func (c *activatedWriteBlockingConn) SetWriteDeadline(deadline time.Time) error {
	if c.deadlines != nil {
		select {
		case c.deadlines <- deadline:
		default:
		}
	}
	return c.Conn.SetWriteDeadline(deadline)
}

func (c *activatedWriteBlockingConn) Close() error {
	if c.closed != nil {
		c.closedOnce.Do(func() { close(c.closed) })
	}
	return c.Conn.Close()
}

func newVoiceHeartbeatWriteTestConnection(t *testing.T, writeErr error) (*VoiceConnection, *activatedWriteBlockingConn, *websocket.Conn) {
	t.Helper()

	active := make(chan struct{})
	release := make(chan struct{})
	if writeErr == nil {
		close(release)
	}
	server := newVoiceOpenTestServer(t, func() { close(active) })
	transport := &activatedWriteBlockingConn{
		active:    active,
		entered:   make(chan struct{}),
		release:   release,
		closed:    make(chan struct{}),
		writeErr:  writeErr,
		deadlines: make(chan time.Time, 2),
	}
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			transport.Conn = conn
			return transport, nil
		},
	}
	conn, _, err := dialer.Dial("wss://"+strings.TrimPrefix(server.URL, "https://"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	for len(transport.deadlines) > 0 {
		<-transport.deadlines
	}
	voice := &VoiceConnection{LogLevel: -1, close: make(chan struct{}), wsConn: conn}
	t.Cleanup(func() {
		select {
		case <-voice.close:
		default:
			close(voice.close)
		}
		_ = conn.Close()
	})
	return voice, transport, conn
}

func TestVoiceOpenIdentifyDoesNotBlockClose(t *testing.T) {
	blockWrites := make(chan struct{})
	releaseWrite := make(chan struct{})

	server := newVoiceOpenTestServer(t, func() {
		close(blockWrites)
	})
	blocking := &activatedWriteBlockingConn{
		active:  blockWrites,
		entered: make(chan struct{}),
		release: releaseWrite,
	}
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			blocking.Conn = conn
			return blocking, nil
		},
	}
	voice := newVoiceOpenTestConnection(server, dialer)
	testVoiceOpenClose(t, voice, blocking.entered, releaseWrite, "Identify write")
}

func TestVoiceResumeWriteDoesNotOutliveClose(t *testing.T) {
	blockWrites := make(chan struct{})
	releaseWrite := make(chan struct{})
	server := newVoiceOpenTestServer(t, func() { close(blockWrites) })
	blocking := &activatedWriteBlockingConn{
		active:  blockWrites,
		entered: make(chan struct{}),
		release: releaseWrite,
		closed:  make(chan struct{}),
	}
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			blocking.Conn = conn
			return blocking, nil
		},
	}
	voice := newVoiceOpenTestConnection(server, dialer)
	voice.close = make(chan struct{})

	released := false
	defer func() {
		if !released {
			close(releaseWrite)
		}
	}()
	resumeDone := make(chan error, 1)
	go func() { resumeDone <- voice.resumeVoice() }()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not reach the websocket write")
	}

	voice.Close()
	select {
	case err := <-resumeDone:
		if err == nil {
			t.Fatal("resume unexpectedly succeeded after Close")
		}
	case <-time.After(200 * time.Millisecond):
		close(releaseWrite)
		released = true
		select {
		case <-resumeDone:
			t.Fatal("Close did not release the blocked voice resume write")
		case <-time.After(time.Second):
			t.Fatal("resume did not return after the test released its write")
		}
	}

	voice.RLock()
	defer voice.RUnlock()
	if voice.wsConn != nil || voice.close != nil {
		t.Fatal("the canceled voice resume installed websocket state")
	}
}

func TestVoiceOpenUsesGatewayV8Identify(t *testing.T) {
	request := make(chan struct {
		version string
		body    []byte
	}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, body, err := conn.ReadMessage()
		if err != nil {
			return
		}
		request <- struct {
			version string
			body    []byte
		}{version: r.URL.Query().Get("v"), body: body}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	session := &Session{Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	vc := &VoiceConnection{
		LogLevel:  -1,
		GuildID:   "guild",
		UserID:    "user",
		sessionID: "session",
		token:     "token",
		endpoint:  strings.TrimPrefix(server.URL, "https://"),
		session:   session,
	}
	if err := vc.open(); err != nil {
		t.Fatalf("open returned error: %v", err)
	}
	defer vc.Close()

	select {
	case got := <-request:
		if got.version != "8" {
			t.Fatalf("voice gateway version = %q, want 8", got.version)
		}
		var identify struct {
			Op   int `json:"op"`
			Data struct {
				ServerID               string `json:"server_id"`
				UserID                 string `json:"user_id"`
				SessionID              string `json:"session_id"`
				Token                  string `json:"token"`
				MaxDaveProtocolVersion *int   `json:"max_dave_protocol_version"`
			} `json:"d"`
		}
		if err := json.Unmarshal(got.body, &identify); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if identify.Op != 0 || identify.Data.ServerID != "guild" || identify.Data.UserID != "user" || identify.Data.SessionID != "session" || identify.Data.Token != "token" {
			t.Fatalf("identify = %#v", identify)
		}
		if identify.Data.MaxDaveProtocolVersion == nil || *identify.Data.MaxDaveProtocolVersion != 0 {
			t.Fatalf("MaxDaveProtocolVersion = %v, want explicit 0", identify.Data.MaxDaveProtocolVersion)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for voice identify")
	}
}

func TestVoiceHelloStartsHeartbeat(t *testing.T) {
	messages := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		_, message, err := c.ReadMessage()
		if err == nil {
			messages <- message
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	closeChan := make(chan struct{})
	defer close(closeChan)

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    closeChan,
		wsConn:   conn,
	}
	vc.updateSequence(vc.wsConn, vc.close, []byte(`{"seq":10}`))

	vc.onEvent([]byte(`{"op":8,"d":{"heartbeat_interval":1}}`))

	select {
	case message := <-messages:
		var heartbeat voiceHeartbeatOp
		if err := json.Unmarshal(message, &heartbeat); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if heartbeat.Op != 3 || heartbeat.Data.Timestamp <= 0 || heartbeat.Data.SequenceAck != 10 {
			t.Fatalf("heartbeat = %#v, want op 3 with seq_ack 10", heartbeat)
		}
	case <-time.After(time.Second):
		t.Fatal("voice Hello did not start heartbeat")
	}
}

func TestVoiceHeartbeatStopsAfterConnectionReplacement(t *testing.T) {
	heartbeats := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
			select {
			case heartbeats <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	closeChan := make(chan struct{})
	defer close(closeChan)
	voice := &VoiceConnection{LogLevel: -1, close: closeChan, wsConn: conn}
	done := make(chan struct{})
	go func() {
		voice.wsHeartbeat(conn, closeChan, 10)
		close(done)
	}()

	select {
	case <-heartbeats:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial heartbeat")
	}
	voice.Lock()
	voice.wsConn = &websocket.Conn{}
	voice.Unlock()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("heartbeat did not stop after websocket replacement")
	}
}

func TestVoiceHeartbeatWriteFailureClosesConnection(t *testing.T) {
	voice, transport, conn := newVoiceHeartbeatWriteTestConnection(t, errors.New("heartbeat write failed"))
	voice.wsHeartbeat(conn, voice.close, 1)

	select {
	case deadline := <-transport.deadlines:
		if deadline.IsZero() {
			t.Error("heartbeat write had no deadline")
		}
	default:
		t.Error("heartbeat write had no deadline")
	}
	select {
	case <-transport.closed:
	default:
		t.Error("heartbeat write failure left the websocket transport open")
	}
}

func TestVoiceHeartbeatClearsWriteDeadline(t *testing.T) {
	voice, transport, conn := newVoiceHeartbeatWriteTestConnection(t, nil)
	done := make(chan struct{})
	go func() {
		voice.wsHeartbeat(conn, voice.close, 1000)
		close(done)
	}()

	select {
	case deadline := <-transport.deadlines:
		if deadline.IsZero() {
			t.Fatal("heartbeat write had no deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for heartbeat write deadline")
	}
	select {
	case <-transport.closed:
		t.Fatal("successful heartbeat closed the websocket transport")
	default:
	}
	close(voice.close)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after lifecycle close")
	}
	// Inspect the deadline inherited by a writer that does not set its own.
	if err := conn.WriteJSON(struct{}{}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	select {
	case deadline := <-transport.deadlines:
		if !deadline.IsZero() {
			t.Fatalf("heartbeat deadline remained on the next voice write: %v", deadline)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the next voice write")
	}
}

func TestVoiceHeartbeatIncludesUnsetSequence(t *testing.T) {
	vc := &VoiceConnection{}
	payload, err := json.Marshal(voiceHeartbeatOp{Op: 3, Data: voiceHeartbeatData{Timestamp: 1, SequenceAck: vc.voiceSequenceAck()}})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"seq_ack":-1`) {
		t.Fatalf("heartbeat = %s, want seq_ack -1", payload)
	}
}

func TestVoiceUpdateSequence(t *testing.T) {
	vc := &VoiceConnection{}
	vc.updateSequence(vc.wsConn, vc.close, []byte(`{"op":5,"seq":65535,"d":{}}`))
	vc.RLock()
	sequence, set := vc.sequence, vc.sequenceSet
	vc.RUnlock()
	if !set || sequence != 65535 {
		t.Fatalf("sequence = %d/%t, want 65535/true", sequence, set)
	}

	vc.updateSequence(vc.wsConn, vc.close, []byte(`{"op":5,"d":{}}`))
	vc.RLock()
	sequence = vc.sequence
	vc.RUnlock()
	if sequence != 65535 {
		t.Fatalf("sequence changed without seq: %d", sequence)
	}

	vc.updateSequence(vc.wsConn, vc.close, []byte(`{"op":5,"seq":0,"d":{}}`))
	vc.RLock()
	sequence = vc.sequence
	vc.RUnlock()
	if sequence != 0 {
		t.Fatalf("wrapped sequence = %d, want 0", sequence)
	}
}

func TestVoiceReadyDoesNotStartHeartbeat(t *testing.T) {
	messages := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		_, message, err := c.ReadMessage()
		if err == nil {
			messages <- message
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		wsConn:   conn,
	}
	defer close(vc.close)

	vc.onEvent([]byte(`{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":1,"modes":["aead_aes256_gcm_rtpsize"],"heartbeat_interval":1}}`))

	select {
	case message := <-messages:
		t.Fatalf("voice Ready started heartbeat %s", message)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestVoiceWsListenManualCloseDoesNotReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	vc := &VoiceConnection{
		close:   make(chan struct{}),
		session: &Session{},
		wsConn:  conn,
	}

	closeChan := vc.close
	done := make(chan struct{})
	go func() {
		defer close(done)
		vc.wsListen(conn, closeChan)
	}()

	vc.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wsListen did not exit after Close")
	}

	vc.RLock()
	reconnecting := vc.reconnecting
	vc.RUnlock()
	if reconnecting {
		t.Fatal("wsListen started reconnect after manual Close")
	}
}

func TestVoiceReadyConcurrentClose(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer udpConn.Close()

	udpDone := make(chan struct{})
	go func() {
		defer close(udpDone)
		buf := make([]byte, 74)
		for {
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n != 74 {
				continue
			}
			resp := make([]byte, 74)
			copy(resp[8:], []byte("127.0.0.1"))
			binary.BigEndian.PutUint16(resp[72:], uint16(udpConn.LocalAddr().(*net.UDPAddr).Port))
			if _, err = udpConn.WriteToUDP(resp, addr); err != nil {
				return
			}
		}
	}()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	udpAddr := udpConn.LocalAddr().(*net.UDPAddr)
	ready := []byte(fmt.Sprintf(`{"op":2,"d":{"ssrc":1,"ip":%q,"port":%d,"modes":["aead_aes256_gcm_rtpsize"],"heartbeat_interval":1}}`, udpAddr.IP.String(), udpAddr.Port))

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		endpoint: "voice.example.com",
		wsConn:   conn,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		vc.onEvent(ready)
	}()

	time.Sleep(time.Millisecond)
	vc.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("voice READY handling did not finish after Close")
	}
}

// textFrameBlockingConn blocks websocket text-frame writes until released,
// while letting the HTTP handshake and control frames (e.g. close) pass
// through, to simulate a stalled TCP send.
type textFrameBlockingConn struct {
	net.Conn
	block       bool
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func (c *textFrameBlockingConn) Write(p []byte) (int, error) {
	if c.block && len(p) > 0 && int(p[0]&0x0f) == websocket.TextMessage {
		c.enteredOnce.Do(func() { close(c.entered) })
		<-c.release
	}
	return c.Conn.Write(p)
}

func (c *textFrameBlockingConn) releaseWrites() {
	c.releaseOnce.Do(func() { close(c.release) })
}

func newTextFrameBlockingWebsocket(t *testing.T) (*websocket.Conn, *textFrameBlockingConn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	blocking := &textFrameBlockingConn{
		block:   true,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	dialer := websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			blocking.Conn = conn
			return blocking, nil
		},
	}

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	t.Cleanup(blocking.releaseWrites)

	return conn, blocking
}

func TestVoiceSpeakingStalledWriteDoesNotBlockClose(t *testing.T) {
	conn, blocking := newTextFrameBlockingWebsocket(t)

	vc := &VoiceConnection{LogLevel: -1, wsConn: conn}

	speakingDone := make(chan struct{})
	go func() {
		defer close(speakingDone)
		_ = vc.Speaking(true)
	}()

	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Speaking never reached the websocket write")
	}

	closeDone := make(chan struct{})
	go func() {
		vc.Close()
		close(closeDone)
	}()

	// Close sleeps one second after the close frame by design; anything
	// well beyond that means it is wedged behind the stalled write.
	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked behind a stalled Speaking write")
	}

	blocking.releaseWrites()
	<-speakingDone
}

func TestVoiceDisconnectStalledGatewayWriteDoesNotBlockCloseOrReorderUpdates(t *testing.T) {
	conn, blocking := newTextFrameBlockingWebsocket(t)

	session := &Session{
		LogLevel:         -1,
		wsConn:           conn,
		VoiceConnections: make(map[string]*VoiceConnection),
	}
	voice := &VoiceConnection{
		LogLevel:  -1,
		GuildID:   "guild",
		sessionID: "session",
		session:   session,
	}
	session.VoiceConnections[voice.GuildID] = voice

	disconnectDone := make(chan error, 1)
	go func() {
		disconnectDone <- voice.Disconnect()
	}()

	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect never reached the gateway write")
	}

	closeDone := make(chan struct{})
	go func() {
		voice.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(time.Second):
		blocking.releaseWrites()
		t.Fatal("Close blocked behind a stalled Disconnect gateway write")
	}

	changeDone := make(chan error, 1)
	go func() {
		changeDone <- voice.ChangeChannel("stale", false, false)
	}()

	type joinResult struct {
		voice *VoiceConnection
		err   error
	}
	joinDone := make(chan joinResult, 1)
	go func() {
		joined, joinErr := session.ChannelVoiceJoin(voice.GuildID, "replacement", false, false)
		joinDone <- joinResult{voice: joined, err: joinErr}
	}()

	blocking.releaseWrites()

	var replacement *VoiceConnection
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for replacement == nil || replacement == voice {
		session.RLock()
		replacement = session.VoiceConnections[voice.GuildID]
		session.RUnlock()
		if replacement != nil && replacement != voice {
			break
		}

		select {
		case <-timer.C:
			t.Fatal("ChannelVoiceJoin reused the disconnecting voice connection")
		case <-time.After(time.Millisecond):
		}
	}

	replacement.Lock()
	replacement.Ready = true
	replacement.Unlock()

	if err := <-disconnectDone; err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if err := <-changeDone; !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
	joined := <-joinDone
	if joined.err != nil {
		t.Fatalf("ChannelVoiceJoin() error = %v", joined.err)
	}
	if joined.voice != replacement {
		t.Fatal("ChannelVoiceJoin did not return the replacement voice connection")
	}

	session.RLock()
	got := session.VoiceConnections[voice.GuildID]
	session.RUnlock()
	if got != replacement {
		t.Fatal("Disconnect removed a replacement voice connection")
	}
}

type voiceChangeStateBufferPool struct {
	once    sync.Once
	written chan struct{}
	release chan struct{}
}

func (*voiceChangeStateBufferPool) Get() interface{} { return nil }
func (p *voiceChangeStateBufferPool) Put(interface{}) {
	p.once.Do(func() {
		close(p.written)
		<-p.release
	})
}

func TestVoiceChangeChannelPreservesReceivedState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		channels []string
		userID   string
		want     string
	}{
		{name: "newer_move", channels: []string{"20", "30"}, userID: "200", want: "30"},
		{name: "move_back", channels: []string{"20", "10"}, userID: "200", want: "10"},
		{name: "request_ack", channels: []string{"20"}, userID: "200", want: "20"},
		{name: "other_user", channels: []string{"30"}, userID: "201", want: "20"},
		{name: "no_event", want: "20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peerReady := make(chan *websocket.Conn, 1)
			serverDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				peerReady <- conn
				<-serverDone
			}))
			defer server.Close()
			defer close(serverDone)
			pool := &voiceChangeStateBufferPool{written: make(chan struct{}), release: make(chan struct{})}
			dialer := websocket.Dialer{WriteBufferPool: pool}
			conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			peer := <-peerReady
			defer func() { _ = conn.Close(); _ = peer.Close() }()
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel, session.SyncEvents = -1, true
			session.ShouldReconnectOnError = false
			session.wsConn, session.listening = conn, make(chan interface{})
			session.State.User = &User{ID: "200"}
			if err := session.State.GuildAdd(&Guild{ID: "100"}); err != nil {
				t.Fatal(err)
			}
			voice := &VoiceConnection{LogLevel: -1, session: session, GuildID: "100", ChannelID: "10"}
			session.VoiceConnections = map[string]*VoiceConnection{"100": voice}
			dispatched := make(chan *VoiceStateUpdate, 2)
			session.AddHandler(func(_ *Session, update *VoiceStateUpdate) { dispatched <- update })
			listenerDone := make(chan struct{})
			listening := session.listening
			go func() { defer close(listenerDone); session.listen(conn, listening) }()
			changeDone := make(chan struct{})
			var changeErr error
			go func() { defer close(changeDone); changeErr = voice.ChangeChannel("20", true, true) }()
			defer func() {
				closeChannel(pool.release)
				<-changeDone
				_ = session.Close()
				<-listenerDone
			}()
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var request voiceChannelJoinOp
			if err := peer.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "20" || !request.Data.SelfMute || !request.Data.SelfDeaf {
				t.Fatalf("channel change request = %+v", request)
			}
			// The frame is already on the wire, but buffer recycling delays the
			// caller's local commit while the real gateway reader receives events.
			select {
			case <-pool.written:
			case <-time.After(3 * time.Second):
				t.Fatal("channel change did not reach buffer recycling")
			}
			for i, channelID := range tc.channels {
				if err := peer.WriteJSON(map[string]interface{}{
					"op": 0, "s": i + 1, "t": "VOICE_STATE_UPDATE",
					"d": &VoiceState{GuildID: "100", ChannelID: channelID, UserID: tc.userID, SessionID: "voice-session"},
				}); err != nil {
					t.Fatal(err)
				}
				select {
				case update := <-dispatched:
					if update.ChannelID != channelID || update.UserID != tc.userID {
						t.Fatalf("unexpected voice-state callback: %+v", update)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("gateway reader did not dispatch the voice state")
				}
			}
			if tc.userID == "200" {
				voice.RLock()
				receivedChannel := voice.ChannelID
				voice.RUnlock()
				if receivedChannel != tc.want {
					t.Fatalf("voice-state handler selected %s before local commit, want %s", receivedChannel, tc.want)
				}
			}
			close(pool.release)
			select {
			case <-changeDone:
			case <-time.After(3 * time.Second):
				t.Fatal("channel change did not finish")
			}
			if changeErr != nil {
				t.Fatal(changeErr)
			}
			voice.RLock()
			channelID, mute, deaf := voice.ChannelID, voice.mute, voice.deaf
			voice.RUnlock()
			if channelID != tc.want || !mute || !deaf {
				t.Errorf("channel/mute/deaf = %s/%t/%t, want %s/true/true", channelID, mute, deaf, tc.want)
			}
			if tc.userID == "200" {
				cached, err := session.State.VoiceState("100", "200")
				if err != nil || cached.ChannelID != tc.want {
					t.Errorf("cached voice state does not match received channel: %+v, %v", cached, err)
				}
			}
		})
	}
}

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

func TestVoiceErrorLoggingConcurrentEndpointUpdate(t *testing.T) {
	for _, path := range []string{"heartbeat", "websocket", "udp"} {
		t.Run(path, func(t *testing.T) {
			conn, _ := newGatewayTestConnection(t)
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			if err := udp.Close(); err != nil {
				t.Fatal(err)
			}
			signal := make(chan struct{})
			voice := &VoiceConnection{LogLevel: -1, wsConn: conn, udpConn: udp, close: signal, reconnecting: true}
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					voice.Lock()
					voice.endpoint = "replacement.voice.example"
					voice.Unlock()
					runtime.Gosched()
				}
			}()
			for i := 0; i < 100; i++ {
				switch path {
				case "heartbeat":
					voice.wsHeartbeat(conn, signal, 1)
				case "websocket":
					voice.Lock()
					voice.wsConn = conn
					voice.Unlock()
					voice.wsListen(conn, signal)
				case "udp":
					// A stale generation must not launch recovery on this connection.
					voice.opusReceiver(udp, make(chan struct{}), nil)
				}
			}
			close(stop)
			<-done
		})
	}
}

func TestVoiceIdentifyStalledWriteTimesOut(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	handshake := make(chan error, 1)
	go func() {
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			handshake <- err
			return
		}
		_ = request.Body.Close()
		accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(peer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
		handshake <- err
		// Keep the peer open without reading the Identify packet.
	}()
	transport := &observedVoiceWriteConn{Conn: client, entered: make(chan struct{})}
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second, NetDialTLSContext: func(context.Context, string, string) (net.Conn, error) {
		// Supply an established transport without real TLS in this fixture.
		return transport, nil
	}}
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	session := &Session{Dialer: dialer, VoiceDAVESessionFactory: func(string, uint64, VoiceDAVECallbacks) (VoiceDAVESession, error) {
		return backend, nil
	}}
	voice := &VoiceConnection{
		LogLevel: -1, GuildID: "100", UserID: "200", ChannelID: "300",
		sessionID: "session", token: "dummy-token", endpoint: "voice.invalid", session: session,
	}
	done := make(chan struct{})
	var openErr error
	go func() {
		defer close(done)
		openErr = voice.open()
	}()
	t.Cleanup(func() { voice.Close(); _ = client.Close(); <-done })
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("Identify did not reach its transport write")
	}
	select {
	case <-done:
		var timeout net.Error
		if !errors.As(openErr, &timeout) || !timeout.Timeout() {
			t.Fatalf("open error = %v, want a write timeout", openErr)
		}
	case <-time.After(2 * websocketWriteTimeout):
		t.Fatal("stalled Identify write did not time out")
	}
	if voice.opening != nil || voice.wsConn != nil || voice.dave != nil {
		t.Error("failed Identify retained startup or installed connection state")
	}
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if closed != 1 {
		t.Errorf("uninstalled DAVE backend closed %d times, want 1", closed)
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read error = %v, want EOF after startup cleanup", err)
	}
}

func TestVoiceIdentifyClearsWriteDeadline(t *testing.T) {
	server := newVoiceOpenTestServer(t, nil)
	transport := &activatedWriteBlockingConn{deadlines: make(chan time.Time, 2)}
	tlsDialer := &tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig}
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second, NetDialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := tlsDialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		transport.Conn = conn
		return transport, nil
	}}
	voice := newVoiceOpenTestConnection(server, dialer)
	defer voice.Close()
	if err := voice.open(); err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; deadline.IsZero() {
		t.Error("Identify write had no deadline")
	}
	voice.RLock()
	conn := voice.wsConn
	voice.RUnlock()
	if conn == nil {
		t.Fatal("successful Identify did not install a websocket")
	}
	voice.wsMutex.Lock()
	err := conn.WriteJSON(struct{}{})
	voice.wsMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; !deadline.IsZero() {
		t.Error("Identify left its deadline on the next write")
	}
}

type voiceJoinChangeCleanupPool struct {
	puts    atomic.Uint32
	written chan struct{}
	release chan struct{}
}

func (*voiceJoinChangeCleanupPool) Get() interface{} { return nil }
func (p *voiceJoinChangeCleanupPool) Put(interface{}) {
	if p.puts.Add(1) == 2 {
		close(p.written)
		<-p.release
	}
}

func TestVoiceJoinTimeoutPreservesChannelChangeDuringWrite(t *testing.T) {
	peerReady := make(chan *websocket.Conn, 1)
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		peerReady <- conn
		<-serverDone
	}))
	defer server.Close()
	defer close(serverDone)
	pool := &voiceJoinChangeCleanupPool{written: make(chan struct{}), release: make(chan struct{})}
	dialer := websocket.Dialer{WriteBufferPool: pool}
	gateway, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-peerReady
	defer func() { _ = gateway.Close(); _ = peer.Close() }()
	replacement, replacementPeer := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.wsConn, session.LogLevel = gateway, LogWarning
	paused, releaseTimeout := make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error waiting for voice to connect, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-releaseTimeout
		}
	}
	defer func() { Logger = oldLogger }()
	firstDone := make(chan struct{})
	var changeDone chan struct{}
	var firstErr, changeErr error
	go func() {
		defer close(firstDone)
		_, firstErr = session.ChannelVoiceJoin("guild", "old-channel", false, false)
	}()
	defer func() {
		closeChannel(releaseTimeout)
		closeChannel(pool.release)
		_ = session.Close()
		<-firstDone
		if changeDone != nil {
			<-changeDone
		}
	}()
	readJoin := func(channelID string) {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var op voiceChannelJoinOp
		if err := peer.ReadJSON(&op); err != nil {
			t.Fatal(err)
		}
		if op.Op != 4 || op.Data.ChannelID == nil || *op.Data.ChannelID != channelID {
			t.Fatalf("voice request = %#v, want channel %s", op, channelID)
		}
	}
	readJoin("old-channel")
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("old join did not reach timeout cleanup")
	}
	session.RLock()
	voice := session.VoiceConnections["guild"]
	session.RUnlock()
	changeDone = make(chan struct{})
	go func() {
		defer close(changeDone)
		changeErr = voice.ChangeChannel("new-channel", false, false)
	}()
	readJoin("new-channel")
	select {
	case <-pool.written:
	case <-time.After(3 * time.Second):
		t.Fatal("channel change did not reach buffer recycling")
	}
	// Model a replacement transport after the new request reached the peer,
	// while buffer recycling still holds its caller before the local commit.
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	installFakeDAVE(voice, backend, 1)
	replacementClose := make(chan struct{})
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	close(releaseTimeout)
	// Give the released timeout cleanup an opportunity to run before the
	// newer write returns. A correctly serialized cleanup waits for it.
	select {
	case <-firstDone:
	case <-time.After(250 * time.Millisecond):
	}
	close(pool.release)
	for _, done := range []<-chan struct{}{changeDone, firstDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("voice request did not finish")
		}
	}
	if changeErr != nil || firstErr == nil || !strings.Contains(firstErr.Error(), "timeout waiting for voice") {
		t.Fatalf("change/old join errors = %v/%v", changeErr, firstErr)
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready && voice.ChannelID == "new-channel"
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("old join cleanup destroyed newer transport: preserved=%t, backend closes=%d", preserved, closed)
	}
	if err := voice.Speaking(true); err != nil {
		t.Fatalf("new transport is unusable after channel change: %v", err)
	}
	if err := replacementPeer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var speaking struct {
		Op int `json:"op"`
	}
	if err := replacementPeer.ReadJSON(&speaking); err != nil || speaking.Op != 5 {
		t.Errorf("replacement speaking command = %d, %v", speaking.Op, err)
	}
}

func TestVoiceJoinDoesNotSucceedForNewerChannel(t *testing.T) {
	for _, tc := range []struct {
		name          string
		channelID     string
		changeChannel bool
	}{
		{name: "newer_join", channelID: "second-channel"},
		{name: "channel_change", channelID: "second-channel", changeChannel: true},
		{name: "same_channel", channelID: "first-channel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, peer := newGatewayTestConnection(t)
			transport, _ := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn = gateway
			voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			firstDone := make(chan struct{})
			var secondDone chan struct{}
			var firstVoice, secondVoice *VoiceConnection
			var firstErr, secondErr error
			go func() {
				defer close(firstDone)
				firstVoice, firstErr = session.ChannelVoiceJoin("guild", "first-channel", false, false)
			}()
			defer func() {
				_ = session.Close()
				<-firstDone
				if secondDone != nil {
					<-secondDone
				}
			}()
			readJoin := func(channelID string) {
				t.Helper()
				if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var request voiceChannelJoinOp
				if err := peer.ReadJSON(&request); err != nil {
					t.Fatal(err)
				}
				if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != channelID {
					t.Fatalf("unexpected voice request: %+v", request)
				}
			}
			readJoin("first-channel")
			// Withhold readiness until a later public request selects its channel.
			secondDone = make(chan struct{})
			go func() {
				defer close(secondDone)
				if tc.changeChannel {
					secondVoice = voice
					secondErr = voice.ChangeChannel(tc.channelID, false, false)
				} else {
					secondVoice, secondErr = session.ChannelVoiceJoin("guild", tc.channelID, false, false)
				}
			}()
			readJoin(tc.channelID)
			if tc.changeChannel {
				select {
				case <-secondDone:
				case <-time.After(3 * time.Second):
					t.Fatal("channel change did not finish")
				}
			}
			// Model only the later request's completed transport. Neither request
			// was ready before this point; no full voice handshake is simulated.
			backend := &fakeVoiceDAVESession{maxVersion: 1}
			installFakeDAVE(voice, backend, 1)
			transportClose := make(chan struct{})
			voice.Lock()
			voice.wsConn, voice.close, voice.Ready = transport, transportClose, true
			voice.Unlock()
			select {
			case <-secondDone:
			case <-time.After(3 * time.Second):
				t.Fatal("newer request did not finish")
			}
			if secondErr != nil || secondVoice != voice {
				t.Fatalf("newer request = %p, %v; want the registered voice and success", secondVoice, secondErr)
			}
			select {
			case <-firstDone:
			case <-time.After(13 * time.Second):
				t.Fatal("first join did not finish within its readiness timeout")
			}
			if firstVoice != voice {
				t.Error("first join replaced the registered voice handle")
			}
			if tc.channelID == "first-channel" {
				if firstErr != nil {
					t.Errorf("same-channel join failed: %v", firstErr)
				}
			} else if firstErr == nil || !strings.Contains(firstErr.Error(), "timeout waiting for voice") {
				t.Errorf("first join returned %v with only %s ready, want readiness timeout", firstErr, tc.channelID)
			}
			voice.RLock()
			preserved := voice.wsConn == transport && voice.close == transportClose && voice.Ready && voice.ChannelID == tc.channelID
			voice.RUnlock()
			backend.mu.Lock()
			closed := backend.closed
			backend.mu.Unlock()
			if !preserved || closed != 0 {
				t.Errorf("first join disturbed newer transport: preserved=%t, backend closes=%d", preserved, closed)
			}
		})
	}
}

func TestVoiceJoinDoesNotReuseOtherChannelReadiness(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelID   string
		replacement bool
	}{
		{name: "same channel", channelID: "old-channel"},
		{name: "other channel without handshake", channelID: "new-channel"},
		{name: "other channel with new transport", channelID: "new-channel", replacement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, peer := newGatewayTestConnection(t)
			transport, _ := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn = gateway
			voice := &VoiceConnection{
				LogLevel: -1, GuildID: "guild", ChannelID: "old-channel", session: session,
				Ready: true, wsConn: transport, close: make(chan struct{}),
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			done := make(chan struct{})
			var joined *VoiceConnection
			var joinErr error
			go func() {
				defer close(done)
				joined, joinErr = session.ChannelVoiceJoin("guild", tc.channelID, false, false)
			}()
			defer func() {
				_ = session.Close()
				<-done
			}()
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var request voiceChannelJoinOp
			if err := peer.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != tc.channelID {
				t.Fatalf("unexpected join request: %+v", request)
			}
			if tc.replacement {
				// Model the later voice handshake replacing the old transport.
				voice.Close()
				replacement, _ := newGatewayTestConnection(t)
				voice.Lock()
				voice.wsConn, voice.close, voice.Ready = replacement, make(chan struct{}), true
				voice.Unlock()
			}
			// Discord sends no voice events when the requested channel is full.
			// Withhold them here: only the old channel's transport is ready.
			select {
			case <-done:
			case <-time.After(13 * time.Second):
				t.Fatal("join did not complete within its readiness timeout")
			}
			if joined != voice {
				t.Fatal("join unexpectedly replaced the registered voice handle")
			}
			if tc.channelID == "old-channel" || tc.replacement {
				if joinErr != nil {
					t.Fatalf("join to the ready channel failed: %v", joinErr)
				}
			} else if joinErr == nil || !strings.Contains(joinErr.Error(), "timeout waiting for voice") {
				t.Fatalf("join to another channel returned %v without any new voice events, want readiness timeout", joinErr)
			}
		})
	}
}

func TestVoiceJoinTimeoutPreservesNewerJoin(t *testing.T) {
	testVoiceJoinTimeoutPreservesNewerRequest(t, false)
}

func TestVoiceJoinTimeoutPreservesChannelChange(t *testing.T) {
	testVoiceJoinTimeoutPreservesNewerRequest(t, true)
}

func testVoiceJoinTimeoutPreservesNewerRequest(t *testing.T, changeChannel bool) {
	t.Helper()
	gateway, peer := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.wsConn = gateway
	session.LogLevel = LogWarning
	paused, release := make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error waiting for voice to connect, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	firstDone := make(chan struct{})
	var secondDone chan struct{}
	var firstErr, secondErr error
	var secondVoice *VoiceConnection
	go func() {
		defer close(firstDone)
		_, firstErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-firstDone
		if secondDone != nil {
			<-secondDone
		}
	}()
	readJoin := func(channelID string) {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var op voiceChannelJoinOp
		if err := peer.ReadJSON(&op); err != nil {
			t.Fatal(err)
		}
		if op.Op != 4 || op.Data.ChannelID == nil || *op.Data.ChannelID != channelID {
			t.Fatalf("unexpected voice join: %#v", op)
		}
	}
	readJoin("channel")
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("first voice join did not reach timeout cleanup")
	}
	session.RLock()
	voice := session.VoiceConnections["guild"]
	session.RUnlock()
	nextChannel := "channel"
	if changeChannel {
		nextChannel = "new-channel"
	}
	secondDone = make(chan struct{})
	go func() {
		defer close(secondDone)
		if changeChannel {
			secondVoice = voice
			secondErr = voice.ChangeChannel(nextChannel, false, false)
		} else {
			secondVoice, secondErr = session.ChannelVoiceJoin("guild", nextChannel, false, false)
		}
	}()
	readJoin(nextChannel)
	// Model completion of the later request after its real gateway send.
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	installFakeDAVE(voice, backend, 1)
	replacementClose := make(chan struct{})
	voice.Lock()
	voice.wsConn = replacement
	voice.close = replacementClose
	voice.Ready = true
	voice.Unlock()
	select {
	case <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("second voice request did not finish")
	}
	if secondErr != nil || secondVoice != voice {
		t.Fatalf("second request = %p, %v; want the existing voice and success", secondVoice, secondErr)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first voice join did not finish cleanup")
	}
	if firstErr == nil || !strings.Contains(firstErr.Error(), "timeout waiting for voice") {
		t.Fatalf("first join error = %v, want readiness timeout", firstErr)
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready && voice.ChannelID == nextChannel
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("old join cleanup changed successful newer request: transport preserved=%t, backend closes=%d", preserved, closed)
	}
}

func TestVoiceJoinWaitCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		closeSession bool
		beforeWait   bool
	}{
		{name: "voice disconnect"},
		{name: "session close", closeSession: true},
		{name: "already disconnected", beforeWait: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session}
				session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
				disconnect := voice.Disconnect
				if tc.closeSession {
					disconnect = session.Close
				}
				if tc.beforeWait {
					if err := disconnect(); err != nil {
						t.Fatal(err)
					}
				}
				result := make(chan error, 1)
				go func() { result <- voice.waitUntilConnected("", nil) }()
				synctest.Wait()
				closedAt := time.Now()
				if !tc.beforeWait {
					if err := disconnect(); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
				}
				if len(result) == 0 {
					t.Error("disconnected voice join is still waiting for readiness")
				}
				// Join even on failure to measure the remaining timeout in fake time.
				if err := <-result; !errors.Is(err, ErrWSNotFound) {
					t.Errorf("wait error = %v, want %v", err, ErrWSNotFound)
				}
				if delay := time.Since(closedAt); delay != 0 {
					t.Errorf("wait returned %v after disconnect, want no delay", delay)
				}
			})
		})
	}
}

func TestVoiceOnEventSkipsRedactionWhenDebugDisabled(t *testing.T) {
	payload := strings.Builder{}
	payload.WriteString(`{"op":99,"d":{`)
	for i := 0; i < 1000; i++ {
		if i > 0 {
			payload.WriteString(",")
		}
		fmt.Fprintf(&payload, `"field%d":"value%d"`, i, i)
	}
	payload.WriteString(`}}`)
	message := []byte(payload.String())

	voice := &VoiceConnection{} // default LogLevel is LogError
	allocs := testing.AllocsPerRun(20, func() {
		voice.onEvent(message)
	})

	// Redacting the payload unmarshals and re-marshals every field.
	// The plain envelope decode stays far below that.
	if allocs > 500 {
		t.Fatalf("onEvent() allocations = %v with debug logging disabled, want <= 500", allocs)
	}
}

func TestVoiceOnEventLogsRedactedDataWhenDebugEnabled(t *testing.T) {
	oldLogger := Logger
	defer func() { Logger = oldLogger }()

	var logged []string
	Logger = func(_, _ int, format string, a ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, a...))
	}

	voice := &VoiceConnection{LogLevel: LogDebug}
	voice.onEvent([]byte(`{"op":99,"d":{"token":"secret-token","safe":"ok"}}`))

	found := false
	for _, line := range logged {
		if strings.Contains(line, "REDACTED") {
			found = true
		}
		if strings.Contains(line, "secret-token") {
			t.Fatalf("debug log leaked token: %q", line)
		}
	}
	if !found {
		t.Fatalf("debug log did not include redacted voice data: %q", logged)
	}
}

func BenchmarkVoiceEventRedaction(b *testing.B) {
	oldLogger := Logger
	Logger = func(int, int, string, ...interface{}) {}
	b.Cleanup(func() { Logger = oldLogger })

	message := []byte(`{"op":5,"d":{"user_id":"123456789012345678","ssrc":42,"speaking":1}}`)
	for _, level := range []struct {
		name string
		log  int
	}{
		{name: "debug-disabled", log: LogError},
		{name: "debug-enabled", log: LogDebug},
	} {
		b.Run(level.name, func(b *testing.B) {
			voice := &VoiceConnection{LogLevel: level.log}
			b.ReportAllocs()
			for b.Loop() {
				voice.onEvent(message)
			}
		})
	}
}

func TestVoiceJoinPreservesPendingReadiness(t *testing.T) {
	for _, changeChannel := range []bool{false, true} {
		name := "join"
		if changeChannel {
			name = "change_channel"
		}
		t.Run(name, func(t *testing.T) {
			gateway, peer := newGatewayTestConnection(t)
			transport, _ := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn = gateway
			voice := &VoiceConnection{
				LogLevel: -1, GuildID: "guild", ChannelID: "old-channel", session: session,
				Ready: true, wsConn: transport, close: make(chan struct{}),
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			firstDone := make(chan struct{})
			var firstErr error
			go func() {
				defer close(firstDone)
				if changeChannel {
					firstErr = voice.ChangeChannel("new-channel", false, false)
				} else {
					_, firstErr = session.ChannelVoiceJoin("guild", "new-channel", false, false)
				}
			}()
			var secondDone chan struct{}
			defer func() {
				_ = session.Close()
				<-firstDone
				if secondDone != nil {
					<-secondDone
				}
			}()
			readJoin := func() {
				t.Helper()
				if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var request voiceChannelJoinOp
				if err := peer.ReadJSON(&request); err != nil {
					t.Fatal(err)
				}
				if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "new-channel" {
					t.Fatalf("unexpected join request: %+v", request)
				}
			}
			readJoin()
			if changeChannel {
				select {
				case <-firstDone:
				case <-time.After(3 * time.Second):
					t.Fatal("ChangeChannel did not finish its send")
				}
				if firstErr != nil {
					t.Fatal(firstErr)
				}
			}
			secondDone = make(chan struct{})
			var joined *VoiceConnection
			var secondErr error
			go func() {
				defer close(secondDone)
				joined, secondErr = session.ChannelVoiceJoin("guild", "new-channel", false, false)
			}()
			readJoin()
			// Neither request receives voice events; the transport still
			// belongs to old-channel even though ChannelID now says new-channel.
			select {
			case <-secondDone:
			case <-time.After(13 * time.Second):
				t.Fatal("second join did not finish within its readiness timeout")
			}
			if joined != voice {
				t.Fatal("second join unexpectedly replaced the registered handle")
			}
			if secondErr == nil || !strings.Contains(secondErr.Error(), "timeout waiting for voice") {
				t.Fatalf("second join returned %v without a new transport, want readiness timeout", secondErr)
			}
		})
	}
}

func TestVoiceQueuedRetryPreservesNewerJoin(t *testing.T) {
	gateway, peer := newGatewayTestConnection(t)
	failedConn, _ := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn, session.DataReady = gateway, true
	voice := &VoiceConnection{
		LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session,
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error reconnecting to channel %s, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	var joinDone chan struct{}
	go func() {
		defer close(done)
		voice.reconnectWithResume(false, nil, nil)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
		if joinDone != nil {
			<-joinDone
		}
	}()
	readJoin := func() {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var update voiceChannelJoinOp
		if err := peer.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "channel" {
			t.Fatalf("unexpected join: %+v", update)
		}
	}
	readJoin()
	// Model a transport installed during the first recovery's join wait.
	// A real failed UDP keepalive queues another recovery and tears it down.
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := udp.Close(); err != nil {
		t.Fatal(err)
	}
	failedClose, failedDone := make(chan struct{}), make(chan struct{})
	installFakeDAVE(voice, &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() { close(failedDone) }}, 1)
	voice.Lock()
	voice.wsConn, voice.udpConn, voice.close = failedConn, udp, failedClose
	voice.Unlock()
	voice.udpKeepAlive(udp, failedClose, time.Second)
	select {
	case <-failedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("UDP failure did not tear down the attempted transport")
	}
	voice.lifecycleMu.Lock()
	voice.RLock()
	queued := voice.reconnecting && voice.reconnectAgain && voice.close == nil
	voice.RUnlock()
	voice.lifecycleMu.Unlock()
	if !queued {
		t.Fatal("UDP failure did not queue recovery")
	}
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("original join did not reach its failure reset")
	}
	joinDone = make(chan struct{})
	var joined *VoiceConnection
	var joinErr error
	go func() {
		defer close(joinDone)
		joined, joinErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	readJoin()
	// Model completion of the newer public join after its actual OP4 write.
	replacementClose := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
		// Stop further retries only after the unwanted destructive teardown.
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
	}}
	installFakeDAVE(voice, backend, 1)
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	select {
	case <-joinDone:
	case <-time.After(3 * time.Second):
		t.Fatal("newer public join did not complete")
	}
	if joined != voice || joinErr != nil {
		t.Fatalf("newer join = %p, %v; want same handle and success", joined, joinErr)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("obsolete recovery did not finish")
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready
	reconnecting := voice.reconnecting || voice.reconnectAgain
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || reconnecting || closed != 0 {
		t.Errorf("obsolete queued recovery changed newer join: preserved=%t, reconnecting=%t, backend closes=%d", preserved, reconnecting, closed)
	}
	select {
	case <-replacementClose:
		t.Error("obsolete queued recovery canceled the replacement lifetime")
	default:
	}
}

func TestVoiceReconnectBatchCancellation(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%v", closeSession), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				session.VoiceConnections = make(map[string]*VoiceConnection)
				for i := 0; i < 4; i++ {
					guildID := fmt.Sprint(i)
					session.VoiceConnections[guildID] = &VoiceConnection{
						LogLevel: -1, GuildID: guildID, ChannelID: "channel", session: session,
					}
				}
				defer func() { _ = session.Close() }()
				done := make(chan struct{})
				started := time.Now()
				go func() {
					defer close(done)
					session.reconnectVoiceConnections(session.reconnectCancelSignal())
				}()
				// The batch has scheduled its first recovery and is waiting
				// between guilds; individual recovery also waits for the gateway.
				synctest.Wait()
				if closeSession {
					if err := session.Close(); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					select {
					case <-done:
					default:
						t.Error("Session.Close left the voice reconnect batch running")
					}
				}
				// Join even on failure and measure the remaining pacing delay.
				<-done
				want := 3 * time.Second
				if closeSession {
					want = 0
				}
				if elapsed := time.Since(started); elapsed != want {
					t.Errorf("batch elapsed = %v, want %v", elapsed, want)
				}
			})
		})
	}
}

func TestSessionCloseCancelsVoiceReconnectBackoff(t *testing.T) {
	testVoiceReconnectBackoffCancellation(t, func(session *Session, _ *VoiceConnection) error {
		return session.Close()
	})
}

func TestVoiceDisconnectCancelsReconnectBackoff(t *testing.T) {
	testVoiceReconnectBackoffCancellation(t, func(session *Session, voice *VoiceConnection) error {
		// Permanent disconnection must remain safe to repeat.
		for i := 0; i < 2; i++ {
			if err := voice.Disconnect(); err != nil {
				return err
			}
		}
		select {
		case <-session.reconnectCancel:
			t.Error("voice disconnect canceled the entire session")
		default:
		}
		return nil
	})
}

func testVoiceReconnectBackoffCancellation(t *testing.T, disconnect func(*Session, *VoiceConnection) error) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		session, err := New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		session.LogLevel = -1
		voice := &VoiceConnection{
			LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
		}
		session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
		done := make(chan struct{})
		go func() {
			defer close(done)
			voice.reconnectWithResume(true, nil, nil)
		}()
		synctest.Wait()
		// The unavailable gateway makes recovery back off through 1, 2,
		// ... 512 seconds. It is now waiting on its capped 600-second timer.
		time.Sleep(1023 * time.Second)
		synctest.Wait()
		closedAt := time.Now()
		if err := disconnect(session, voice); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Error("disconnect left the voice reconnect worker waiting on backoff")
		}
		// Join even on failure; fake time exposes the retained worker's delay.
		<-done
		if delay := time.Since(closedAt); delay != 0 {
			t.Errorf("voice reconnect exited %v after disconnect, want no backoff delay", delay)
		}
		if voice.reconnecting {
			t.Error("voice reconnecting flag was not cleared")
		}
	})
}

func TestVoiceReconnectDoesNotRejoinAfterDisconnect(t *testing.T) {
	conn, peer := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.DataReady, session.wsConn = true, conn
	voice := &VoiceConnection{
		LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session,
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "trying to reconnect to channel %s" {
			close(paused)
			<-release
		}
	}
	requests := make(chan voiceChannelJoinOp, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		var request voiceChannelJoinOp
		if err := peer.ReadJSON(&request); err == nil {
			requests <- request
		}
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		_ = peer.Close()
		<-done
		<-readDone
		Logger = oldLogger
	}()
	go func() {
		defer close(done)
		voice.reconnectWithResume(true, nil, nil)
	}()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("voice recovery did not reach its join attempt")
	}
	if err := voice.Disconnect(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case request := <-requests:
		t.Errorf("disconnected recovery sent a gateway request: op=%d, channel=%v", request.Op, request.Data.ChannelID)
		// Stop the unexpected join's readiness wait only after observing its
		// request, so cleanup cannot hide the original failure.
		session.RLock()
		unexpected := session.VoiceConnections["guild"]
		session.RUnlock()
		if unexpected != nil {
			t.Error("disconnected recovery registered a new voice connection")
			_ = unexpected.Disconnect()
		}
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected recovery did not return")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("voice recovery did not finish")
	}
	session.RLock()
	registered := session.VoiceConnections["guild"]
	session.RUnlock()
	if registered != nil {
		t.Error("voice connection remains registered after Disconnect")
	}
	if voice.reconnecting {
		t.Error("voice reconnecting flag was not cleared")
	}
}

func TestVoiceReconnectLoggingConcurrentJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := &Session{LogLevel: -1, VoiceConnections: make(map[string]*VoiceConnection)}
		voice := &VoiceConnection{
			LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
			// An existing recovery worker makes duplicate reconnects return.
			reconnecting: true,
		}
		session.VoiceConnections[voice.GuildID] = voice
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				// Gateway recovery snapshots the registered voice connections
				// while a caller can independently request a channel join.
				session.reconnectVoiceConnections(session.reconnectCancelSignal())
			}
		}()
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				_, err := session.ChannelVoiceJoin("guild", "channel", false, false)
				// No gateway is installed: the public join still configures the
				// registered voice connection before its send returns this error.
				if !errors.Is(err, ErrWSNotFound) {
					t.Errorf("ChannelVoiceJoin error = %v, want ErrWSNotFound", err)
					return
				}
			}
		}()
		workers.Wait()
		synctest.Wait()
	})
}

func TestVoiceReconnectFailureDoesNotDisconnectNewerJoin(t *testing.T) {
	testVoiceReconnectResetOwnership(t, true)
}

func TestVoiceReconnectFailureResetsCurrentJoin(t *testing.T) {
	testVoiceReconnectResetOwnership(t, false)
}

func testVoiceReconnectResetOwnership(t *testing.T, newerJoin bool) {
	t.Helper()
	gateway, peer := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn, session.DataReady = gateway, true
	voice := &VoiceConnection{
		LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session,
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release := make(chan struct{}), make(chan struct{})
	var pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error reconnecting to channel %s, %s" && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	done := make(chan struct{})
	var joinDone chan struct{}
	go func() {
		defer close(done)
		voice.reconnectWithResume(false, nil, nil)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
		if joinDone != nil {
			<-joinDone
		}
	}()
	readUpdate := func() voiceChannelJoinOp {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var update voiceChannelJoinOp
		if err := peer.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		return update
	}
	checkJoin := func(update voiceChannelJoinOp) {
		t.Helper()
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "channel" {
			t.Fatalf("unexpected join: %+v", update)
		}
	}
	checkJoin(readUpdate())
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("recovery did not reach its failed-join reset")
	}
	if !newerJoin {
		close(release)
		update := readUpdate()
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID != nil || !update.Data.SelfMute || !update.Data.SelfDeaf {
			t.Errorf("current recovery reset = %+v, want a leave request", update)
		}
		return
	}
	joinDone = make(chan struct{})
	var joined *VoiceConnection
	var joinErr error
	go func() {
		defer close(joinDone)
		joined, joinErr = session.ChannelVoiceJoin("guild", "channel", false, false)
	}()
	checkJoin(readUpdate())
	// Model the newer join completing after its actual gateway request.
	replacementClose := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1}
	installFakeDAVE(voice, backend, 1)
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	select {
	case <-joinDone:
	case <-time.After(3 * time.Second):
		t.Fatal("newer join did not finish")
	}
	if joined != voice || joinErr != nil {
		t.Fatalf("newer join = %p, %v; want original handle and success", joined, joinErr)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("obsolete recovery did not finish")
	}
	// All recovery writes precede this marker, so a reset cannot be hidden
	// by the scheduling of a reader goroutine or by fixture cleanup.
	if err := session.ChannelVoiceJoinManual("marker", "marker", false, false); err != nil {
		t.Fatal(err)
	}
	update := readUpdate()
	if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "marker" || update.Data.ChannelID == nil || *update.Data.ChannelID != "marker" {
		t.Errorf("obsolete recovery sent an unexpected voice update before the marker: %+v", update)
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready && !voice.reconnecting
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if !preserved || closed != 0 {
		t.Errorf("obsolete recovery changed newer transport: preserved=%t, backend closes=%d", preserved, closed)
	}
}

func TestVoiceRetryPreservesNewerRequest(t *testing.T) {
	for _, changeChannel := range []bool{false, true} {
		name := "join"
		if changeChannel {
			name = "change_channel"
		}
		t.Run(name, func(t *testing.T) {
			gateway, peer := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn, session.DataReady = gateway, true
			voice := &VoiceConnection{
				LogLevel: LogInformational, GuildID: "guild", ChannelID: "old-channel", session: session,
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var pauseOnce atomic.Bool
			oldLogger := Logger
			Logger = func(_, _ int, format string, _ ...interface{}) {
				if format == "trying to reconnect to channel %s" && pauseOnce.CompareAndSwap(false, true) {
					close(paused)
					<-release
				}
			}
			defer func() { Logger = oldLogger }()
			var requestDone chan struct{}
			go func() {
				defer close(done)
				voice.reconnectWithResume(false, nil, nil)
			}()
			defer func() {
				closeChannel(release)
				_ = session.Close()
				<-done
				if requestDone != nil {
					<-requestDone
				}
			}()
			select {
			case <-paused:
			case <-time.After(3 * time.Second):
				t.Fatal("recovery did not reach its join attempt")
			}
			readUpdate := func() voiceChannelJoinOp {
				t.Helper()
				if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var update voiceChannelJoinOp
				if err := peer.ReadJSON(&update); err != nil {
					t.Fatal(err)
				}
				return update
			}
			requestDone = make(chan struct{})
			var requestErr error
			var joined *VoiceConnection
			go func() {
				defer close(requestDone)
				if changeChannel {
					requestErr = voice.ChangeChannel("new-channel", true, true)
				} else {
					joined, requestErr = session.ChannelVoiceJoin("guild", "new-channel", true, true)
				}
			}()
			update := readUpdate()
			if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "new-channel" || !update.Data.SelfMute || !update.Data.SelfDeaf {
				t.Fatalf("newer request = %+v, want new channel and settings", update)
			}
			// Model readiness after the newer public request's actual gateway write.
			voice.Lock()
			voice.Ready = true
			voice.Unlock()
			select {
			case <-requestDone:
			case <-time.After(3 * time.Second):
				t.Fatal("newer request did not finish")
			}
			if requestErr != nil || (!changeChannel && joined != voice) {
				t.Fatalf("newer request = %p, %v; want same handle and success", joined, requestErr)
			}
			voice.RLock()
			version := voice.joinVersion
			voice.RUnlock()
			close(release)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("obsolete recovery did not finish")
			}
			// Every recovery command precedes this marker; no reader scheduling
			// or manual cleanup can hide an unwanted retry.
			if err := session.ChannelVoiceJoinManual("marker", "marker", false, false); err != nil {
				t.Fatal(err)
			}
			update = readUpdate()
			if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "marker" || update.Data.ChannelID == nil || *update.Data.ChannelID != "marker" {
				channelID := "<nil>"
				if update.Data.ChannelID != nil {
					channelID = *update.Data.ChannelID
				}
				t.Errorf("obsolete recovery sent OP%d channel=%q mute=%t deaf=%t before the marker", update.Op, channelID, update.Data.SelfMute, update.Data.SelfDeaf)
			}
			voice.RLock()
			preserved := voice.ChannelID == "new-channel" && voice.mute && voice.deaf && voice.joinVersion == version && voice.Ready && !voice.reconnecting
			voice.RUnlock()
			if !preserved {
				t.Error("obsolete recovery overwrote newer voice settings")
			}
		})
	}
}

func TestVoiceQueuedRetryTeardownPreservesNewerJoin(t *testing.T) {
	testVoiceQueuedRetryTeardown(t, true)
}

func TestVoiceQueuedRetryTeardownClosesCurrentTransport(t *testing.T) {
	testVoiceQueuedRetryTeardown(t, false)
}

func testVoiceQueuedRetryTeardown(t *testing.T, newerJoin bool) {
	t.Helper()
	gateway, peer := newGatewayTestConnection(t)
	failedConn, _ := newGatewayTestConnection(t)
	replacement, _ := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn, session.DataReady = gateway, true
	voice := &VoiceConnection{LogLevel: LogInformational, GuildID: "guild", ChannelID: "channel", session: session}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var completed, pauseOnce atomic.Bool
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "successfully reconnected to channel %s" {
			completed.Store(true)
		}
		if format == "called" && completed.Load() && pauseOnce.CompareAndSwap(false, true) {
			close(paused)
			<-release
		}
	}
	defer func() { Logger = oldLogger }()
	go func() {
		defer close(done)
		voice.reconnectWithResume(false, nil, nil)
	}()
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
	}()
	readJoin := func() {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var update voiceChannelJoinOp
		if err := peer.ReadJSON(&update); err != nil {
			t.Fatal(err)
		}
		if update.Op != 4 || update.Data.GuildID == nil || *update.Data.GuildID != "guild" || update.Data.ChannelID == nil || *update.Data.ChannelID != "channel" {
			t.Fatalf("unexpected join: %+v", update)
		}
	}
	readJoin()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := udp.Close(); err != nil {
		t.Fatal(err)
	}
	failedClose, failedDone := make(chan struct{}), make(chan struct{})
	installFakeDAVE(voice, &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() { close(failedDone) }}, 1)
	voice.Lock()
	voice.wsConn, voice.udpConn, voice.close = failedConn, udp, failedClose
	voice.Unlock()
	// An actual failed keepalive queues recovery while the first join is waiting.
	voice.udpKeepAlive(udp, failedClose, time.Second)
	select {
	case <-failedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("UDP failure did not finish transport teardown")
	}
	voice.lifecycleMu.Lock()
	voice.RLock()
	queued := voice.reconnecting && voice.reconnectAgain && voice.close == nil
	voice.RUnlock()
	if !queued {
		voice.lifecycleMu.Unlock()
		t.Fatal("UDP failure did not queue recovery")
	}
	// Model handshake completion before the waiting recovery join returns.
	// Installation takes the real lifecycle lock, before the later pause.
	replacementClose := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
		// Stop additional attempts only after the unwanted teardown occurs.
		session.Lock()
		delete(session.VoiceConnections, "guild")
		session.Unlock()
	}}
	installFakeDAVE(voice, backend, 1)
	voice.Lock()
	voice.wsConn, voice.close, voice.Ready = replacement, replacementClose, true
	voice.Unlock()
	voice.lifecycleMu.Unlock()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("queued recovery did not reach its later teardown")
	}
	// Same-channel public joins can use this ready transport without installing
	// anything while the obsolete closer holds lifecycleMu.
	if newerJoin {
		joined, joinErr := session.ChannelVoiceJoin("guild", "channel", false, false)
		readJoin()
		if joinErr != nil || joined != voice {
			t.Fatalf("newer public join = %p, %v; want existing handle and success", joined, joinErr)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("obsolete recovery did not finish")
	}
	voice.RLock()
	preserved := voice.wsConn == replacement && voice.close == replacementClose && voice.Ready
	reconnecting := voice.reconnecting || voice.reconnectAgain
	voice.RUnlock()
	backend.mu.Lock()
	closed := backend.closed
	backend.mu.Unlock()
	if reconnecting {
		t.Error("recovery flags remained set after worker completion")
	}
	if newerJoin && (!preserved || closed != 0) {
		t.Errorf("queued teardown invalidated successful newer join: preserved=%t, backend closes=%d", preserved, closed)
	} else if !newerJoin && (preserved || closed != 1) {
		t.Errorf("current recovery did not tear down its transport: preserved=%t, backend closes=%d", preserved, closed)
	}
	select {
	case <-replacementClose:
		if newerJoin {
			t.Error("queued teardown closed the newer join's transport lifetime")
		}
	default:
		if !newerJoin {
			t.Error("current recovery left its transport lifetime open")
		}
	}
}

func TestVoiceOldSenderDoesNotChangeReplacementSpeaking(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "start speaking"
		flags := VoiceSpeakingFlagMicrophone
		if stop {
			name = "stop speaking"
			flags = 0
		}
		t.Run(name, func(t *testing.T) {
			oldWS, _ := newGatewayTestConnection(t)
			newWS, newPeer := newGatewayTestConnection(t)
			server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			oldUDP, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = oldUDP.Close() }()
			newUDP, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = newUDP.Close() }()
			aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
			if err != nil {
				t.Fatal(err)
			}
			oldClose := make(chan struct{})
			voice := &VoiceConnection{
				LogLevel: LogDebug, wsConn: oldWS, udpConn: oldUDP, close: oldClose,
				speaking: stop, Ready: true, aead: aead, op2: voiceOP2{SSRC: 42},
			}
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var pauseOnce sync.Once
			previousLogger := Logger
			Logger = func(_, _ int, format string, args ...interface{}) {
				if format == "called (%d)" && len(args) == 1 && args[0] == flags {
					pauseOnce.Do(func() {
						close(paused)
						<-release
					})
				}
			}
			defer func() {
				closeChannel(release)
				voice.Close()
				<-done
				Logger = previousLogger
			}()
			opus := make(chan []byte, 1)
			opus <- []byte{1, 2, 3}
			close(opus)
			go func() {
				voice.opusSender(oldUDP, oldClose, opus, 48000, 960)
				close(done)
			}()
			select {
			case <-paused:
			case <-time.After(2 * time.Second):
				t.Fatal("audio sender did not reach the speaking barrier")
			}
			if stop {
				// The stop notification follows one audio frame and five silence frames.
				if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				packet := make([]byte, 2048)
				for i := 0; i < 1+voiceOpusSilenceFrameCount; i++ {
					if _, _, err := server.ReadFromUDP(packet); err != nil {
						t.Fatalf("old sender packet %d: %v", i, err)
					}
				}
			}
			voice.Close()
			voice.Lock()
			voice.wsConn, voice.udpConn, voice.close = newWS, newUDP, make(chan struct{})
			voice.speaking, voice.Ready = stop, true
			voice.op2 = voiceOP2{SSRC: 99}
			voice.Unlock()
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old audio sender did not finish")
			}
			voice.RLock()
			speaking, ready := voice.speaking, voice.Ready
			voice.RUnlock()
			if speaking != stop || !ready {
				t.Errorf("old sender changed replacement: speaking=%t, ready=%t", speaking, ready)
			}
			if err := newPeer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			_, payload, err := newPeer.ReadMessage()
			if err == nil {
				t.Errorf("old sender wrote to replacement websocket: %s", payload)
			} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				t.Fatalf("replacement websocket failed unexpectedly: %v", err)
			}
		})
	}
}

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
			voice.dispatchVoiceMessage(voice.wsConn, voice.close, websocket.TextMessage, []byte(`{"op":5,"seq":1,"d":{"user_id":"remote","ssrc":55,"speaking":1}}`))
			voice.dispatchVoiceMessage(voice.wsConn, voice.close, websocket.TextMessage, []byte(`{"op":13,"seq":2,"d":{"user_id":"remote"}}`))
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
			voice.dispatchVoiceMessage(voice.wsConn, voice.close, websocket.TextMessage, []byte(`{"op":5,"seq":1,"d":{"user_id":"remote","ssrc":55,"speaking":1}}`))
			voice.dispatchVoiceMessage(voice.wsConn, voice.close, websocket.TextMessage, []byte(`{"op":13,"seq":2,"d":{"user_id":"remote"}}`))
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

type observedVoiceWriteConn struct {
	net.Conn
	entered chan struct{}
	once    sync.Once
}

func (c *observedVoiceWriteConn) Write(p []byte) (int, error) {
	if len(p) > 0 && (p[0] == 0x81 || p[0] == 0x82) {
		c.once.Do(func() { close(c.entered) })
	}
	return c.Conn.Write(p)
}

func TestVoiceSpeakingStallDoesNotBlockHeartbeat(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	handshake := make(chan error, 1)
	go func() {
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			handshake <- err
			return
		}
		_ = request.Body.Close()
		// Complete the standard WebSocket handshake, then leave the peer
		// open without reading frames. net.Pipe provides actual backpressure.
		accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(peer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
		handshake <- err
	}()
	transport := &observedVoiceWriteConn{Conn: client, entered: make(chan struct{})}
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second, NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		return transport, nil
	}}
	conn, _, err := dialer.Dial("ws://voice.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	voice := &VoiceConnection{LogLevel: -1, wsConn: conn, close: make(chan struct{})}
	speakingDone := make(chan struct{})
	var speakingErr error
	go func() {
		defer close(speakingDone)
		speakingErr = voice.Speaking(true)
	}()
	t.Cleanup(func() { _ = conn.Close(); <-speakingDone })
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("Speaking did not reach the transport write")
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		voice.wsHeartbeat(conn, voice.close, 10)
	}()
	t.Cleanup(func() { _ = conn.Close(); <-heartbeatDone })
	select {
	case <-speakingDone:
		var timeout net.Error
		if !errors.As(speakingErr, &timeout) || !timeout.Timeout() {
			t.Fatalf("Speaking error = %v, want a write timeout", speakingErr)
		}
	case <-time.After(2 * websocketWriteTimeout):
		t.Fatal("stalled Speaking retained the voice write lock without timing out")
	}
	select {
	case <-heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not finish after the failed speaking write")
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read error = %v, want EOF after heartbeat closes the transport", err)
	}
}

func TestVoiceSpeakingClearsWriteDeadline(t *testing.T) {
	voice, transport, conn := newVoiceHeartbeatWriteTestConnection(t, nil)
	if err := voice.Speaking(true); err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; deadline.IsZero() {
		t.Error("Speaking write had no deadline")
	}
	// A subsequent writer must not inherit the speaking deadline.
	if err := conn.WriteJSON(struct{}{}); err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; !deadline.IsZero() {
		t.Error("Speaking left its deadline on the next write")
	}
}

type speakingOrderBufferPool struct{ writes int }

func (*speakingOrderBufferPool) Get() interface{} { return nil }
func (p *speakingOrderBufferPool) Put(interface{}) {
	p.writes++
	if p.writes%128 != 0 {
		time.Sleep(2 * time.Millisecond)
	}
}

func TestVoiceSpeakingStateFollowsWriteOrder(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	messages := make(chan VoiceSpeakingFlags, 128)
	peerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(peerDone)
		defer close(messages)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Error(err)
			return
		}
		for {
			var message struct {
				Data struct {
					Speaking VoiceSpeakingFlags `json:"speaking"`
				} `json:"d"`
			}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			messages <- message.Data.Speaking
		}
	}))
	defer server.Close()
	// Repeated callers contend while buffer recycling is delayed. The last
	// write has no recycling delay, so it can finish before an older caller
	// resumes after handing off the websocket write lock.
	dialer := websocket.Dialer{WriteBufferPool: &speakingOrderBufferPool{}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); <-peerDone }()
	voice := &VoiceConnection{LogLevel: -1, wsConn: conn, op2: voiceOP2{SSRC: 42}}
	for round := 0; round < 4; round++ {
		start := make(chan struct{})
		results := make(chan error, 128)
		var workers sync.WaitGroup
		for i := 0; i < 32; i++ {
			workers.Add(1)
			go func(speaking bool) {
				defer workers.Done()
				<-start
				for range 4 {
					results <- voice.Speaking(speaking)
					speaking = !speaking
				}
			}(i%2 == 0)
		}
		close(start)
		workers.Wait()
		var last VoiceSpeakingFlags
		for range 128 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
			var ok bool
			if last, ok = <-messages; !ok {
				t.Fatal("peer closed before receiving all speaking updates")
			}
		}
		if voice.speaking != (last != 0) {
			t.Fatalf("round %d: local speaking=%t, last received speaking=%d", round, voice.speaking, last)
		}
	}
}

func TestVoiceDisconnectAfterTerminalClosePreservesNewJoin(t *testing.T) {
	testVoiceCommandAfterTerminalClose(t, func(voice *VoiceConnection) {
		if err := voice.Disconnect(); err != nil {
			t.Fatal(err)
		}
		voice.RLock()
		defer voice.RUnlock()
		if !voice.disconnecting || voice.sessionID != "" || voice.OpusSend != nil || voice.OpusRecv != nil {
			t.Error("old Disconnect did not finish its own cleanup")
		}
	})
}

func TestVoiceChangeChannelAfterTerminalClosePreservesNewJoin(t *testing.T) {
	testVoiceCommandAfterTerminalClose(t, func(voice *VoiceConnection) {
		if err := voice.ChangeChannel("stale-channel", true, true); !errors.Is(err, ErrWSNotFound) {
			t.Errorf("old ChangeChannel error = %v, want ErrWSNotFound", err)
		}
		voice.RLock()
		defer voice.RUnlock()
		if voice.ChannelID != "old-channel" || voice.mute || voice.deaf {
			t.Error("old ChangeChannel updated stale local state")
		}
	})
}

func TestVoiceChangeChannelRegisteredConnection(t *testing.T) {
	conn, peer := newGatewayTestConnection(t)
	session := &Session{LogLevel: -1, wsConn: conn}
	voice := &VoiceConnection{LogLevel: -1, session: session, GuildID: "guild", ChannelID: "old-channel"}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
	if err := voice.ChangeChannel("new-channel", true, true); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var request voiceChannelJoinOp
	if err := peer.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Op != 4 || request.Data.GuildID == nil || *request.Data.GuildID != "guild" || request.Data.ChannelID == nil || *request.Data.ChannelID != "new-channel" || !request.Data.SelfMute || !request.Data.SelfDeaf {
		t.Errorf("current ChangeChannel request = %+v", request)
	}
	if voice.ChannelID != "new-channel" || !voice.mute || !voice.deaf {
		t.Error("current ChangeChannel did not update local state")
	}
}

func testVoiceCommandAfterTerminalClose(t *testing.T, command func(*VoiceConnection)) {
	t.Helper()
	gateway, gatewayPeer := newGatewayTestConnection(t)
	oldSocket, oldPeer := newGatewayTestConnection(t)
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.wsConn = gateway
	oldVoice := &VoiceConnection{
		LogLevel: -1, GuildID: "guild", ChannelID: "old-channel", sessionID: "old-session",
		session: session, wsConn: oldSocket, close: make(chan struct{}),
		OpusSend: make(chan []byte), OpusRecv: make(chan *Packet),
	}
	session.VoiceConnections = map[string]*VoiceConnection{"guild": oldVoice}
	defer oldVoice.Close()
	defer func() { _ = session.Close() }()

	// A real terminal close unregisters the old handle without an application
	// Disconnect call. The application can still retain that handle.
	if err := oldPeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
		t.Fatal(err)
	}
	oldVoice.wsListen(oldSocket, oldVoice.close)
	if session.VoiceConnections["guild"] != nil {
		t.Fatal("terminal close did not unregister the old connection")
	}

	joined := make(chan struct{})
	go func() {
		defer close(joined)
		_, _ = session.ChannelVoiceJoin("guild", "new-channel", false, false)
	}()
	defer func() {
		_ = session.Close()
		<-joined
	}()
	if err := gatewayPeer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var request voiceChannelJoinOp
	if err := gatewayPeer.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "new-channel" {
		t.Fatalf("replacement join request = %+v", request)
	}
	session.RLock()
	replacement := session.VoiceConnections["guild"]
	session.RUnlock()
	if replacement == nil || replacement == oldVoice {
		t.Fatal("public join did not create a replacement")
	}
	command(oldVoice)
	// A subsequent public command marks the end of any stale writes. No
	// negative timing assertion or synthetic replacement installation is needed.
	if err := session.ChannelVoiceJoinManual("other-guild", "marker", false, false); err != nil {
		t.Fatal(err)
	}
	request = voiceChannelJoinOp{}
	if err := gatewayPeer.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Op != 4 || request.Data.GuildID == nil || *request.Data.GuildID != "other-guild" || request.Data.ChannelID == nil || *request.Data.ChannelID != "marker" {
		t.Errorf("old handle sent a voice command ahead of the marker: %+v", request)
	}
	session.RLock()
	registered := session.VoiceConnections["guild"]
	session.RUnlock()
	if registered != replacement {
		t.Error("old handle unregistered the replacement")
	}
}

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

type voiceResumeResultBarrier struct {
	net.Conn
	succeeded                bool
	active, entered, release chan struct{}
	once                     sync.Once
}

func (c *voiceResumeResultBarrier) pause() {
	select {
	case <-c.active:
		c.once.Do(func() { close(c.entered) })
		<-c.release
	default:
	}
}

func (c *voiceResumeResultBarrier) Read(p []byte) (n int, err error) {
	n, err = c.Conn.Read(p)
	if !c.succeeded {
		c.pause()
	}
	return
}

func (c *voiceResumeResultBarrier) SetReadDeadline(deadline time.Time) error {
	err := c.Conn.SetReadDeadline(deadline)
	if err == nil && c.succeeded && deadline.IsZero() {
		c.pause()
	}
	return err
}

func TestVoiceResumeResultDoesNotChangeReplacement(t *testing.T) {
	for _, outcome := range []string{"resumed", "failed", "opening"} {
		t.Run(outcome, func(t *testing.T) {
			barrier := &voiceResumeResultBarrier{succeeded: outcome == "resumed", active: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
			serverDone := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				var request voiceResumeOp
				if err := conn.ReadJSON(&request); err != nil {
					return
				}
				if request.Op != 7 {
					t.Errorf("received operation %d, want resume", request.Op)
					return
				}
				close(barrier.active)
				if barrier.succeeded {
					err = conn.WriteMessage(websocket.TextMessage, []byte(`{"op":9,"seq":7,"d":null}`))
				} else {
					err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4006, "invalid session"))
				}
				if err == nil {
					<-serverDone
				}
			}))
			defer server.Close()
			defer close(serverDone)
			tlsDialer := &tls.Dialer{Config: server.Client().Transport.(*http.Transport).TLSClientConfig}
			session := &Session{VoiceConnections: make(map[string]*VoiceConnection), Dialer: &websocket.Dialer{
				HandshakeTimeout: time.Second,
				NetDialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					conn, err := tlsDialer.DialContext(ctx, network, address)
					barrier.Conn = conn
					return barrier, err
				},
			}}
			udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
			if err != nil {
				t.Fatal(err)
			}
			voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", sessionID: "session", token: "dummy-token",
				endpoint: strings.TrimPrefix(server.URL, "https://"), session: session,
				udpConn: udp, aead: aead, close: make(chan struct{}),
			}
			session.VoiceConnections[voice.GuildID] = voice
			newConn, _ := newGatewayTestConnection(t)
			done := make(chan struct{})
			oldClose := voice.close
			go func() {
				voice.reconnectWithResume(true, nil, oldClose)
				close(done)
			}()
			defer func() {
				closeChannel(barrier.release)
				session.Lock()
				delete(session.VoiceConnections, voice.GuildID)
				session.Unlock()
				voice.Close()
				<-done
			}()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				t.Fatal("resume did not reach the result barrier")
			}
			voice.Close()
			// Stop the test's fallback loop if the buggy worker closes the new
			// backend. This hook runs only after the destructive action occurs.
			backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() {
				session.Lock()
				delete(session.VoiceConnections, voice.GuildID)
				session.Unlock()
			}}
			installFakeDAVE(voice, backend, 1)
			newClose := make(chan struct{})
			var opening *voiceOpenAttempt
			voice.Lock()
			voice.wsConn, voice.close = newConn, newClose
			voice.Ready, voice.speaking = true, true
			voice.sequence, voice.sequenceSet = 42, true
			if outcome == "opening" {
				opening = &voiceOpenAttempt{conn: newConn, canceled: make(chan struct{}), cancel: func() {}}
				voice.opening = opening
				voice.wsConn, voice.close = nil, nil
				voice.Ready, voice.speaking = false, false
			}
			voice.Unlock()
			close(barrier.release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old reconnect worker did not finish")
			}
			voice.RLock()
			preserved := voice.wsConn == newConn && voice.close == newClose && voice.Ready && voice.speaking && voice.sequence == 42
			if opening != nil {
				preserved = voice.opening == opening && voice.wsConn == nil && voice.close == nil
			}
			reconnecting := voice.reconnecting
			voice.RUnlock()
			if !preserved {
				t.Error("old resume result changed the replacement voice connection")
			}
			if reconnecting {
				t.Error("old reconnect worker left reconnecting set")
			}
			backend.mu.Lock()
			closed := backend.closed
			backend.mu.Unlock()
			if closed != 0 {
				t.Errorf("old resume failure closed the replacement DAVE backend %d times", closed)
			}
		})
	}
}

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

func TestVoiceStateAppliedBeforeCallbacks(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%v", gateway), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				session.SyncEvents = true
				session.State.User = &User{ID: "bot"}
				if err := session.State.GuildAdd(&Guild{ID: "guild"}); err != nil {
					t.Fatal(err)
				}
				voice := &VoiceConnection{GuildID: "guild", session: session}
				session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
				callbacks := 0
				session.AddHandler(func(s *Session, update *VoiceStateUpdate) {
					// Application handlers must still run outside the session lock.
					s.Lock()
					callbacks++
					s.Unlock()
					voice.RLock()
					defer voice.RUnlock()
					if voice.UserID != update.UserID || voice.ChannelID != update.ChannelID || voice.sessionID != update.SessionID {
						t.Errorf("callback saw voice state (%q, %q, %q), want (%q, %q, %q)", voice.UserID, voice.ChannelID, voice.sessionID, update.UserID, update.ChannelID, update.SessionID)
					}
				})
				for i := 0; i < 8; i++ {
					update := &VoiceStateUpdate{VoiceState: &VoiceState{
						GuildID: "guild", UserID: "bot", ChannelID: fmt.Sprint("channel-", i), SessionID: fmt.Sprint("session-", i),
					}}
					if gateway {
						message, err := json.Marshal(map[string]interface{}{"op": 0, "s": i + 1, "t": "VOICE_STATE_UPDATE", "d": update})
						if err != nil {
							t.Fatal(err)
						}
						if _, err := session.onEvent(websocket.TextMessage, message); err != nil {
							t.Fatal(err)
						}
					} else {
						session.handleEvent(voiceStateUpdateEventType, update)
					}
				}
				synctest.Wait()
				if callbacks != 8 {
					t.Errorf("callbacks = %d, want 8", callbacks)
				}
				if voice.ChannelID != "channel-7" || voice.sessionID != "session-7" {
					t.Errorf("voice state after all workers finished = (%q, %q), want latest dispatch", voice.ChannelID, voice.sessionID)
				}
			})
		})
	}
}

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

func TestVoiceTerminalCloseCancelsJoinWait(t *testing.T) {
	for _, beforeWait := range []bool{false, true} {
		name := "waiting"
		if beforeWait {
			name = "before wait initialization"
		}
		t.Run(name, func(t *testing.T) {
			gateway, gatewayPeer := newGatewayTestConnection(t)
			voiceSocket, voicePeer := newGatewayTestConnection(t)
			session, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.wsConn = gateway
			voice := &VoiceConnection{
				LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
				wsConn: voiceSocket, close: make(chan struct{}),
			}
			session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
			paused, release := make(chan struct{}), make(chan struct{})
			oldLogger := Logger
			if beforeWait {
				voice.LogLevel = LogInformational
				var once atomic.Bool
				Logger = func(_, _ int, format string, _ ...interface{}) {
					if format == "called" && once.CompareAndSwap(false, true) {
						close(paused)
						<-release
					}
				}
			}
			defer func() { Logger = oldLogger }()
			joined := make(chan struct{})
			var joinErr error
			go func() {
				defer close(joined)
				_, joinErr = session.ChannelVoiceJoin("guild", "channel", false, false)
			}()
			defer func() {
				closeChannel(release)
				_ = voice.Disconnect()
				_ = session.Close()
				<-joined
			}()
			if err := gatewayPeer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var request voiceChannelJoinOp
			if err := gatewayPeer.ReadJSON(&request); err != nil {
				t.Fatal(err)
			}
			if request.Op != 4 || request.Data.ChannelID == nil || *request.Data.ChannelID != "channel" {
				t.Fatalf("unexpected join request: %+v", request)
			}
			if beforeWait {
				select {
				case <-paused:
				case <-time.After(time.Second):
					t.Fatal("join did not reach the readiness wait")
				}
			} else {
				deadline := time.Now().Add(time.Second)
				for {
					voice.RLock()
					initialized := voice.disconnectCancel != nil
					voice.RUnlock()
					if initialized {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("join did not initialize cancellation")
					}
					time.Sleep(time.Millisecond)
				}
			}
			// A terminal voice close must end the pending public join, even
			// when the wait has not yet installed its cancellation channel.
			if err := voicePeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4022, "")); err != nil {
				t.Fatal(err)
			}
			voice.wsListen(voiceSocket, voice.close)
			close(release)
			select {
			case <-joined:
				if !errors.Is(joinErr, ErrWSNotFound) {
					t.Errorf("join error = %v, want ErrWSNotFound", joinErr)
				}
			case <-time.After(200 * time.Millisecond):
				t.Error("terminally closed voice left its public join waiting")
			}
		})
	}
}

func TestVoiceUDPDiscoveryFailureClosesWebsocket(t *testing.T) {
	oldTimeout := voiceUDPReadTimeout
	voiceUDPReadTimeout = 25 * time.Millisecond
	defer func() { voiceUDPReadTimeout = oldTimeout }()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = udp.Close() }()
	conn, peer := newGatewayTestConnection(t)
	voice := newUDPOpenTestVoice(conn, udp.LocalAddr().(*net.UDPAddr))
	voice.LogLevel = LogError
	closeChan := voice.close
	backendClosed := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() { close(backendClosed) }}
	installFakeDAVE(voice, backend, 1)
	failed := make(chan struct{})
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error opening udp connection, %s" {
			close(failed)
		}
	}
	defer func() { Logger = oldLogger }()
	listened := make(chan struct{})
	go func() {
		defer close(listened)
		voice.wsListen(conn, closeChan)
	}()
	defer func() {
		voice.Close()
		<-listened
		<-backendClosed
	}()

	ready := fmt.Sprintf(`{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":%d,"modes":["aead_aes256_gcm_rtpsize"]}}`, udp.LocalAddr().(*net.UDPAddr).Port)
	if err := peer.WriteMessage(websocket.TextMessage, []byte(ready)); err != nil {
		t.Fatal(err)
	}
	if err := udp.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 74)
	if n, _, err := udp.ReadFromUDP(packet); err != nil || n != len(packet) {
		t.Fatalf("discovery request = %d bytes, error %v", n, err)
	}
	// The local server deliberately withholds the discovery response.
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("discovery did not fail within its read deadline")
	}
	select {
	case <-listened:
	case <-time.After(200 * time.Millisecond):
		t.Error("failed discovery left the websocket listener running")
	}
	select {
	case <-backendClosed:
	case <-time.After(200 * time.Millisecond):
		t.Error("failed discovery retained the DAVE backend")
	}
	select {
	case <-closeChan:
	default:
		t.Error("failed discovery left the transport lifetime active")
	}
	voice.RLock()
	retained := voice.wsConn != nil || voice.udpConn != nil || voice.dave != nil || voice.Ready
	voice.RUnlock()
	if retained {
		t.Error("failed discovery retained transport or backend state")
	}
	// Observe actual socket closure before deferred fixture cleanup.
	if err := peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, err = peer.ReadMessage()
	if netErr, ok := err.(net.Error); err == nil || (ok && netErr.Timeout()) {
		t.Errorf("peer read = %v, want socket closure", err)
	}
}
