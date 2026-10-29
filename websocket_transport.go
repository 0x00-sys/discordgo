package discordgo

import (
	"context"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
)

// dialWebsocket cancels both dialing and the subsequent TLS/HTTP
// handshake. The pinned websocket dialer uses context for TCP dialing and
// deadlines, but does not close an established transport on cancellation.
func dialWebsocket(ctx context.Context, dialer *websocket.Dialer, endpoint string, header http.Header) (*websocket.Conn, error) {
	copyDialer := *dialer
	var stopClosing func() bool
	copyDialer.NetDialContext = func(dialCtx context.Context, network, address string) (conn net.Conn, err error) {
		switch {
		case dialer.NetDialContext != nil:
			conn, err = dialer.NetDialContext(dialCtx, network, address)
		case dialer.NetDial != nil:
			conn, err = dialer.NetDial(network, address)
		default:
			conn, err = (&net.Dialer{}).DialContext(dialCtx, network, address)
		}
		if err == nil {
			// Use the caller's context: the websocket dialer cancels its own
			// handshake-timeout context even when a connection succeeds.
			stopClosing = context.AfterFunc(ctx, func() { _ = conn.Close() })
		}
		return
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
