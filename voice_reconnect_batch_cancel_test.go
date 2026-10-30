package discordgo

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func TestVoiceReconnectBatchCancellation(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%v", closeSession), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session, err := New("Bot test")
				if err != nil {
					t.Fatal(err)
				}
				session.LogLevel = -1
				session.VoiceConnections = make(map[string]*VoiceConnection)
				for i := 0; i < 4; i++ {
					guildID := fmt.Sprint(i)
					session.VoiceConnections[guildID] = &VoiceConnection{
						LogLevel: -1, GuildID: guildID, ChannelID: "channel", session: session,
					}
				}
				defer func() { _ = session.Close() }()
				done := make(chan struct{})
				started := time.Now()
				go func() {
					defer close(done)
					session.reconnectVoiceConnections(session.reconnectCancelSignal())
				}()
				// The batch has scheduled its first recovery and is waiting
				// between guilds; individual recovery also waits for the gateway.
				synctest.Wait()
				if closeSession {
					if err := session.Close(); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					select {
					case <-done:
					default:
						t.Error("Session.Close left the voice reconnect batch running")
					}
				}
				// Join even on failure and measure the remaining pacing delay.
				<-done
				want := 3 * time.Second
				if closeSession {
					want = 0
				}
				if elapsed := time.Since(started); elapsed != want {
					t.Errorf("batch elapsed = %v, want %v", elapsed, want)
				}
			})
		})
	}
}
