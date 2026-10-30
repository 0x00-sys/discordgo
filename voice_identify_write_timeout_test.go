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
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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
