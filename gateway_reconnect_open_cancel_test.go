package discordgo

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReconnectCanceledBeforeOpenDoesNotDial(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.LogLevel = LogInformational
	session.gateway = "ws://discord.invalid/gateway"
	var attempts atomic.Int32
	session.Dialer = &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		attempts.Add(1)
		return nil, errors.New("test dial failure")
	}}
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oldLogger := Logger
	Logger = func(_, _ int, format string, _ ...interface{}) {
		if format == "trying to reconnect to gateway" {
			close(paused)
			<-release
		}
	}
	defer func() {
		closeChannel(release)
		_ = session.Close()
		<-done
		Logger = oldLogger
	}()
	go func() {
		defer close(done)
		session.reconnect(session.reconnectCancelSignal())
	}()
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not reach the open attempt")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled reconnect did not return")
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("canceled reconnect made %d dial attempts after Close returned", got)
	}
	session.RLock()
	canceled := reconnectCanceled(session.reconnectCancel)
	session.RUnlock()
	if !canceled {
		t.Error("canceled reconnect rearmed session cancellation")
	}
}
