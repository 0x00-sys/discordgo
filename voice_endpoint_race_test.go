package discordgo

import (
	"net"
	"runtime"
	"testing"
)

func TestVoiceErrorLoggingConcurrentEndpointUpdate(t *testing.T) {
	for _, path := range []string{"heartbeat", "websocket", "udp"} {
		t.Run(path, func(t *testing.T) {
			conn, _ := newGatewayTestConnection(t)
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			if err := udp.Close(); err != nil {
				t.Fatal(err)
			}
			signal := make(chan struct{})
			voice := &VoiceConnection{LogLevel: -1, wsConn: conn, udpConn: udp, close: signal, reconnecting: true}
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					voice.Lock()
					voice.endpoint = "replacement.voice.example"
					voice.Unlock()
					runtime.Gosched()
				}
			}()
			for i := 0; i < 100; i++ {
				switch path {
				case "heartbeat":
					voice.wsHeartbeat(conn, signal, 1)
				case "websocket":
					voice.Lock()
					voice.wsConn = conn
					voice.Unlock()
					voice.wsListen(conn, signal)
				case "udp":
					// A stale generation must not launch recovery on this connection.
					voice.opusReceiver(udp, make(chan struct{}), nil)
				}
			}
			close(stop)
			<-done
		})
	}
}
