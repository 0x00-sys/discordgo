package discordgo

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type speakingOrderBufferPool struct{ writes int }

func (*speakingOrderBufferPool) Get() interface{} { return nil }
func (p *speakingOrderBufferPool) Put(interface{}) {
	p.writes++
	if p.writes%128 != 0 {
		time.Sleep(2 * time.Millisecond)
	}
}

func TestVoiceSpeakingStateFollowsWriteOrder(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	messages := make(chan VoiceSpeakingFlags, 128)
	peerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(peerDone)
		defer close(messages)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Error(err)
			return
		}
		for {
			var message struct {
				Data struct {
					Speaking VoiceSpeakingFlags `json:"speaking"`
				} `json:"d"`
			}
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			messages <- message.Data.Speaking
		}
	}))
	defer server.Close()
	// Repeated callers contend while buffer recycling is delayed. The last
	// write has no recycling delay, so it can finish before an older caller
	// resumes after handing off the websocket write lock.
	dialer := websocket.Dialer{WriteBufferPool: &speakingOrderBufferPool{}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); <-peerDone }()
	voice := &VoiceConnection{LogLevel: -1, wsConn: conn, op2: voiceOP2{SSRC: 42}}
	for round := 0; round < 4; round++ {
		start := make(chan struct{})
		results := make(chan error, 128)
		var workers sync.WaitGroup
		for i := 0; i < 32; i++ {
			workers.Add(1)
			go func(speaking bool) {
				defer workers.Done()
				<-start
				for range 4 {
					results <- voice.Speaking(speaking)
					speaking = !speaking
				}
			}(i%2 == 0)
		}
		close(start)
		workers.Wait()
		var last VoiceSpeakingFlags
		for range 128 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
			var ok bool
			if last, ok = <-messages; !ok {
				t.Fatal("peer closed before receiving all speaking updates")
			}
		}
		if voice.speaking != (last != 0) {
			t.Fatalf("round %d: local speaking=%t, last received speaking=%d", round, voice.speaking, last)
		}
	}
}
