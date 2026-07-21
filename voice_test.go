package discordgo

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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
		speaking: true,
		aead:     aead,
		op2:      voiceOP2{SSRC: 42},
	}
	done := make(chan struct{})
	go func() {
		voice.opusSender(client, make(chan struct{}), opus, 48000, 960)
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

	if err = vc.udpOpen(); err != nil {
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
	err = v.udpOpen()
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
	defer func() {
		close(stopMutating)
		<-mutatorDone
	}()

	reconnectDone := make(chan struct{})
	go func() {
		voice.reconnectWithResume(false)
		close(reconnectDone)
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
		v.reconnect()
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
		sent, err := stale.sendDisconnectIfCurrent()
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		vc := &VoiceConnection{LogLevel: -1}
		vc.wsHeartbeat(&websocket.Conn{}, make(chan struct{}), 0)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wsHeartbeat did not return for invalid interval")
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
	enteredOnce sync.Once
	closedOnce  sync.Once
}

func (c *activatedWriteBlockingConn) Write(p []byte) (int, error) {
	select {
	case <-c.active:
		c.enteredOnce.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	default:
	}
	return c.Conn.Write(p)
}

func (c *activatedWriteBlockingConn) Close() error {
	if c.closed != nil {
		c.closedOnce.Do(func() { close(c.closed) })
	}
	return c.Conn.Close()
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
	vc.updateSequence([]byte(`{"seq":10}`))

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
	vc.updateSequence([]byte(`{"op":5,"seq":65535,"d":{}}`))
	vc.RLock()
	sequence, set := vc.sequence, vc.sequenceSet
	vc.RUnlock()
	if !set || sequence != 65535 {
		t.Fatalf("sequence = %d/%t, want 65535/true", sequence, set)
	}

	vc.updateSequence([]byte(`{"op":5,"d":{}}`))
	vc.RLock()
	sequence = vc.sequence
	vc.RUnlock()
	if sequence != 65535 {
		t.Fatalf("sequence changed without seq: %d", sequence)
	}

	vc.updateSequence([]byte(`{"op":5,"seq":0,"d":{}}`))
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
