package discordgo

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestStatusHelpersSharePresenceRateLimit(t *testing.T) {
	for _, staggered := range []bool{false, true} {
		t.Run(fmt.Sprintf("staggered=%t", staggered), func(t *testing.T) {
			testStatusHelpersSharePresenceRateLimit(t, staggered)
		})
	}
}

func testStatusHelpersSharePresenceRateLimit(t *testing.T, staggered bool) {
	t.Helper()
	now := time.Unix(1, 0)
	var waits []time.Duration
	completed := 0
	writes := captureGatewayWrites(t, 8, func(session *Session) error {
		session.gatewaySendRateLimiter.now = func() time.Time { return now }
		session.gatewaySendRateLimiter.wait = func(delay time.Duration, _ <-chan struct{}) bool {
			if completed < 5 || completed > 6 {
				t.Errorf("unexpected presence wait after %d status writes", completed)
			}
			waits = append(waits, delay)
			now = now.Add(delay)
			return true
		}
		updates := []func() error{
			func() error { return session.UpdateGameStatus(0, "game") },
			func() error { return session.UpdateWatchStatus(0, "watch") },
			func() error { return session.UpdateStreamingStatus(0, "stream", "https://www.twitch.tv/example") },
			func() error { return session.UpdateListeningStatus("listen") },
			func() error { return session.UpdateCustomStatus("custom") },
			func() error { return session.UpdateStatusComplex(UpdateStatusData{Status: "online"}) },
			func() error { return session.UpdateGameStatus(0, "next") },
		}
		for i, update := range updates {
			if staggered && i == 1 {
				now = now.Add(19 * time.Second)
			}
			if i == 5 {
				// An exhausted presence budget must not delay other gateway events.
				if err := session.RequestGuildMembers("guild", "", 1, "nonce", false); err != nil {
					return err
				}
				if len(waits) != 0 {
					t.Error("presence budget delayed a guild member request")
				}
			}
			if err := update(); err != nil {
				return err
			}
			completed++
		}
		return nil
	})
	wantWaits := []time.Duration{20 * time.Second}
	if staggered {
		// Only the first update expires at t=20; the other four expire at t=39.
		wantWaits = []time.Duration{time.Second, 19 * time.Second}
	}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Errorf("status rate limit waits = %v, want %v", waits, wantWaits)
	}
	for i, write := range writes {
		want := 3
		if i == 5 {
			want = 8
		}
		if write.Op != want {
			t.Errorf("write %d opcode = %d, want %d", i, write.Op, want)
		}
	}
}

func TestPresenceRateLimitWaitStopsOnClose(t *testing.T) {
	conn, peer := newGatewayTestConnection(t)
	session := &Session{LogLevel: -1, wsConn: conn, sequence: new(int64)}
	waiting := make(chan struct{})
	session.gatewaySendRateLimiter.wait = func(_ time.Duration, cancel <-chan struct{}) bool {
		close(waiting)
		<-cancel
		return false
	}
	for i := 0; i < 5; i++ {
		if err := session.UpdateGameStatus(0, "game"); err != nil {
			t.Fatal(err)
		}
		if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		var write gatewayWrite
		if err := peer.ReadJSON(&write); err != nil {
			t.Fatal(err)
		}
		if write.Op != 3 {
			t.Fatalf("opcode = %d, want presence", write.Op)
		}
	}
	done := make(chan struct{})
	var writeErr error
	go func() {
		defer close(done)
		writeErr = session.UpdateCustomStatus("sixth")
	}()
	defer func() {
		_ = session.Close()
		<-done
	}()
	select {
	case <-waiting:
	case <-done:
		t.Fatalf("sixth status update returned without waiting: %v", writeErr)
	case <-time.After(time.Second):
		t.Fatal("sixth status update did not reach the limiter")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if !errors.Is(writeErr, ErrWSNotFound) {
			t.Errorf("canceled status update error = %v, want ErrWSNotFound", writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("status update remained blocked after Close")
	}
}

func TestPresenceRateLimitResetsForNewConnection(t *testing.T) {
	old, _ := newGatewayTestConnection(t)
	replacement, peer := newGatewayTestConnection(t)
	session := &Session{LogLevel: -1, wsConn: old}
	session.gatewaySendRateLimiter.wait = func(time.Duration, <-chan struct{}) bool {
		t.Error("replacement connection inherited the old presence budget")
		return false
	}
	for i := 0; i < 5; i++ {
		if err := session.UpdateGameStatus(0, "game"); err != nil {
			t.Fatal(err)
		}
	}
	// Model Open's connection installation and limiter reset under its lock.
	session.Lock()
	session.wsConn = replacement
	session.gatewaySendRateLimiter.reset(replacement)
	session.Unlock()
	if err := session.UpdateCustomStatus("new connection"); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var write gatewayWrite
	if err := peer.ReadJSON(&write); err != nil || write.Op != 3 {
		t.Fatalf("replacement status opcode=%d, error=%v", write.Op, err)
	}
}
