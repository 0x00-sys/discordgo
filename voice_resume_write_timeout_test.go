package discordgo

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceResumeStalledWriteTimesOut(t *testing.T) {
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
		// Keep the peer open without reading the resume packet.
	}()
	transport := &observedVoiceWriteConn{Conn: client, entered: make(chan struct{})}
	dialer := &websocket.Dialer{HandshakeTimeout: time.Second, NetDialTLSContext: func(context.Context, string, string) (net.Conn, error) {
		// This fixture supplies an established transport, without real TLS.
		return transport, nil
	}}
	voice := &VoiceConnection{
		LogLevel: -1, GuildID: "guild", sessionID: "session", token: "dummy-token",
		endpoint: "voice.invalid", close: make(chan struct{}), session: &Session{Dialer: dialer},
	}
	done := make(chan struct{})
	var resumeErr error
	go func() {
		defer close(done)
		resumeErr = voice.resumeVoice()
	}()
	t.Cleanup(func() { voice.Close(); _ = client.Close(); <-done })
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not reach its transport write")
	}
	select {
	case <-done:
		var timeout net.Error
		if !errors.As(resumeErr, &timeout) || !timeout.Timeout() {
			t.Fatalf("resume error = %v, want a write timeout", resumeErr)
		}
	case <-time.After(2 * websocketWriteTimeout):
		t.Fatal("stalled resume write did not time out")
	}
	if voice.wsConn != nil {
		t.Error("failed resume installed a websocket")
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read error = %v, want EOF after resume cleanup", err)
	}
}

func TestVoiceResumeClearsWriteDeadline(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var resume voiceResumeOp
		if err := conn.ReadJSON(&resume); err != nil {
			return
		}
		if resume.Op != 7 {
			t.Errorf("resume opcode = %d, want 7", resume.Op)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":9,"d":null}`)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
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
	voice := &VoiceConnection{
		LogLevel: -1, GuildID: "guild", sessionID: "session", token: "dummy-token",
		endpoint: strings.TrimPrefix(server.URL, "https://"), close: make(chan struct{}), session: &Session{Dialer: dialer},
	}
	defer voice.Close()
	if err := voice.resumeVoice(); err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; deadline.IsZero() {
		t.Error("resume write had no deadline")
	}
	voice.RLock()
	conn := voice.wsConn
	voice.RUnlock()
	if conn == nil {
		t.Fatal("successful resume did not install a websocket")
	}
	voice.wsMutex.Lock()
	err := conn.WriteJSON(struct{}{})
	voice.wsMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if deadline := <-transport.deadlines; !deadline.IsZero() {
		t.Error("resume left its deadline on the next write")
	}
}
