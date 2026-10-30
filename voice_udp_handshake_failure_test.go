package discordgo

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceUDPDiscoveryFailureClosesWebsocket(t *testing.T) {
	oldTimeout := voiceUDPReadTimeout
	voiceUDPReadTimeout = 25 * time.Millisecond
	defer func() { voiceUDPReadTimeout = oldTimeout }()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = udp.Close() }()
	conn, peer := newGatewayTestConnection(t)
	voice := newUDPOpenTestVoice(conn, udp.LocalAddr().(*net.UDPAddr))
	voice.LogLevel = LogError
	closeChan := voice.close
	backendClosed := make(chan struct{})
	backend := &fakeVoiceDAVESession{maxVersion: 1, closeHook: func() { close(backendClosed) }}
	installFakeDAVE(voice, backend, 1)
	failed := make(chan struct{})
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "error opening udp connection, %s" {
			close(failed)
		}
	}
	defer func() { Logger = oldLogger }()
	listened := make(chan struct{})
	go func() {
		defer close(listened)
		voice.wsListen(conn, closeChan)
	}()
	defer func() {
		voice.Close()
		<-listened
		<-backendClosed
	}()

	ready := fmt.Sprintf(`{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":%d,"modes":["aead_aes256_gcm_rtpsize"]}}`, udp.LocalAddr().(*net.UDPAddr).Port)
	if err := peer.WriteMessage(websocket.TextMessage, []byte(ready)); err != nil {
		t.Fatal(err)
	}
	if err := udp.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 74)
	if n, _, err := udp.ReadFromUDP(packet); err != nil || n != len(packet) {
		t.Fatalf("discovery request = %d bytes, error %v", n, err)
	}
	// The local server deliberately withholds the discovery response.
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("discovery did not fail within its read deadline")
	}
	select {
	case <-listened:
	case <-time.After(200 * time.Millisecond):
		t.Error("failed discovery left the websocket listener running")
	}
	select {
	case <-backendClosed:
	case <-time.After(200 * time.Millisecond):
		t.Error("failed discovery retained the DAVE backend")
	}
	select {
	case <-closeChan:
	default:
		t.Error("failed discovery left the transport lifetime active")
	}
	voice.RLock()
	retained := voice.wsConn != nil || voice.udpConn != nil || voice.dave != nil || voice.Ready
	voice.RUnlock()
	if retained {
		t.Error("failed discovery retained transport or backend state")
	}
	// Observe actual socket closure before deferred fixture cleanup.
	if err := peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, err = peer.ReadMessage()
	if netErr, ok := err.(net.Error); err == nil || (ok && netErr.Timeout()) {
		t.Errorf("peer read = %v, want socket closure", err)
	}
}
