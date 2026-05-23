package discordgo

import (
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUDPOpenReadTimeout(t *testing.T) {
	oldTimeout := voiceUDPReadTimeout
	voiceUDPReadTimeout = 25 * time.Millisecond
	defer func() {
		voiceUDPReadTimeout = oldTimeout
	}()

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("error listening on udp: %v", err)
	}
	defer server.Close()

	addr := server.LocalAddr().(*net.UDPAddr)
	v := &VoiceConnection{
		wsConn:   &websocket.Conn{},
		close:    make(chan struct{}),
		endpoint: "127.0.0.1",
		op2: voiceOP2{
			IP:   "127.0.0.1",
			Port: addr.Port,
			SSRC: 1,
		},
	}
	defer func() {
		if v.udpConn != nil {
			v.udpConn.Close()
		}
	}()

	start := time.Now()
	err = v.udpOpen()
	if err == nil {
		t.Fatal("expected udpOpen to time out waiting for discovery response")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("udpOpen took too long to return: %s", elapsed)
	}

	lockAcquired := make(chan struct{})
	go func() {
		v.RLock()
		v.RUnlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("udpOpen returned without releasing the voice lock")
	}
}
