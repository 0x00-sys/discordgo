package discordgo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceHandshakeCanceledOnClose(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, phase := range []string{"TLS", "HTTP"} {
			t.Run(fmt.Sprintf("resume=%v/%s", resume, phase), func(t *testing.T) {
				entered := make(chan struct{})
				dialer := &websocket.Dialer{}
				endpoint := "voice.invalid"
				if phase == "TLS" {
					client, peer := net.Pipe()
					defer func() { _ = client.Close() }()
					defer func() { _ = peer.Close() }()
					dialer.NetDialContext = func(context.Context, string, string) (net.Conn, error) {
						close(entered)
						return client, nil
					}
				} else {
					release := make(chan struct{})
					server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
						close(entered)
						select {
						case <-release:
						case <-r.Context().Done():
						}
					}))
					defer server.Close()
					defer close(release)
					endpoint = strings.TrimPrefix(server.URL, "https://")
					dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
				}
				voice := &VoiceConnection{
					GuildID: "guild", ChannelID: "1", UserID: "user",
					sessionID: "session", token: "dummy-token", endpoint: endpoint,
					close: make(chan struct{}), LogLevel: -1, session: &Session{Dialer: dialer},
				}
				done := make(chan error, 1)
				go func() {
					if resume {
						done <- voice.resumeVoice()
					} else {
						done <- voice.open()
					}
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("handshake did not begin")
				}
				voice.Close()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("canceled handshake succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("Close left the handshake running")
				}
				if voice.wsConn != nil {
					t.Fatal("canceled handshake installed a connection")
				}
			})
		}
	}
}

func TestVoiceResumeDialCanceledOnClose(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	voice := &VoiceConnection{
		GuildID: "guild", sessionID: "session", token: "dummy-token",
		endpoint: "voice.invalid", close: make(chan struct{}), LogLevel: -1,
		session: &Session{Dialer: &websocket.Dialer{
			NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				close(entered)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return nil, errors.New("test released stalled dial")
				}
			},
		}},
	}
	done := make(chan error, 1)
	go func() { done <- voice.resumeVoice() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not enter local dial stub")
	}
	voice.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("resume error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left the voice resume dial running")
	}
}

func TestVoiceDialSuccessfulConnectionSurvivesContextCleanup(t *testing.T) {
	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		peers <- conn
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := dialVoiceWebsocket(ctx, &websocket.Dialer{HandshakeTimeout: time.Second}, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	peer := <-peers
	defer func() { _ = peer.Close() }()
	// Both the dialer's internal timeout context and our caller's context
	// are cleaned up once dialing returns. Neither owns the live socket.
	cancel()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("still connected")); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, message, err := peer.ReadMessage()
	if err != nil || string(message) != "still connected" {
		t.Fatalf("live socket after cleanup: message=%q, err=%v", message, err)
	}
}
