package discordgo

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestGatewayRecoveryKeepsOriginalLifetime(t *testing.T) {
	for _, source := range []string{"read_error", "heartbeat", "reconnect_event", "invalid_session"} {
		for _, reopen := range []bool{false, true} {
			name := "current"
			if reopen {
				name = "close_then_failed_open"
			}
			t.Run(source+"/"+name, func(t *testing.T) {
				conn, peer := newGatewayTestConnection(t)
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				session.SyncEvents = true
				session.wsConn = conn
				session.listening = make(chan interface{})
				session.gateway = "ws://discord.invalid/gateway"
				originalCancel := session.reconnectCancelSignal()
				session.Client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{"url":"ws://discord.invalid/gateway"}`))}, nil
				})}
				attempted := make(chan struct{}, 4)
				var attempts atomic.Int32
				dialErr := errors.New("test dial failure")
				session.Dialer = &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
					attempts.Add(1)
					attempted <- struct{}{}
					return nil, dialErr
				}}
				callbackDone := make(chan struct{})
				var handled atomic.Bool
				session.AddHandler(func(s *Session, _ *Disconnect) {
					if !handled.CompareAndSwap(false, true) {
						return
					}
					defer close(callbackDone)
					if reopen {
						if err := s.Close(); err != nil {
							t.Errorf("Close: %v", err)
						}
						if err := s.Open(); !errors.Is(err, dialErr) {
							t.Errorf("explicit Open error = %v, want dial failure", err)
						}
					}
				})
				done := make(chan struct{})
				defer func() {
					_ = session.Close()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("gateway recovery worker did not stop during cleanup")
					}
				}()
				listening := session.listening
				// An outstanding heartbeat forces the real missed-ACK recovery path.
				session.lastHeartbeatSent = time.Now()
				go func() {
					defer close(done)
					if source == "heartbeat" {
						session.heartbeat(conn, listening, 1)
					} else {
						session.listen(conn, listening)
					}
				}()
				switch source {
				case "read_error":
					_ = peer.Close()
				case "reconnect_event", "invalid_session":
					payload := `{"op":7,"d":null}`
					if source == "invalid_session" {
						payload = `{"op":9,"d":false}`
					}
					if err := peer.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-callbackDone:
				case <-time.After(time.Second):
					t.Fatal("Disconnect callback did not finish")
				}
				select {
				case <-attempted:
				case <-time.After(time.Second):
					t.Fatal("expected Open attempt did not reach dialer")
				}
				if reopen {
					if !reconnectCanceled(originalCancel) {
						t.Error("Close did not cancel the original gateway lifetime")
					}
					select {
					case <-done:
					case <-attempted:
						t.Fatal("old gateway recovery dialed again after Close and failed explicit Open")
					case <-time.After(time.Second):
						t.Fatal("old gateway recovery did not return")
					}
					if reconnectCanceled(session.reconnectCancelSignal()) {
						t.Error("old gateway recovery canceled the explicit Open lifetime")
					}
				}
				if got := attempts.Load(); got != 1 {
					t.Errorf("dial attempts = %d, want 1", got)
				}
			})
		}
	}
}
