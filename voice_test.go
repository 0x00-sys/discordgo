package discordgo

import (
	"errors"
	"testing"
)

func TestVoiceChangeChannelNilSessionWsConn(t *testing.T) {
	v := &VoiceConnection{
		GuildID: "guild",
		session: &Session{},
	}

	err := v.ChangeChannel("channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChangeChannel() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestVoiceDisconnectNilSessionWsConn(t *testing.T) {
	v := &VoiceConnection{
		GuildID:   "guild",
		sessionID: "session",
		session:   &Session{},
	}

	err := v.Disconnect()
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("Disconnect() error = %v, want %v", err, ErrWSNotFound)
	}
	if v.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", v.sessionID)
	}
}
