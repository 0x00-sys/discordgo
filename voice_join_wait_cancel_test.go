package discordgo

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestVoiceJoinWaitCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		closeSession bool
		beforeWait   bool
	}{
		{name: "voice disconnect"},
		{name: "session close", closeSession: true},
		{name: "already disconnected", beforeWait: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				voice := &VoiceConnection{LogLevel: -1, GuildID: "guild", session: session}
				session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
				disconnect := voice.Disconnect
				if tc.closeSession {
					disconnect = session.Close
				}
				if tc.beforeWait {
					if err := disconnect(); err != nil {
						t.Fatal(err)
					}
				}
				result := make(chan error, 1)
				go func() { result <- voice.waitUntilConnected(nil) }()
				synctest.Wait()
				closedAt := time.Now()
				if !tc.beforeWait {
					if err := disconnect(); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
				}
				if len(result) == 0 {
					t.Error("disconnected voice join is still waiting for readiness")
				}
				// Join even on failure to measure the remaining timeout in fake time.
				if err := <-result; !errors.Is(err, ErrWSNotFound) {
					t.Errorf("wait error = %v, want %v", err, ErrWSNotFound)
				}
				if delay := time.Since(closedAt); delay != 0 {
					t.Errorf("wait returned %v after disconnect, want no delay", delay)
				}
			})
		})
	}
}
