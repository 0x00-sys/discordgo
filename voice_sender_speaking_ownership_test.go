package discordgo

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestVoiceOldSenderDoesNotChangeReplacementSpeaking(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "start speaking"
		flags := VoiceSpeakingFlagMicrophone
		if stop {
			name = "stop speaking"
			flags = 0
		}
		t.Run(name, func(t *testing.T) {
			oldWS, _ := newGatewayTestConnection(t)
			newWS, newPeer := newGatewayTestConnection(t)
			server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			oldUDP, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = oldUDP.Close() }()
			newUDP, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = newUDP.Close() }()
			aead, err := newVoiceAEAD(voiceModeAES256GCMRTPSize, make([]int, 32))
			if err != nil {
				t.Fatal(err)
			}
			oldClose := make(chan struct{})
			voice := &VoiceConnection{
				LogLevel: LogDebug, wsConn: oldWS, udpConn: oldUDP, close: oldClose,
				speaking: stop, Ready: true, aead: aead, op2: voiceOP2{SSRC: 42},
			}
			paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var pauseOnce sync.Once
			previousLogger := Logger
			Logger = func(_, _ int, format string, args ...interface{}) {
				if format == "called (%d)" && len(args) == 1 && args[0] == flags {
					pauseOnce.Do(func() {
						close(paused)
						<-release
					})
				}
			}
			defer func() {
				closeChannel(release)
				voice.Close()
				<-done
				Logger = previousLogger
			}()
			opus := make(chan []byte, 1)
			opus <- []byte{1, 2, 3}
			close(opus)
			go func() {
				voice.opusSender(oldUDP, oldClose, opus, 48000, 960)
				close(done)
			}()
			select {
			case <-paused:
			case <-time.After(2 * time.Second):
				t.Fatal("audio sender did not reach the speaking barrier")
			}
			if stop {
				// The stop notification follows one audio frame and five silence frames.
				if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				packet := make([]byte, 2048)
				for i := 0; i < 1+voiceOpusSilenceFrameCount; i++ {
					if _, _, err := server.ReadFromUDP(packet); err != nil {
						t.Fatalf("old sender packet %d: %v", i, err)
					}
				}
			}
			voice.Close()
			voice.Lock()
			voice.wsConn, voice.udpConn, voice.close = newWS, newUDP, make(chan struct{})
			voice.speaking, voice.Ready = stop, true
			voice.op2 = voiceOP2{SSRC: 99}
			voice.Unlock()
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old audio sender did not finish")
			}
			voice.RLock()
			speaking, ready := voice.speaking, voice.Ready
			voice.RUnlock()
			if speaking != stop || !ready {
				t.Errorf("old sender changed replacement: speaking=%t, ready=%t", speaking, ready)
			}
			if err := newPeer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			_, payload, err := newPeer.ReadMessage()
			if err == nil {
				t.Errorf("old sender wrote to replacement websocket: %s", payload)
			} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				t.Fatalf("replacement websocket failed unexpectedly: %v", err)
			}
		})
	}
}
