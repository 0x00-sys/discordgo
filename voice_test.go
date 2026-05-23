package discordgo

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceReadyConcurrentClose(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v", err)
	}
	defer udpConn.Close()

	udpDone := make(chan struct{})
	go func() {
		defer close(udpDone)
		buf := make([]byte, 74)
		for {
			n, addr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n != 74 {
				continue
			}
			resp := make([]byte, 74)
			copy(resp[8:], []byte("127.0.0.1"))
			binary.BigEndian.PutUint16(resp[72:], uint16(udpConn.LocalAddr().(*net.UDPAddr).Port))
			if _, err = udpConn.WriteToUDP(resp, addr); err != nil {
				return
			}
		}
	}()

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		for {
			if _, _, err = c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	udpAddr := udpConn.LocalAddr().(*net.UDPAddr)
	ready := []byte(fmt.Sprintf(`{"op":2,"d":{"ssrc":1,"ip":%q,"port":%d,"modes":["aead_aes256_gcm_rtpsize"],"heartbeat_interval":1}}`, udpAddr.IP.String(), udpAddr.Port))

	vc := &VoiceConnection{
		LogLevel: -1,
		close:    make(chan struct{}),
		endpoint: "voice.example.com",
		wsConn:   conn,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		vc.onEvent(ready)
	}()

	time.Sleep(time.Millisecond)
	vc.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("voice READY handling did not finish after Close")
	}
}
