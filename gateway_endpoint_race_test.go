package discordgo

import (
	"runtime"
	"testing"
	"time"
)

func TestGatewayErrorLoggingConcurrentEndpointUpdate(t *testing.T) {
	oldJitter := gatewayHeartbeatInitialJitter.Swap(func(time.Duration) time.Duration { return 0 })
	defer gatewayHeartbeatInitialJitter.Store(oldJitter)
	for _, path := range []string{"heartbeat", "listener"} {
		t.Run(path, func(t *testing.T) {
			conn, _ := newGatewayTestConnection(t)
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			session := &Session{LogLevel: -1, sequence: new(int64)}
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					// Open refreshes or clears the cached endpoint under this lock.
					session.Lock()
					session.gateway = "wss://replacement.gateway.example"
					session.Unlock()
					runtime.Gosched()
				}
			}()
			for i := 0; i < 100; i++ {
				listening := make(chan interface{})
				session.Lock()
				session.wsConn = conn
				session.listening = listening
				session.lastHeartbeatAck = time.Now().Add(time.Hour)
				session.Unlock()
				if path == "heartbeat" {
					session.heartbeat(conn, listening, 1)
				} else {
					session.listen(conn, listening)
				}
			}
			close(stop)
			<-done
		})
	}
}
