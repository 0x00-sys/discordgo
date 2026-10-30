package discordgo

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestGatewayOpenCanceledDuringNetworkSetup(t *testing.T) {
	for _, stage := range []string{"lookup", "dial", "TLS", "custom TLS", "HTTP"} {
		t.Run(stage, func(t *testing.T) {
			session, err := New("Bot dummy-token")
			if err != nil {
				t.Fatal(err)
			}
			session.LogLevel = -1
			session.gateway = "wss://gateway.invalid/"
			session.Dialer = &websocket.Dialer{}
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			canceled := func(ctx context.Context) error {
				close(entered)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
					return errors.New("test released stalled operation")
				}
			}
			switch stage {
			case "lookup":
				session.gateway = ""
				session.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					return nil, canceled(req.Context())
				})}
			case "dial":
				session.Dialer.NetDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
					return nil, canceled(ctx)
				}
			case "TLS":
				client, peer := net.Pipe()
				defer func() { _ = client.Close() }()
				defer func() { _ = peer.Close() }()
				session.Dialer.NetDialContext = func(context.Context, string, string) (net.Conn, error) {
					close(entered)
					return client, nil
				}
			case "custom TLS":
				client, peer := net.Pipe()
				defer func() { _ = client.Close() }()
				defer func() { _ = peer.Close() }()
				session.Dialer.NetDialTLSContext = func(context.Context, string, string) (net.Conn, error) {
					close(entered)
					return client, nil
				}
			case "HTTP":
				server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
					_ = canceled(req.Context())
				}))
				// CloseClientConnections also makes failure-path cleanup bounded.
				defer server.Close()
				defer server.CloseClientConnections()
				session.gateway = "ws" + strings.TrimPrefix(server.URL, "http")
			}
			done := make(chan error, 1)
			go func() { done <- session.Open() }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("Open did not reach network setup")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled Open succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("Close left Open blocked in " + stage)
			}
			if session.wsConn != nil {
				t.Fatal("canceled Open installed a socket")
			}
		})
	}
}
