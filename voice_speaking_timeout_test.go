package discordgo

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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
