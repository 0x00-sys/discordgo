package discordgo

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebsocketMasksDoNotFollowMathRand(t *testing.T) {
	// Applications can still opt into legacy global PRNG seeding. Wire masks
	// must remain independent of this predictable, non-cryptographic source.
	t.Setenv("GODEBUG", "randseednop=0")
	conn, peer := newGatewayTestConnection(t)
	if err := peer.UnderlyingConn().SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	rand.Seed(42) //nolint:staticcheck // Deliberately exercise legacy predictable global randomness.
	expected := rand.New(rand.NewSource(42))
	matches := 0
	for i := 0; i < 4; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, []byte("mask-test")); err != nil {
			t.Fatal(err)
		}
		frame := make([]byte, 6+len("mask-test"))
		if _, err := io.ReadFull(peer.UnderlyingConn(), frame); err != nil {
			t.Fatal(err)
		}
		if frame[1]&0x80 == 0 {
			t.Fatal("client frame is not masked")
		}
		if binary.LittleEndian.Uint32(frame[2:6]) == expected.Uint32() {
			matches++
		}
	}
	if matches == 4 {
		t.Fatal("all four WebSocket masks were predictable from math/rand seed 42")
	}
}

// newIdleWebsocketServer accepts websocket connections and reads from them
// until they close.
func newIdleWebsocketServer(t testing.TB) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

// websocketWriteBufferLen reads gorilla's private write buffer. The test is
// skipped if a gorilla update renames the field.
func websocketWriteBufferLen(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	field := reflect.ValueOf(conn).Elem().FieldByName("writeBuf")
	if !field.IsValid() || field.Kind() != reflect.Slice {
		t.Skip("gorilla/websocket no longer has a writeBuf []byte field")
	}
	return field.Len()
}

func TestDialWebsocketIdleConnectionHoldsNoWriteBuffer(t *testing.T) {
	endpoint := newIdleWebsocketServer(t)
	conn, err := dialWebsocket(context.Background(), websocket.DefaultDialer, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.WriteJSON(heartbeatOp{1, 42}); err != nil {
		t.Fatal(err)
	}

	// Gateway and voice connections idle between infrequent heartbeats.
	// Keeping a 4 KiB write buffer per idle connection costs about 40 MB
	// for 10,000 sessions in one process.
	if n := websocketWriteBufferLen(t, conn); n != 0 {
		t.Fatalf("idle connection holds a %d byte write buffer", n)
	}
}

// A dialer that sets its own write buffer size or pool keeps it, and the
// caller's dialer is never modified.
func TestDialWebsocketKeepsCustomWriteBufferSettings(t *testing.T) {
	endpoint := newIdleWebsocketServer(t)
	dialer := &websocket.Dialer{WriteBufferSize: 8192}
	conn, err := dialWebsocket(context.Background(), dialer, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.WriteJSON(heartbeatOp{1, 42}); err != nil {
		t.Fatal(err)
	}
	if n := websocketWriteBufferLen(t, conn); n != 8192+14 {
		t.Fatalf("custom WriteBufferSize connection holds a %d byte write buffer, want %d", n, 8192+14)
	}
	if dialer.WriteBufferPool != nil {
		t.Fatal("dialWebsocket modified the caller's dialer")
	}

	pool := &countingBufferPool{}
	conn, err = dialWebsocket(context.Background(), &websocket.Dialer{WriteBufferPool: pool}, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.WriteJSON(heartbeatOp{1, 42}); err != nil {
		t.Fatal(err)
	}
	if pool.gets.Load() == 0 {
		t.Fatal("custom WriteBufferPool was not used")
	}
}

type countingBufferPool struct {
	sync.Pool
	gets atomic.Int32
}

func (p *countingBufferPool) Get() interface{} {
	p.gets.Add(1)
	return p.Pool.Get()
}

// pipeListener serves in-memory connections, so benchmarks that open many
// connections do not run out of local ports.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "pipe"} }

func (l *pipeListener) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// BenchmarkDialWebsocketIdleHeap reports the live heap per idle connection
// after one heartbeat has been written on each. The server side of every
// connection is in the same process and counted too, so compare the
// heap-B/conn metric between versions rather than reading it as the client
// cost alone.
func BenchmarkDialWebsocketIdleHeap(b *testing.B) {
	const conns = 200
	listener := newPipeListener()
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()
	// Default buffer settings, as in websocket.DefaultDialer.
	dialer := &websocket.Dialer{NetDialContext: listener.DialContext}

	var perConn float64
	for b.Loop() {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		open := make([]*websocket.Conn, 0, conns)
		for range conns {
			conn, err := dialWebsocket(context.Background(), dialer, "ws://pipe/", nil)
			if err != nil {
				b.Fatal(err)
			}
			if err := conn.WriteJSON(heartbeatOp{1, 42}); err != nil {
				b.Fatal(err)
			}
			open = append(open, conn)
		}
		// Let the server read the heartbeats and settle.
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		runtime.ReadMemStats(&after)
		perConn += (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / conns
		runtime.KeepAlive(open)
		for _, conn := range open {
			_ = conn.Close()
		}
		// Let the server side connections finish before the next round.
		time.Sleep(50 * time.Millisecond)
	}
	b.ReportMetric(perConn/float64(b.N), "heap-B/conn")
}

func TestGatewayOpenHandlesProxyStatusWithoutReason(t *testing.T) {
	// Isolate the original fatal unlock during panic unwinding in Open.
	if os.Getenv("DISCORDGO_PROXY_STATUS_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestGatewayOpenHandlesProxyStatusWithoutReason$", "-test.timeout=5s")
		cmd.Env = append(os.Environ(), "DISCORDGO_PROXY_STATUS_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Open crashed on a proxy error response: %v\n%s", err, output)
		}
		return
	}

	client, peer := net.Pipe()
	if err := peer.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	peerDone := make(chan error, 1)
	peerExited := make(chan struct{})
	defer func() {
		_ = client.Close()
		_ = peer.Close()
		<-peerExited
	}()
	go func() {
		defer close(peerExited)
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			peerDone <- err
			return
		}
		_ = request.Body.Close()
		if request.Method != http.MethodConnect || request.Host != "gateway.invalid:443" {
			peerDone <- fmt.Errorf("unexpected proxy request: %s %s", request.Method, request.Host)
			return
		}
		if _, err = io.WriteString(peer, "HTTP/1.1 407\r\nContent-Length: 0\r\n\r\n"); err != nil {
			peerDone <- err
			return
		}
		_, err = peer.Read(make([]byte, 1))
		if !errors.Is(err, io.EOF) {
			peerDone <- fmt.Errorf("proxy connection not closed after rejection: %v", err)
			return
		}
		peerDone <- nil
	}()

	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = -1
	session.gateway = "wss://gateway.invalid/"
	session.Dialer = &websocket.Dialer{
		HandshakeTimeout: time.Second,
		Proxy:            http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.invalid:8080"}),
		NetDialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "proxy.invalid:8080" {
				t.Errorf("dial address = %q, want proxy", address)
			}
			return client, nil
		},
	}
	if err := session.Open(); err == nil {
		t.Fatal("Open accepted a rejected proxy connection")
	}
	if session.wsConn != nil || session.gatewayOpen != nil {
		t.Fatal("failed Open retained connection or startup state")
	}
	if err := <-peerDone; err != nil {
		t.Fatal(err)
	}
}

