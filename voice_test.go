package discordgo

import (
	"net"
	"testing"
)

func TestOpusSenderNilAEAD(t *testing.T) {
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP returned error: %v", err)
	}
	defer udpConn.Close()

	opus := make(chan []byte, 1)
	opus <- []byte{0x01, 0x02}
	close(opus)

	v := &VoiceConnection{LogLevel: -1}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("opusSender panicked with nil aead: %v", r)
		}
	}()

	v.opusSender(udpConn, make(chan struct{}), opus, 48000, 960)
}
