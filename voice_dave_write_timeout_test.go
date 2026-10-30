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
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var daveCallbackWriteTests = []struct {
	name string
	send func(VoiceDAVECallbacks) error
}{
	{"json", func(c VoiceDAVECallbacks) error { return c.SendReadyForTransition(1) }},
	{"binary", func(c VoiceDAVECallbacks) error { return c.SendMLSKeyPackage([]byte{1, 2}) }},
}

func TestVoiceDAVECallbackStallDoesNotBlockHeartbeat(t *testing.T) {
	for _, tc := range daveCallbackWriteTests {
		t.Run(tc.name, func(t *testing.T) {
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
				accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				_, err = fmt.Fprintf(peer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
				handshake <- err
				// Leave the peer open without reading callback frames.
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
			callbacks := &voiceDAVECallbacks{voice: voice}
			voice.Lock()
			voice.installVoiceDAVESessionLocked(&fakeVoiceDAVESession{maxVersion: 1}, callbacks, 1)
			voice.Unlock()
			t.Cleanup(voice.Close)
			callbackDone := make(chan struct{})
			var callbackErr error
			go func() {
				defer close(callbackDone)
				callbackErr = tc.send(callbacks)
			}()
			t.Cleanup(func() { _ = conn.Close(); <-callbackDone })
			select {
			case <-transport.entered:
			case <-time.After(time.Second):
				t.Fatal("DAVE callback did not reach its transport write")
			}
			heartbeatDone := make(chan struct{})
			go func() {
				defer close(heartbeatDone)
				voice.wsHeartbeat(conn, voice.close, 10)
			}()
			t.Cleanup(func() { _ = conn.Close(); <-heartbeatDone })
			select {
			case <-callbackDone:
				var timeout net.Error
				if !errors.As(callbackErr, &timeout) || !timeout.Timeout() {
					t.Fatalf("callback error = %v, want a write timeout", callbackErr)
				}
			case <-time.After(2 * websocketWriteTimeout):
				t.Fatal("stalled DAVE callback retained the voice write lock without timing out")
			}
			select {
			case <-heartbeatDone:
			case <-time.After(time.Second):
				t.Fatal("heartbeat did not finish after the failed callback write")
			}
			if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatalf("peer read error = %v, want EOF after heartbeat closes the transport", err)
			}
		})
	}
}

func TestVoiceDAVECallbacksClearWriteDeadline(t *testing.T) {
	for _, tc := range daveCallbackWriteTests {
		t.Run(tc.name, func(t *testing.T) {
			voice, transport, conn := newVoiceHeartbeatWriteTestConnection(t, nil)
			callbacks := &voiceDAVECallbacks{voice: voice}
			voice.Lock()
			voice.installVoiceDAVESessionLocked(&fakeVoiceDAVESession{maxVersion: 1}, callbacks, 1)
			voice.Unlock()
			t.Cleanup(voice.closeVoiceDAVE)
			if err := tc.send(callbacks); err != nil {
				t.Fatal(err)
			}
			if deadline := <-transport.deadlines; deadline.IsZero() {
				t.Error("DAVE callback write had no deadline")
			}
			if err := conn.WriteJSON(struct{}{}); err != nil {
				t.Fatal(err)
			}
			if deadline := <-transport.deadlines; !deadline.IsZero() {
				t.Error("DAVE callback left its deadline on the next write")
			}
		})
	}
}
