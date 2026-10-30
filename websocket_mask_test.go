package discordgo

import (
	"encoding/binary"
	"io"
	"math/rand"
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
