package discordgo

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"
)

func TestOnEventOp1NilWsConn(t *testing.T) {
	seq := int64(0)
	s := &Session{sequence: &seq}

	_, err := s.onEvent(websocket.TextMessage, []byte(`{"op":1,"s":0,"t":"","d":null}`))
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("onEvent() error = %v, want %v", err, ErrWSNotFound)
	}
}

func TestChannelVoiceJoinManualNilWsConn(t *testing.T) {
	s := &Session{}

	err := s.ChannelVoiceJoinManual("guild", "channel", false, false)
	if !errors.Is(err, ErrWSNotFound) {
		t.Fatalf("ChannelVoiceJoinManual() error = %v, want %v", err, ErrWSNotFound)
	}
}