func TestWebsocketHTTPProxyPreservesSuccessfulTunnel(t *testing.T) {
	for _, status := range []string{"200 Connection established", "200"} {
		for _, customTLS := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/customTLS=%t", status, customTLS), func(t *testing.T) {
				client, peer := net.Pipe()
				if err := peer.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Fatal(err)
				}
				peerDone := make(chan struct{})
				defer func() {
					_ = client.Close()
					_ = peer.Close()
					<-peerDone
				}()
				sendFrame := make(chan struct{})
				var peerErr error
				go func() {
					defer close(peerDone)
					peerErr = func() error {
						reader := bufio.NewReader(peer)
						request, err := http.ReadRequest(reader)
						if err != nil {
							return err
						}
						_ = request.Body.Close()
						if request.Method != http.MethodConnect || request.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
							return errors.New("invalid proxy CONNECT method or authentication")
						}
						if _, err = fmt.Fprintf(peer, "HTTP/1.1 %s\r\n\r\n", status); err != nil {
							return err
						}
						request, err = http.ReadRequest(reader)
						if err != nil {
							return err
						}
						_ = request.Body.Close()
						accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
						if _, err = fmt.Fprintf(peer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:])); err != nil {
							return err
						}
						select {
						case <-sendFrame:
						case <-time.After(2 * time.Second):
							return errors.New("client did not finish proxy setup")
						}
						_, err = peer.Write([]byte{0x81, 5, 'h', 'e', 'l', 'l', 'o'})
						return err
					}()
				}()
				dial := func(_ context.Context, _, address string) (net.Conn, error) {
					if address != "proxy.invalid:80" {
						t.Errorf("dial address = %q, want proxy's default port", address)
					}
					return client, nil
				}
				dialer := &websocket.Dialer{
					HandshakeTimeout: time.Second,
					Proxy:            http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.invalid", User: url.UserPassword("user", "pass")}),
					NetDialContext:   dial,
				}
				endpoint := "ws://gateway.invalid/"
				if customTLS {
					// Exercise the custom transport hook without adding TLS to the pipe.
					dialer.NetDialTLSContext = dial
					endpoint = "wss://gateway.invalid/"
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				conn, err := dialWebsocket(ctx, dialer, endpoint, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.Close() }()
				cancel()
				close(sendFrame)
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				kind, message, err := conn.ReadMessage()
				if err != nil || kind != websocket.TextMessage || string(message) != "hello" {
					t.Fatalf("tunnel read = %d %q, %v", kind, message, err)
				}
				<-peerDone
				if peerErr != nil {
					t.Fatal(peerErr)
				}
			})
		}
	}
}
