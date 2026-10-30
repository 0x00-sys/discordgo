package discordgo

import (
	"errors"
	"sync"
	"testing"
	"testing/synctest"
)

func TestVoiceReconnectLoggingConcurrentJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := &Session{LogLevel: -1, VoiceConnections: make(map[string]*VoiceConnection)}
		voice := &VoiceConnection{
			LogLevel: -1, GuildID: "guild", ChannelID: "channel", session: session,
			// An existing recovery worker makes duplicate reconnects return.
			reconnecting: true,
		}
		session.VoiceConnections[voice.GuildID] = voice
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				// Gateway recovery snapshots the registered voice connections
				// while a caller can independently request a channel join.
				session.reconnectVoiceConnections(session.reconnectCancelSignal())
			}
		}()
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				_, err := session.ChannelVoiceJoin("guild", "channel", false, false)
				// No gateway is installed: the public join still configures the
				// registered voice connection before its send returns this error.
				if !errors.Is(err, ErrWSNotFound) {
					t.Errorf("ChannelVoiceJoin error = %v, want ErrWSNotFound", err)
					return
				}
			}
		}()
		workers.Wait()
		synctest.Wait()
	})
}
