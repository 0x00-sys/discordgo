package discordgo

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionCloseCancelsVoiceReconnectBackoff(t *testing.T) {
	testVoiceReconnectBackoffCancellation(t, func(session *Session, _ *VoiceConnection) error {
		return session.Close()
	})
}

func TestVoiceDisconnectCancelsReconnectBackoff(t *testing.T) {
	testVoiceReconnectBackoffCancellation(t, func(session *Session, voice *VoiceConnection) error {
		// Permanent disconnection must remain safe to repeat.
		for i := 0; i < 2; i++ {
			if err := voice.Disconnect(); err != nil {
				return err
			}
		}
		select {
		case <-session.reconnectCancel:
			t.Error("voice disconnect canceled the entire session")
		default:
		}
		return nil
	})
}

func testVoiceReconnectBackoffCancellation(t *testing.T, disconnect func(*Session, *VoiceConnection) error) {
	t.Helper()
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
			voice.reconnectWithResume(true, nil, nil)
		}()
		synctest.Wait()
		// The unavailable gateway makes recovery back off through 1, 2,
		// ... 512 seconds. It is now waiting on its capped 600-second timer.
		time.Sleep(1023 * time.Second)
		synctest.Wait()
		closedAt := time.Now()
		if err := disconnect(session, voice); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Error("disconnect left the voice reconnect worker waiting on backoff")
		}
		// Join even on failure; fake time exposes the retained worker's delay.
		<-done
		if delay := time.Since(closedAt); delay != 0 {
			t.Errorf("voice reconnect exited %v after disconnect, want no backoff delay", delay)
		}
		if voice.reconnecting {
			t.Error("voice reconnecting flag was not cleared")
		}
	})
}
