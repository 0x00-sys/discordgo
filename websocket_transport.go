package discordgo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// dialWebsocket cancels both dialing and the subsequent TLS/HTTP
// handshake. The pinned websocket dialer uses context for TCP dialing and
// deadlines, but does not close an established transport on cancellation.
func dialWebsocket(ctx context.Context, dialer *websocket.Dialer, endpoint string, header http.Header) (*websocket.Conn, error) {
	copyDialer := *dialer
	var httpProxy bool
	if dialer.Proxy != nil {
		copyDialer.Proxy = func(req *http.Request) (*url.URL, error) {
			proxyURL, err := dialer.Proxy(req)
			httpProxy = proxyURL != nil && proxyURL.Scheme == "http"
			return proxyURL, err
		}
	}
	var stopClosing func() bool
	closeOnCancel := func(conn net.Conn, err error) (net.Conn, error) {
		if err == nil {
			// Use the caller's context: the websocket dialer cancels its own
			// handshake-timeout context even when a connection succeeds.
			stopClosing = context.AfterFunc(ctx, func() { _ = conn.Close() })
			if httpProxy {
				return &websocketProxyConn{Conn: conn}, nil
			}
		}
		return conn, err
	}
	copyDialer.NetDialContext = func(dialCtx context.Context, network, address string) (conn net.Conn, err error) {
		switch {
		case dialer.NetDialContext != nil:
			conn, err = dialer.NetDialContext(dialCtx, network, address)
		case dialer.NetDial != nil:
			conn, err = dialer.NetDial(network, address)
		default:
			conn, err = (&net.Dialer{}).DialContext(dialCtx, network, address)
		}
		return closeOnCancel(conn, err)
	}
	if dialer.NetDialTLSContext != nil {
		copyDialer.NetDialTLSContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
			return closeOnCancel(dialer.NetDialTLSContext(dialCtx, network, address))
		}
	}
	conn, _, err := copyDialer.DialContext(ctx, endpoint, header)
	if stopClosing != nil {
		stopClosing()
	}
	if ctx.Err() != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, ctx.Err()
	}
	return conn, err
}

// Gorilla indexes the reason phrase of rejected HTTP CONNECT responses without
// checking that it exists. Go's HTTP parser accepts a bare status code, so reject
// that error response before it reaches Gorilla. Successful tunnels pass through.
type websocketProxyConn struct {
	net.Conn
	readMu sync.Mutex
	reader io.Reader
}

func (c *websocketProxyConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.reader == nil {
		reader := bufio.NewReader(c.Conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		_, status, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		status = strings.TrimLeft(status, " ")
		if status != "200" && !strings.Contains(status, " ") {
			return 0, fmt.Errorf("invalid HTTP proxy response status: %q", status)
		}
		c.reader = io.MultiReader(strings.NewReader(line), reader)
	}
	return c.reader.Read(p)
}
