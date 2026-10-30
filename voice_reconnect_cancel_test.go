package discordgo

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionCloseCancelsVoiceReconnectBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, err := New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		session.LogLevel = -1
		voice := &VoiceConnection{
			LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
		}
		session.VoiceConnections = map[string]*VoiceConnection{"guild": voice}
		done := make(chan struct{})
		go func() {
			defer close(done)
			voice.reconnect()
		}()
		synctest.Wait()
		// The unavailable gateway makes recovery back off through 1, 2,
		// ... 512 seconds. It is now waiting on its capped 600-second timer.
		time.Sleep(1023 * time.Second)
		synctest.Wait()
		closedAt := time.Now()
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Error("Session.Close left the voice reconnect worker waiting on backoff")
		}
		// Join even on failure; fake time exposes the retained worker's delay.
		<-done
		if delay := time.Since(closedAt); delay != 0 {
			t.Errorf("voice reconnect exited %v after Session.Close, want no backoff delay", delay)
		}
		if voice.reconnecting {
			t.Error("voice reconnecting flag was not cleared")
		}
	})
}
